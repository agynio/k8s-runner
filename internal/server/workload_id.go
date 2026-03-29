package server

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const workloadNamePrefix = "workload-"

func podNameFromID(id string) string {
	return fmt.Sprintf("%s%s", workloadNamePrefix, id)
}

func workloadIDFromPod(pod *corev1.Pod) string {
	return pod.Labels[workloadIDLabelKey]
}
