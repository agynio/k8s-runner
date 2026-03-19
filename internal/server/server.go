package server

import (
	"sync"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

const (
	managedByLabelKey      = "app.kubernetes.io/managed-by"
	managedByLabelValue    = "k8s-runner"
	workloadIDLabelKey     = "agyn.io/workload-id"
	pvcAnnotationKey       = "agyn.io/pvc-names"
	touchedAtAnnotationKey = "agyn.io/touched-at"
)

// Server implements the RunnerService gRPC API against the Kubernetes API.
type Server struct {
	runnerv1.UnimplementedRunnerServiceServer
	clientset    kubernetes.Interface
	restConfig   *rest.Config
	namespace    string
	storageClass *string
	storageSize  string
	logger       *zap.Logger

	execSessions   map[string]*execSession
	execSessionsMu sync.Mutex
}

// New constructs a RunnerService server.
func New(clientset kubernetes.Interface, restConfig *rest.Config, namespace string, storageClass *string, storageSize string, logger *zap.Logger) *Server {
	return &Server{
		clientset:    clientset,
		restConfig:   restConfig,
		namespace:    namespace,
		storageClass: storageClass,
		storageSize:  storageSize,
		logger:       logger,
		execSessions: make(map[string]*execSession),
	}
}
