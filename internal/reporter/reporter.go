// Package reporter tells the platform what the runner sees.
//
// The platform used to ask. The Agents Orchestrator dialed the runner on its
// reconcile interval and called InspectWorkload, so a workload was ready and
// serving for up to a full interval before anything recorded it -- and the
// sandbox behind it stayed "starting" for exactly that long. The runner is the
// only component that can see a Pod, so it is the only one that can say when.
package reporter

import (
	"context"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	gatewayv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/gateway/v1"
	runnersv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runners/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WorkloadIDLabel is set by this runner when it creates the Pod, so a Pod maps
// to a workload record with no lookup.
const WorkloadIDLabel = "agyn.io/workload-id"

const (
	resyncInterval = 30 * time.Second
	reportTimeout  = 10 * time.Second
	retryBackoff   = time.Second
	retryAttempts  = 3
)

type Reporter struct {
	clientset    kubernetes.Interface
	namespace    string
	gateway      gatewayv1.RunnersGatewayClient
	serviceToken string
	logger       *zap.Logger

	// reported is the last status sent per workload, so a resync or an
	// unrelated Pod update does not resend what the platform already has.
	mu       sync.Mutex
	reported map[string]runnersv1.WorkloadStatus
}

func New(clientset kubernetes.Interface, namespace string, gateway gatewayv1.RunnersGatewayClient, serviceToken string, logger *zap.Logger) *Reporter {
	return &Reporter{
		clientset:    clientset,
		namespace:    namespace,
		gateway:      gateway,
		serviceToken: serviceToken,
		logger:       logger,
		reported:     map[string]runnersv1.WorkloadStatus{},
	}
}

// Run watches the workload Pods in this runner's namespace until ctx ends.
//
// An informer rather than a bare watch for its two recovery properties: it
// reconnects when the API server drops the watch, and it re-lists on resync, so
// a transition missed during a disconnect is re-derived rather than lost.
func (r *Reporter) Run(ctx context.Context) error {
	factory := informers.NewSharedInformerFactoryWithOptions(
		r.clientset,
		resyncInterval,
		informers.WithNamespace(r.namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = WorkloadIDLabel
		}),
	)
	pods := factory.Core().V1().Pods().Informer()
	if _, err := pods.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { r.observe(ctx, obj) },
		UpdateFunc: func(_, obj any) { r.observe(ctx, obj) },
		DeleteFunc: r.forget,
	}); err != nil {
		return err
	}
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())
	<-ctx.Done()
	return ctx.Err()
}

// observe reports a Pod's runtime state when it is one the platform does not
// already have. Anything that is neither running nor failed is a transition in
// progress and says nothing worth sending.
func (r *Reporter) observe(ctx context.Context, obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	workloadID := strings.TrimSpace(pod.Labels[WorkloadIDLabel])
	if workloadID == "" {
		return
	}
	observed, ok := workloadStatus(pod)
	if !ok {
		return
	}
	if !r.changed(workloadID, observed) {
		return
	}
	r.report(ctx, workloadID, observed)
}

// forget drops a deleted Pod's last reported status so a workload restarted
// under the same id reports again from scratch.
//
// Deliberately reports nothing itself. A deleted Pod means the workload is gone,
// and ending a workload is the platform's decision -- the Orchestrator's
// reconciliation notices the absence and settles what it means.
func (r *Reporter) forget(obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		if tombstone, isTombstone := obj.(cache.DeletedFinalStateUnknown); isTombstone {
			pod, ok = tombstone.Obj.(*corev1.Pod)
		}
		if !ok {
			return
		}
	}
	workloadID := strings.TrimSpace(pod.Labels[WorkloadIDLabel])
	if workloadID == "" {
		return
	}
	r.mu.Lock()
	delete(r.reported, workloadID)
	r.mu.Unlock()
}

func (r *Reporter) changed(workloadID string, observed runnersv1.WorkloadStatus) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.reported[workloadID]; ok && last == observed {
		return false
	}
	r.reported[workloadID] = observed
	return true
}

// report is best-effort. A failure is retried a few times and then dropped: the
// Orchestrator's reconciliation is the backstop, so a runner that cannot report
// is behind on latency, not on correctness.
func (r *Reporter) report(ctx context.Context, workloadID string, observed runnersv1.WorkloadStatus) {
	observedAt := timestamppb.Now()
	for attempt := 0; attempt < retryAttempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, reportTimeout)
		_, err := r.gateway.ReportWorkloadState(callCtx, &runnersv1.ReportWorkloadStateRequest{
			ServiceToken: r.serviceToken,
			WorkloadId:   workloadID,
			Status:       observed,
			ObservedAt:   observedAt,
		})
		cancel()
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		// A platform older than this RPC answers Unimplemented. It reconciles on
		// its own interval exactly as it always did, so the runner keeps serving
		// and simply stops trying to help.
		if status.Code(err) == codes.Unimplemented {
			r.logger.Info("platform does not accept workload state reports; leaving it to reconciliation")
			return
		}
		if attempt == retryAttempts-1 {
			r.logger.Warn("report workload state",
				zap.String("workload_id", workloadID),
				zap.String("status", observed.String()),
				zap.Error(err))
			// Forgotten so the next observation retries rather than being
			// suppressed as already reported.
			r.mu.Lock()
			delete(r.reported, workloadID)
			r.mu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryBackoff << attempt):
		}
	}
}

// workloadStatus reads the runtime state a Pod is in.
//
// Running means every container is ready, not merely that the Pod exists: the
// platform marks a sandbox usable on this signal, and a Pod whose containers are
// still starting is not one a shell can attach to.
func workloadStatus(pod *corev1.Pod) (runnersv1.WorkloadStatus, bool) {
	switch pod.Status.Phase {
	case corev1.PodFailed:
		return runnersv1.WorkloadStatus_WORKLOAD_STATUS_FAILED, true
	case corev1.PodRunning:
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return runnersv1.WorkloadStatus_WORKLOAD_STATUS_RUNNING, true
			}
		}
	}
	return runnersv1.WorkloadStatus_WORKLOAD_STATUS_UNSPECIFIED, false
}
