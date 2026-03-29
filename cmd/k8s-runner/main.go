package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/openziti/sdk-golang/ziti"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
	zitimgmtv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/ziti_management/v1"
	"github.com/agynio/k8s-runner/internal/config"
	"github.com/agynio/k8s-runner/internal/kube"
	"github.com/agynio/k8s-runner/internal/logging"
	"github.com/agynio/k8s-runner/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "k8s-runner failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	kubeClient, err := kube.New()
	if err != nil {
		return fmt.Errorf("init kube client: %w", err)
	}

	grpcServer := grpc.NewServer()
	runnerv1.RegisterRunnerServiceServer(
		grpcServer,
		server.New(server.Options{
			Clientset:    kubeClient.Clientset,
			RestConfig:   kubeClient.RestConfig,
			Namespace:    cfg.Namespace,
			RunnerID:     cfg.RunnerID,
			StorageClass: cfg.StorageClass,
			StorageSize:  cfg.StorageSize,
			Logger:       logger,
		}),
	)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	startServe := func(listener net.Listener, label string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("gRPC server starting", zap.String("listener", label), zap.String("addr", listener.Addr().String()))
			err := grpcServer.Serve(listener)
			if errors.Is(err, grpc.ErrServerStopped) {
				err = nil
			}
			if err != nil {
				errCh <- err
			}
		}()
	}

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.GRPCAddr, err)
	}
	defer listener.Close()
	startServe(listener, "tcp")

	if cfg.ZitiEnabled {
		zitiConn, err := grpc.DialContext(ctx, cfg.ZitiManagementAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dial ziti management: %w", err)
		}
		defer zitiConn.Close()

		zitiMgmtClient := zitimgmtv1.NewZitiManagementServiceClient(zitiConn)
		identityResponse, err := zitiMgmtClient.RequestServiceIdentity(ctx, &zitimgmtv1.RequestServiceIdentityRequest{
			ServiceType: zitimgmtv1.ServiceType_SERVICE_TYPE_RUNNER,
		})
		if err != nil {
			return fmt.Errorf("request ziti service identity: %w", err)
		}

		zitiConfig := &ziti.Config{}
		if err := json.Unmarshal(identityResponse.IdentityJson, zitiConfig); err != nil {
			return fmt.Errorf("parse ziti identity: %w", err)
		}

		zitiContext, err := ziti.NewContext(zitiConfig)
		if err != nil {
			return fmt.Errorf("create ziti context: %w", err)
		}
		defer zitiContext.Close()

		zitiListener, err := zitiContext.ListenWithOptions(cfg.ZitiServiceName, ziti.DefaultListenOptions())
		if err != nil {
			return fmt.Errorf("listen on ziti service %s: %w", cfg.ZitiServiceName, err)
		}
		defer zitiListener.Close()
		startServe(zitiListener, "ziti")

		go renewLease(ctx, logger, zitiMgmtClient, identityResponse.ZitiIdentityId, cfg.ZitiLeaseRenewalInterval)
	}

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		grpcServer.GracefulStop()
	}

	wg.Wait()
	return nil
}

func renewLease(ctx context.Context, logger *zap.Logger, client zitimgmtv1.ZitiManagementServiceClient, identityID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			if _, err := client.ExtendIdentityLease(ctx, &zitimgmtv1.ExtendIdentityLeaseRequest{ZitiIdentityId: identityID}); err != nil {
				logger.Warn("failed to extend ziti lease", zap.Error(err))
			}
		}
	}
}
