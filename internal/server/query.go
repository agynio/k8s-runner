package server

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func (s *Server) GetWorkloadLabels(ctx context.Context, req *runnerv1.GetWorkloadLabelsRequest) (*runnerv1.GetWorkloadLabelsResponse, error) {
	workloadID := strings.TrimSpace(req.GetWorkloadId())
	if workloadID == "" {
		return nil, status.Error(codes.InvalidArgument, "workload_id_required")
	}

	pod, err := s.clientset.CoreV1().Pods(s.namespace).Get(ctx, workloadID, metav1.GetOptions{})
	if err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	return &runnerv1.GetWorkloadLabelsResponse{Labels: pod.Labels}, nil
}

func (s *Server) FindWorkloadsByLabels(ctx context.Context, req *runnerv1.FindWorkloadsByLabelsRequest) (*runnerv1.FindWorkloadsByLabelsResponse, error) {
	selector := labels.Set(req.GetLabels()).AsSelector().String()
	list, err := s.clientset.CoreV1().Pods(s.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	ids := make([]string, 0, len(list.Items))
	for _, pod := range list.Items {
		if !req.GetAll() && pod.Status.Phase != corev1.PodRunning {
			continue
		}
		ids = append(ids, pod.Name)
	}

	return &runnerv1.FindWorkloadsByLabelsResponse{TargetIds: ids}, nil
}

func (s *Server) ListWorkloadsByVolume(ctx context.Context, req *runnerv1.ListWorkloadsByVolumeRequest) (*runnerv1.ListWorkloadsByVolumeResponse, error) {
	volumeName := strings.TrimSpace(req.GetVolumeName())
	if volumeName == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_name_required")
	}

	selector := labels.Set(map[string]string{managedByLabelKey: managedByLabelValue}).AsSelector().String()
	list, err := s.clientset.CoreV1().Pods(s.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	ids := make([]string, 0)
	for _, pod := range list.Items {
		if podUsesPVC(&pod, volumeName) {
			ids = append(ids, pod.Name)
		}
	}

	return &runnerv1.ListWorkloadsByVolumeResponse{TargetIds: ids}, nil
}

func podUsesPVC(pod *corev1.Pod, pvcName string) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}
		if volume.PersistentVolumeClaim.ClaimName == pvcName {
			return true
		}
	}
	return false
}
