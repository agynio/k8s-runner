package reporter

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	runnersv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runners/v1"
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

// Every Pod update and every resync re-delivers the same object. Without this
// the runner would resend a state the platform already has, on a timer.
func TestChangedSuppressesARepeatOfTheSameStatus(t *testing.T) {
	r := New(nil, "ns", nil, "token", nil)

	if !r.changed("workload-1", runnersv1.WorkloadStatus_WORKLOAD_STATUS_RUNNING) {
		t.Fatal("expected the first observation to be reported")
	}
	if r.changed("workload-1", runnersv1.WorkloadStatus_WORKLOAD_STATUS_RUNNING) {
		t.Fatal("expected a repeat of the same status to be suppressed")
	}
	if !r.changed("workload-1", runnersv1.WorkloadStatus_WORKLOAD_STATUS_FAILED) {
		t.Fatal("expected a different status to be reported")
	}
}

// A workload restarted under the same id must report again from scratch.
func TestForgetClearsTheRememberedStatus(t *testing.T) {
	r := New(nil, "ns", nil, "token", nil)
	r.changed("workload-1", runnersv1.WorkloadStatus_WORKLOAD_STATUS_RUNNING)

	r.forget(podWith(corev1.PodRunning, corev1.ConditionTrue))

	if !r.changed("workload-1", runnersv1.WorkloadStatus_WORKLOAD_STATUS_RUNNING) {
		t.Fatal("expected a forgotten workload to report again")
	}
}
