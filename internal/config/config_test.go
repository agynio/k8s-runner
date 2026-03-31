package config

import (
	"os"
	"testing"
)

func TestLoadZitiEnrollmentTimeoutDefault(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ZITI_ENABLED", "true")
	unsetEnv(t, "ZITI_ENROLLMENT_TIMEOUT")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ZitiEnrollmentTimeout != defaultZitiEnrollmentTimeout {
		t.Fatalf("expected ziti enrollment timeout %s, got %s", defaultZitiEnrollmentTimeout, cfg.ZitiEnrollmentTimeout)
	}
}

func TestLoadZitiEnrollmentTimeoutInvalid(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ZITI_ENABLED", "true")
	t.Setenv("ZITI_ENROLLMENT_TIMEOUT", "0s")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid ziti enrollment timeout")
	}
}

func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KUBE_NAMESPACE", "test-namespace")
	t.Setenv("RUNNER_ID", "runner-1")
	t.Setenv("GRPC_ADDR", defaultGRPCAddr)
	t.Setenv("PVC_STORAGE_SIZE", defaultStorageSize)
	t.Setenv("PVC_STORAGE_CLASS", "")
	t.Setenv("LOG_LEVEL", defaultLogLevel)
	t.Setenv("ZITI_MANAGEMENT_ADDRESS", defaultZitiManagementAddress)
	t.Setenv("ZITI_SERVICE_NAME", defaultZitiServiceName)
	t.Setenv("ZITI_LEASE_RENEWAL_INTERVAL", defaultZitiLeaseRenewalInterval.String())
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	value, ok := os.LookupEnv(key)
	if ok {
		t.Cleanup(func() {
			_ = os.Setenv(key, value)
		})
	} else {
		t.Cleanup(func() {
			_ = os.Unsetenv(key)
		})
	}
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}
