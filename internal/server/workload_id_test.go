package server

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodNameFromID(t *testing.T) {
	id := "some-uuid"
	got := podNameFromID(id)
	if got != "workload-some-uuid" {
		t.Fatalf("expected pod name %q, got %q", "workload-some-uuid", got)
	}
}

func TestWorkloadIDFromPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workload-unused",
			Labels: map[string]string{
				workloadIDLabelKey: "uuid-123",
			},
		},
	}

	got := workloadIDFromPod(pod)
	if got != "uuid-123" {
		t.Fatalf("expected workload id %q, got %q", "uuid-123", got)
	}
}
