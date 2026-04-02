package config

import (
	"os"
	"reflect"
	"testing"
)

const testRunnerID = "123e4567-e89b-12d3-a456-426614174000"

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

func TestLoadRunnerIDRequiredWhenZitiEnabled(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ZITI_ENABLED", "true")
	unsetEnv(t, "RUNNER_ID")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing RUNNER_ID")
	}
}

func TestLoadRunnerIDInvalid(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ZITI_ENABLED", "true")
	t.Setenv("RUNNER_ID", "not-a-uuid")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid RUNNER_ID")
	}
}

func TestLoadZitiRoleAttributesDefault(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ZITI_ENABLED", "true")
	unsetEnv(t, "ZITI_ROLE_ATTRIBUTES")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	expected := []string{"runners"}
	if !reflect.DeepEqual(cfg.ZitiRoleAttributes, expected) {
		t.Fatalf("expected ziti role attributes %v, got %v", expected, cfg.ZitiRoleAttributes)
	}
}

func TestLoadZitiRoleAttributesCustom(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ZITI_ENABLED", "true")
	t.Setenv("ZITI_ROLE_ATTRIBUTES", "runners, prod,staging")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	expected := []string{"runners", "prod", "staging"}
	if !reflect.DeepEqual(cfg.ZitiRoleAttributes, expected) {
		t.Fatalf("expected ziti role attributes %v, got %v", expected, cfg.ZitiRoleAttributes)
	}
}

func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KUBE_NAMESPACE", "test-namespace")
	t.Setenv("GRPC_ADDR", defaultGRPCAddr)
	t.Setenv("PVC_STORAGE_SIZE", defaultStorageSize)
	t.Setenv("PVC_STORAGE_CLASS", "")
	t.Setenv("LOG_LEVEL", defaultLogLevel)
	t.Setenv("ZITI_MANAGEMENT_ADDRESS", defaultZitiManagementAddress)
	t.Setenv("RUNNER_ID", testRunnerID)
	t.Setenv("ZITI_ROLE_ATTRIBUTES", defaultZitiRoleAttributes)
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
