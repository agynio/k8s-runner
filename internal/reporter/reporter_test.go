package reporter

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/agynio/runner-core/backend"
	runnersv1 "github.com/agynio/runner-core/gen/agynio/api/runners/v1"
)

func podWith(phase corev1.PodPhase, ready corev1.ConditionStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{WorkloadIDLabel: "workload-1"}},
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: ready}},
		},
	}
}

// The platform marks a sandbox usable on this signal, so a Pod whose containers
// are still starting is not one to call running -- a shell cannot attach to it.
func TestWorkloadStatusRequiresReadyNotMerelyRunning(t *testing.T) {
	observed, ok := workloadStatus(podWith(corev1.PodRunning, corev1.ConditionTrue))
	if !ok || observed != runnersv1.WorkloadStatus_WORKLOAD_STATUS_RUNNING {
		t.Fatalf("expected a ready pod to report running, got %v (ok=%v)", observed, ok)
	}

	if _, ok := workloadStatus(podWith(corev1.PodRunning, corev1.ConditionFalse)); ok {
		t.Fatal("expected a running-but-unready pod to report nothing")
	}
}

func TestWorkloadStatusReportsFailure(t *testing.T) {
	observed, ok := workloadStatus(podWith(corev1.PodFailed, corev1.ConditionFalse))
	if !ok || observed != runnersv1.WorkloadStatus_WORKLOAD_STATUS_FAILED {
		t.Fatalf("expected a failed pod to report failed, got %v (ok=%v)", observed, ok)
	}
}

// Pending and succeeded are transitions in progress or an ending the platform
// owns; neither is something a runner reports.
func TestWorkloadStatusStaysSilentOnEverythingElse(t *testing.T) {
	for _, phase := range []corev1.PodPhase{corev1.PodPending, corev1.PodSucceeded, corev1.PodUnknown} {
		if _, ok := workloadStatus(podWith(phase, corev1.ConditionFalse)); ok {
			t.Errorf("expected phase %s to report nothing", phase)
		}
	}
}

type recordingObserver struct {
	observed  []backend.Observation
	forgotten []string
}

func (o *recordingObserver) Observe(_ context.Context, observation backend.Observation) {
	o.observed = append(o.observed, observation)
}

func (o *recordingObserver) Forget(workloadID string) {
	o.forgotten = append(o.forgotten, workloadID)
}

// What the informer delivers reaches runner-core's reporter keyed by the
// workload id on the Pod, and only once the Pod says something worth sending.
func TestObserveHandsReadyPodsToTheObserver(t *testing.T) {
	observer := &recordingObserver{}

	observe(context.Background(), podWith(corev1.PodPending, corev1.ConditionFalse), observer)
	observe(context.Background(), podWith(corev1.PodRunning, corev1.ConditionTrue), observer)
	observe(context.Background(), &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}, observer)

	if len(observer.observed) != 1 {
		t.Fatalf("expected one observation, got %v", observer.observed)
	}
	if got := observer.observed[0]; got.WorkloadID != "workload-1" || got.Status != runnersv1.WorkloadStatus_WORKLOAD_STATUS_RUNNING {
		t.Fatalf("unexpected observation %+v", got)
	}
}

// A deletion the informer only learned about on relist arrives as a tombstone
// and must still clear the workload.
func TestForgetUnwrapsTombstones(t *testing.T) {
	observer := &recordingObserver{}

	forget(podWith(corev1.PodRunning, corev1.ConditionTrue), observer)
	forget(cache.DeletedFinalStateUnknown{Key: "ns/pod", Obj: podWith(corev1.PodFailed, corev1.ConditionFalse)}, observer)
	forget("not a pod", observer)

	if len(observer.forgotten) != 2 || observer.forgotten[0] != "workload-1" || observer.forgotten[1] != "workload-1" {
		t.Fatalf("expected the workload forgotten twice, got %v", observer.forgotten)
	}
}
