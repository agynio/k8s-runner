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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
	zitimgmtv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/ziti_management/v1"
	"github.com/agynio/k8s-runner/internal/config"
	"github.com/agynio/k8s-runner/internal/kube"
	"github.com/agynio/k8s-runner/internal/logging"
	"github.com/agynio/k8s-runner/internal/server"
)

const (
	retryInitialBackoff = 1 * time.Second
	retryMaxBackoff     = 15 * time.Second
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
			StorageClass: cfg.StorageClass,
			StorageSize:  cfg.StorageSize,
			Logger:       logger,
		}),
	)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	var zitiMgmtClient zitimgmtv1.ZitiManagementServiceClient
	var identityResponse *zitimgmtv1.CreateRunnerIdentityResponse

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

		zitiMgmtClient = zitimgmtv1.NewZitiManagementServiceClient(zitiConn)

		enrollmentCtx, cancel := context.WithTimeout(ctx, cfg.ZitiEnrollmentTimeout)
		defer cancel()

		if err := retryWithBackoff(enrollmentCtx, logger, "ziti enrollment", func(attemptCtx context.Context) error {
			var requestErr error
			identityResponse, requestErr = zitiMgmtClient.CreateRunnerIdentity(attemptCtx, &zitimgmtv1.CreateRunnerIdentityRequest{
				RunnerId:       cfg.RunnerID,
				RoleAttributes: cfg.ZitiRoleAttributes,
			})
			return requestErr
		}); err != nil {
			return fmt.Errorf("create ziti runner identity: %w", err)
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

		zitiListener, err := zitiContext.ListenWithOptions(identityResponse.ZitiServiceName, ziti.DefaultListenOptions())
		if err != nil {
			return fmt.Errorf("listen on ziti service %s: %w", identityResponse.ZitiServiceName, err)
		}
		defer zitiListener.Close()
		startServe(zitiListener, "ziti")
	}

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		grpcServer.GracefulStop()
		if identityResponse != nil && zitiMgmtClient != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := zitiMgmtClient.DeleteRunnerIdentity(cleanupCtx, &zitimgmtv1.DeleteRunnerIdentityRequest{
				ZitiIdentityId: identityResponse.ZitiIdentityId,
				ZitiServiceId:  identityResponse.ZitiServiceId,
			}); err != nil {
				logger.Warn("failed to delete ziti runner identity", zap.Error(err))
			}
		}
	}

	wg.Wait()
	return nil
}

func retryWithBackoff(ctx context.Context, logger *zap.Logger, operationName string, fn func(context.Context) error) error {
	backoff := retryInitialBackoff
	attempt := 1
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !isRetryableGrpcError(err) {
			return err
		}

		delay := backoff
		if delay > retryMaxBackoff {
			delay = retryMaxBackoff
		}

		logger.Warn(
			"operation failed, retrying",
			zap.String("operation", operationName),
			zap.Int("attempt", attempt),
			zap.Duration("backoff", delay),
			zap.Error(err),
		)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		backoff *= 2
		if backoff > retryMaxBackoff {
			backoff = retryMaxBackoff
		}
		attempt++
	}
}

func isRetryableGrpcError(err error) bool {
	statusErr, ok := status.FromError(err)
	if !ok {
		return false
	}
	return statusErr.Code() == codes.Unavailable || statusErr.Code() == codes.Unknown
}
