package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	defaultGRPCAddr        = ":50051"
	defaultZitiServiceName = "runner"
	defaultStorageSize     = "10Gi"
	defaultLogLevel        = "info"
)

// Config captures runtime configuration derived from the environment.
type Config struct {
	GRPCAddr         string
	Namespace        string
	DisableZiti      bool
	ZitiIdentityFile string
	ZitiServiceName  string
	StorageClass     *string
	StorageSize      string
	LogLevel         string
}

// Load reads configuration from environment variables, applying defaults when
// values are not provided. Returns an error when supplied values are invalid.
func Load() (Config, error) {
	var cfg Config

	cfg.GRPCAddr = readEnv("GRPC_ADDR", defaultGRPCAddr)
	cfg.Namespace = strings.TrimSpace(os.Getenv("KUBE_NAMESPACE"))
	if cfg.Namespace == "" {
		return Config{}, fmt.Errorf("KUBE_NAMESPACE is required")
	}
	if value, ok := os.LookupEnv("DISABLE_ZITI"); ok {
		disableZiti, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("invalid DISABLE_ZITI: %w", err)
		}
		cfg.DisableZiti = disableZiti
	}

	cfg.ZitiIdentityFile = strings.TrimSpace(os.Getenv("ZITI_IDENTITY_FILE"))
	cfg.ZitiServiceName = readEnv("ZITI_SERVICE_NAME", defaultZitiServiceName)
	if !cfg.DisableZiti && cfg.ZitiIdentityFile == "" {
		return Config{}, fmt.Errorf("ZITI_IDENTITY_FILE is required unless DISABLE_ZITI is true")
	}

	storageClass := strings.TrimSpace(os.Getenv("PVC_STORAGE_CLASS"))
	if storageClass != "" {
		cfg.StorageClass = &storageClass
	}

	cfg.StorageSize = readEnv("PVC_STORAGE_SIZE", defaultStorageSize)
	if _, err := resource.ParseQuantity(cfg.StorageSize); err != nil {
		return Config{}, fmt.Errorf("invalid PVC_STORAGE_SIZE: %w", err)
	}

	cfg.LogLevel = normalizeLogLevel(readEnv("LOG_LEVEL", defaultLogLevel))

	return cfg, nil
}

func readEnv(key, def string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return def
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return "info"
	case "debug":
		return "debug"
	case "warn", "warning":
		return "warn"
	case "error":
		return "error"
	default:
		return "info"
	}
}
