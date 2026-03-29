package server

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const workloadNamePrefix = "workload-"

func podNameFromID(id string) string {
	return fmt.Sprintf("%s%s", workloadNamePrefix, id)
}

func workloadIDFromPodName(podName string) string {
	return strings.TrimPrefix(podName, workloadNamePrefix)
}

func workloadIDFromPod(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	labelValue := strings.TrimSpace(pod.Labels[workloadIDLabelKey])
	if labelValue != "" {
		return strings.TrimPrefix(labelValue, workloadNamePrefix)
	}
	return workloadIDFromPodName(pod.Name)
}
