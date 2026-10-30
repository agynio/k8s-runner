package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	coreconfig "github.com/agynio/runner-core/config"
	"k8s.io/apimachinery/pkg/api/resource"
)

const defaultStorageSize = "10Gi"

// Config captures runtime configuration derived from the environment: what
// every runner shares, read by runner-core, and what is this runner's own.
type Config struct {
	coreconfig.Config
	Namespace                 string
	StorageClass              *string
	StorageSize               string
	CapabilityImplementations CapabilityImplementations
	// Catalog is what this runner reports it offers. Declared in the runner's
	// own configuration, since every entry needs an implementation here.
	Catalog Catalog
	// WorkloadPriorityClass is named on every workload Pod; empty leaves it unset.
	WorkloadPriorityClass string
}

// CapabilityDocker is the capability name a workload asks for and the runner
// reports. Named here rather than in the server package so the startup report
// and the request handling cannot drift apart.
const CapabilityDocker = "docker"

type DockerImplementation string

const (
	DockerImplementationRootless   DockerImplementation = "rootless"
	DockerImplementationPrivileged DockerImplementation = "privileged"
	DockerImplementationKataQemu   DockerImplementation = "kata-qemu"
	DockerImplementationKataFc     DockerImplementation = "kata-fc"
)

type CapabilityImplementations struct {
	Docker DockerImplementation
}

// Load reads configuration from environment variables, applying defaults when
// values are not provided. Returns an error when supplied values are invalid.
func Load() (Config, error) {
	var cfg Config

	cfg.Namespace = strings.TrimSpace(os.Getenv("KUBE_NAMESPACE"))
	if cfg.Namespace == "" {
		return Config{}, fmt.Errorf("KUBE_NAMESPACE is required")
	}

	shared, err := coreconfig.Load()
	if err != nil {
		return Config{}, err
	}
	cfg.Config = shared

	catalog, err := LoadCatalog(cfg.CatalogPath)
	if err != nil {
		return Config{}, err
	}
	cfg.Catalog = catalog

	storageClass := strings.TrimSpace(os.Getenv("PVC_STORAGE_CLASS"))
	if storageClass != "" {
		cfg.StorageClass = &storageClass
	}

	cfg.StorageSize = coreconfig.ReadEnv("PVC_STORAGE_SIZE", defaultStorageSize)
	if _, err := resource.ParseQuantity(cfg.StorageSize); err != nil {
		return Config{}, fmt.Errorf("invalid PVC_STORAGE_SIZE: %w", err)
	}

	cfg.WorkloadPriorityClass = strings.TrimSpace(os.Getenv("WORKLOAD_PRIORITY_CLASS"))

	capabilityConfig := strings.TrimSpace(os.Getenv("CAPABILITY_IMPLEMENTATIONS"))
	if capabilityConfig != "" {
		implementations, err := parseCapabilityImplementations(capabilityConfig)
		if err != nil {
			return Config{}, err
		}
		cfg.CapabilityImplementations = implementations
	}

	return cfg, nil
}

func parseCapabilityImplementations(raw string) (CapabilityImplementations, error) {
	if raw == "" {
		return CapabilityImplementations{}, nil
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return CapabilityImplementations{}, fmt.Errorf("invalid CAPABILITY_IMPLEMENTATIONS: %w", err)
	}
	if len(parsed) == 0 {
		return CapabilityImplementations{}, nil
	}
	var implementations CapabilityImplementations
	for key, value := range parsed {
		capability := strings.TrimSpace(key)
		if capability == "" {
			return CapabilityImplementations{}, fmt.Errorf("invalid CAPABILITY_IMPLEMENTATIONS key")
		}
		implementation := strings.ToLower(strings.TrimSpace(value))
		switch capability {
		case "docker":
			switch DockerImplementation(implementation) {
			case DockerImplementationRootless:
				implementations.Docker = DockerImplementationRootless
			case DockerImplementationPrivileged:
				implementations.Docker = DockerImplementationPrivileged
			case DockerImplementationKataQemu:
				implementations.Docker = DockerImplementationKataQemu
			case DockerImplementationKataFc:
				implementations.Docker = DockerImplementationKataFc
			default:
				return CapabilityImplementations{}, fmt.Errorf("invalid docker capability implementation %q", value)
			}
		default:
			return CapabilityImplementations{}, fmt.Errorf("unknown capability implementation %q", capability)
		}
	}
	return implementations, nil
}
