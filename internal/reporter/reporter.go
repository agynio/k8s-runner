// Package reporter watches the Pods this runner manages and hands every
// transition to runner-core's reporter, which tells the platform.
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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/agynio/runner-core/backend"
	runnersv1 "github.com/agynio/runner-core/gen/agynio/api/runners/v1"
)

// WorkloadIDLabel is set by this runner when it creates the Pod, so a Pod maps
// to a workload record with no lookup.
const WorkloadIDLabel = "agyn.io/workload-id"

const resyncInterval = 30 * time.Second

// Watch follows the workload Pods in namespace until ctx ends, handing what
// it sees to observer.
//
// An informer rather than a bare watch for its two recovery properties: it
// reconnects when the API server drops the watch, and it re-lists on resync, so
// a transition missed during a disconnect is re-derived rather than lost.
func Watch(ctx context.Context, clientset kubernetes.Interface, namespace string, observer backend.Observer) error {
	factory := informers.NewSharedInformerFactoryWithOptions(
		clientset,
		resyncInterval,
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = WorkloadIDLabel
		}),
	)
	pods := factory.Core().V1().Pods().Informer()
	if _, err := pods.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { observe(ctx, obj, observer) },
		UpdateFunc: func(_, obj any) { observe(ctx, obj, observer) },
		DeleteFunc: func(obj any) { forget(obj, observer) },
	}); err != nil {
		return err
	}
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())
	<-ctx.Done()
	return ctx.Err()
}

// observe hands a Pod's runtime state to the observer. Anything that is
// neither running nor failed is a transition in progress and says nothing
// worth sending.
func observe(ctx context.Context, obj any, observer backend.Observer) {
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
	observer.Observe(ctx, backend.Observation{WorkloadID: workloadID, Status: observed})
}

// forget drops a deleted Pod's last reported status so a workload restarted
// under the same id reports again from scratch.
//
// Deliberately reports nothing itself. A deleted Pod means the workload is gone,
// and ending a workload is the platform's decision -- the Orchestrator's
// reconciliation notices the absence and settles what it means.
func forget(obj any, observer backend.Observer) {
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
	observer.Forget(workloadID)
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
