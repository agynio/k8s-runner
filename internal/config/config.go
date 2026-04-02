package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	defaultGRPCAddr              = ":50051"
	defaultZitiManagementAddress = "ziti-management:50051"
	defaultZitiEnrollmentTimeout = 2 * time.Minute
	defaultStorageSize           = "10Gi"
	defaultLogLevel              = "info"
	defaultZitiRoleAttributes    = "runners"
)

// Config captures runtime configuration derived from the environment.
type Config struct {
	GRPCAddr              string
	Namespace             string
	ZitiEnabled           bool
	ZitiManagementAddress string
	ZitiEnrollmentTimeout time.Duration
	RunnerID              string
	ZitiRoleAttributes    []string
	StorageClass          *string
	StorageSize           string
	LogLevel              string
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

	var err error
	cfg.ZitiEnabled, err = readBool("ZITI_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	if cfg.ZitiEnabled {
		cfg.ZitiManagementAddress = readEnv("ZITI_MANAGEMENT_ADDRESS", defaultZitiManagementAddress)

		runnerID := strings.TrimSpace(os.Getenv("RUNNER_ID"))
		if runnerID == "" {
			return Config{}, fmt.Errorf("RUNNER_ID is required when ZITI_ENABLED is true")
		}
		if _, err := uuid.Parse(runnerID); err != nil {
			return Config{}, fmt.Errorf("invalid RUNNER_ID: %w", err)
		}
		cfg.RunnerID = runnerID

		roleAttributes, err := readRoleAttributes("ZITI_ROLE_ATTRIBUTES", defaultZitiRoleAttributes)
		if err != nil {
			return Config{}, err
		}
		cfg.ZitiRoleAttributes = roleAttributes

		enrollmentTimeout, err := readDuration("ZITI_ENROLLMENT_TIMEOUT", defaultZitiEnrollmentTimeout)
		if err != nil {
			return Config{}, err
		}
		if enrollmentTimeout <= 0 {
			return Config{}, fmt.Errorf("ZITI_ENROLLMENT_TIMEOUT must be greater than 0")
		}
		cfg.ZitiEnrollmentTimeout = enrollmentTimeout
	} else {
		cfg.ZitiEnrollmentTimeout = defaultZitiEnrollmentTimeout
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

func readRoleAttributes(key string, def string) ([]string, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		value = def
	}
	roleAttributes := splitRoleAttributes(value)
	if len(roleAttributes) == 0 {
		return nil, fmt.Errorf("%s must contain at least one value", key)
	}
	return roleAttributes, nil
}

func splitRoleAttributes(value string) []string {
	parts := strings.Split(value, ",")
	roleAttributes := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		roleAttributes = append(roleAttributes, trimmed)
	}
	return roleAttributes
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
