package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	defaultGRPCAddr                 = ":50051"
	defaultZitiManagementAddress    = "ziti-management:50051"
	defaultZitiLeaseRenewalInterval = 2 * time.Minute
	defaultZitiServiceName          = "runner"
	defaultStorageSize              = "10Gi"
	defaultLogLevel                 = "info"
)

// Config captures runtime configuration derived from the environment.
type Config struct {
	GRPCAddr                 string
	Namespace                string
	RunnerID                 string
	ZitiEnabled              bool
	ZitiManagementAddress    string
	ZitiLeaseRenewalInterval time.Duration
	ZitiServiceName          string
	StorageClass             *string
	StorageSize              string
	LogLevel                 string
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

	cfg.RunnerID = strings.TrimSpace(os.Getenv("RUNNER_ID"))
	if cfg.RunnerID == "" {
		return Config{}, fmt.Errorf("RUNNER_ID is required")
	}

	var err error
	cfg.ZitiEnabled, err = readBool("ZITI_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	if cfg.ZitiEnabled {
		cfg.ZitiManagementAddress = readEnv("ZITI_MANAGEMENT_ADDRESS", defaultZitiManagementAddress)
		cfg.ZitiServiceName = readEnv("ZITI_SERVICE_NAME", defaultZitiServiceName)

		leaseInterval, err := readDuration("ZITI_LEASE_RENEWAL_INTERVAL", defaultZitiLeaseRenewalInterval)
		if err != nil {
			return Config{}, err
		}
		if leaseInterval <= 0 {
			return Config{}, fmt.Errorf("ZITI_LEASE_RENEWAL_INTERVAL must be greater than 0")
		}
		cfg.ZitiLeaseRenewalInterval = leaseInterval
	} else {
		cfg.ZitiServiceName = readEnv("ZITI_SERVICE_NAME", defaultZitiServiceName)
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

func readBool(key string, def bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	parsed, err := strconv.ParseBool(trimmed)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
}

func readDuration(key string, def time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("%s must be a duration", key)
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
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
