package config

import (
	"fmt"
	"strings"

	"github.com/agynio/runner-core/catalog"
)

// Catalog is what this runner offers: the compute sizes, storage tiers and
// capabilities it can honour. It is declared here rather than in the platform
// because every entry needs a runner-side implementation anyway — a flavor is
// only real if this runner can allocate those resources, and a storage class is
// only real if it maps to a StorageClass in this cluster.
//
// The platform sees the names and sizes (Reported); the Kubernetes mapping
// behind each entry stays here.
type Catalog struct {
	Flavors        []FlavorEntry       `yaml:"flavors"`
	StorageClasses []StorageClassEntry `yaml:"storageClasses"`
	Capabilities   []string            `yaml:"capabilities"`
}

type FlavorEntry struct {
	Name       string `yaml:"name"`
	Default    bool   `yaml:"default"`
	Deprecated bool   `yaml:"deprecated"`
	// Resources size the workload's main container, SidecarResources each of
	// its sidecars. One name covers both so a workload asks for a size, not a
	// per-container budget. Unset SidecarResources leaves sidecars unsized.
	Resources        ComputeResources `yaml:"resources"`
	SidecarResources ComputeResources `yaml:"sidecarResources"`
}

// ComputeResources is a size as the platform understands it.
type ComputeResources = catalog.ComputeResources

type StorageClassEntry struct {
	Name string `yaml:"name"`
	// StorageClassName is the Kubernetes StorageClass this entry maps to. Empty
	// means the cluster default, which is what an unset class has always used.
	StorageClassName string `yaml:"storageClassName"`
	Default          bool   `yaml:"default"`
	Deprecated       bool   `yaml:"deprecated"`
}

// LoadCatalog reads the catalog from path. A missing path is not an error: a
// runner with no declared catalog reports nothing and simply offers no named
// entries.
func LoadCatalog(path string) (Catalog, error) {
	var declared Catalog
	if err := catalog.LoadFile(path, &declared); err != nil {
		return Catalog{}, err
	}
	if err := declared.validate(); err != nil {
		return Catalog{}, fmt.Errorf("catalog %s: %w", strings.TrimSpace(path), err)
	}
	return declared, nil
}

// validate catches locally what the platform would reject anyway, so a bad
// configuration fails at startup with a clear message instead of as a rejected
// report. The platform-facing part is runner-core's check; the sidecar budget
// is this runner's own.
func (c Catalog) validate() error {
	if err := c.Reported().Validate(); err != nil {
		return err
	}
	for _, flavor := range c.Flavors {
		// Sidecar sizing is opt-in, but half of it is a typo rather than a
		// choice: a flavor that sets two of the four would silently size
		// sidecars by a budget nobody wrote.
		if flavor.SidecarResources.IsZero() {
			continue
		}
		if field, missing := flavor.SidecarResources.MissingField(); missing {
			return fmt.Errorf("flavor %q: sidecarResources.%s is empty", strings.TrimSpace(flavor.Name), field)
		}
	}
	return nil
}

// Reported is the platform-facing view of this catalog: the entries by name
// and size, without the Kubernetes mapping behind each one.
func (c Catalog) Reported() catalog.Catalog {
	flavors := make([]catalog.Flavor, 0, len(c.Flavors))
	for _, flavor := range c.Flavors {
		flavors = append(flavors, catalog.Flavor{
			Name:       flavor.Name,
			Default:    flavor.Default,
			Deprecated: flavor.Deprecated,
			Resources:  flavor.Resources,
		})
	}
	storageClasses := make([]catalog.StorageClass, 0, len(c.StorageClasses))
	for _, class := range c.StorageClasses {
		storageClasses = append(storageClasses, catalog.StorageClass{
			Name:       class.Name,
			Default:    class.Default,
			Deprecated: class.Deprecated,
		})
	}
	return catalog.Catalog{
		Flavors:        flavors,
		StorageClasses: storageClasses,
		Capabilities:   c.Capabilities,
	}
}

// FlavorFor maps a catalog entry name to the sizes backing it. An unknown name
// resolves to nothing, which is what makes an unresolvable reference fail the
// start rather than silently land unsized.
func (c Catalog) FlavorFor(name string) (FlavorEntry, bool) {
	for _, flavor := range c.Flavors {
		if flavor.Name == name {
			return flavor, true
		}
	}
	return FlavorEntry{}, false
}

// StorageClassNameFor maps a catalog entry name to the Kubernetes
// StorageClass backing it. An unknown name resolves to nothing, which is what
// makes an unresolvable reference fail scheduling rather than silently land on
// the cluster default.
func (c Catalog) StorageClassNameFor(name string) (string, bool) {
	for _, class := range c.StorageClasses {
		if class.Name == name {
			return class.StorageClassName, true
		}
	}
	return "", false
}
