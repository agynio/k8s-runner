package server

import (
	"context"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/agynio/runner-core/backend"

	"github.com/agynio/k8s-runner/internal/config"
	"github.com/agynio/k8s-runner/internal/reporter"
)

const (
	managedByLabelKey           = "app.kubernetes.io/managed-by"
	managedByLabelValue         = "k8s-runner"
	workloadManagedByLabelKey   = "agyn.dev/managed-by"
	workloadManagedByLabelValue = "agents-orchestrator"
	workloadIDLabelKey          = "agyn.io/workload-id"
	workloadKeyLabelKey         = "workload_key"
	volumeKeyLabelKey           = "volume_key"
	pvcAnnotationKey            = "agyn.io/pvc-names"
	secretAnnotationKey         = "agyn.io/pull-secret-names"
	inlineFilesVolumeName       = "inline-files"
	touchedAtAnnotationKey      = "agyn.io/touched-at"
)

// Server is the Kubernetes backend of the RunnerService gRPC API. runner-core
// serves the RPCs and calls it for everything that touches a Pod, a
// PersistentVolumeClaim or a Secret.
type Server struct {
	clientset                 kubernetes.Interface
	restConfig                *rest.Config
	namespace                 string
	storageClass              *string
	storageSize               string
	catalog                   config.Catalog
	logger                    *zap.Logger
	capabilityImplementations config.CapabilityImplementations
	workloadPriorityClass     string
}

var _ backend.Backend = (*Server)(nil)

// Options defines required inputs for constructing a Server.
type Options struct {
	Clientset                 kubernetes.Interface
	RestConfig                *rest.Config
	Namespace                 string
	StorageClass              *string
	StorageSize               string
	Catalog                   config.Catalog
	Logger                    *zap.Logger
	CapabilityImplementations config.CapabilityImplementations
	WorkloadPriorityClass     string
}

// New constructs the Kubernetes backend.
func New(options Options) *Server {
	return &Server{
		clientset:                 options.Clientset,
		restConfig:                options.RestConfig,
		namespace:                 options.Namespace,
		storageClass:              options.StorageClass,
		storageSize:               options.StorageSize,
		catalog:                   options.Catalog,
		logger:                    options.Logger,
		capabilityImplementations: options.CapabilityImplementations,
		workloadPriorityClass:     options.WorkloadPriorityClass,
	}
}

// WatchWorkloadState hands every transition of the Pods this runner manages
// to observer until ctx ends.
func (s *Server) WatchWorkloadState(ctx context.Context, observer backend.Observer) error {
	return reporter.Watch(ctx, s.clientset, s.namespace, observer)
}
