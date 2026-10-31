package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	runnercore "github.com/agynio/runner-core"
	"github.com/agynio/runner-core/logging"

	"github.com/agynio/k8s-runner/internal/config"
	"github.com/agynio/k8s-runner/internal/kube"
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

	// Everything that is not Kubernetes -- enrollment, the catalog report, the
	// Runner API server, state reporting -- is runner-core's. This binary is
	// the Pod backend behind it.
	return runnercore.Run(ctx, runnercore.Options{
		Config: cfg.Config,
		Logger: logger,
		Backend: server.New(server.Options{
			Clientset:                 kubeClient.Clientset,
			RestConfig:                kubeClient.RestConfig,
			Namespace:                 cfg.Namespace,
			StorageClass:              cfg.StorageClass,
			StorageSize:               cfg.StorageSize,
			Catalog:                   cfg.Catalog,
			Logger:                    logger,
			CapabilityImplementations: cfg.CapabilityImplementations,
			WorkloadPriorityClass:     cfg.WorkloadPriorityClass,
		}),
		Catalog:      cfg.Catalog.Reported(),
		Capabilities: implementedCapabilities(cfg),
	})
}

// implementedCapabilities is what this runner can actually do, which is what
// it has an implementation configured for. The catalog listed them separately
// and the two silently disagreed: docker was configured and working, the
// catalog said nothing, and the Orchestrator placed no agent that asked for it
// -- "no eligible runners found (required capabilities: [docker])" -- with the
// runner right there able to serve them.
//
// runner-core merges these with the catalog's own entries, so a capability can
// still be advertised ahead of the implementation landing, but it no longer
// has to be kept in step by hand for the ones the runner already implements.
func implementedCapabilities(cfg config.Config) []string {
	if cfg.CapabilityImplementations.Docker != "" {
		return []string{config.CapabilityDocker}
	}
	return nil
}
