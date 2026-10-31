package main

import (
	"reflect"
	"testing"

	"github.com/agynio/k8s-runner/internal/config"
)

// The catalog listed capabilities by hand while the runner decided what it
// could serve from its configured implementations, and the two disagreed:
// docker was configured and working, the catalog said nothing, and the
// Orchestrator refused to place any agent that asked for it -- "no eligible
// runners found (required capabilities: [docker])".
func TestImplementedCapabilitiesReportsConfiguredDocker(t *testing.T) {
	cfg := config.Config{CapabilityImplementations: config.CapabilityImplementations{
		Docker: config.DockerImplementationRootless,
	}}

	if got := implementedCapabilities(cfg); !reflect.DeepEqual(got, []string{"docker"}) {
		t.Fatalf("expected [docker], got %v", got)
	}
}

// Without an implementation the runner cannot serve it, so it must not claim it.
func TestImplementedCapabilitiesOmitsUnconfiguredDocker(t *testing.T) {
	if got := implementedCapabilities(config.Config{}); len(got) != 0 {
		t.Fatalf("expected no capabilities, got %v", got)
	}
}
