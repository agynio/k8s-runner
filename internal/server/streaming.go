package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	runnerv1 "github.com/agynio/runner-core/gen/agynio/api/runner/v1"
)

// OpenWorkloadLogs opens the Pod log stream runner-core reads onto the wire,
// with the request's tail, since and follow options applied.
func (s *Server) OpenWorkloadLogs(ctx context.Context, req *runnerv1.StreamWorkloadLogsRequest) (io.ReadCloser, error) {
	workloadID := strings.TrimSpace(req.GetWorkloadId())
	containerName := strings.TrimSpace(req.GetContainerName())
	podName := podNameFromID(workloadID)

	pod, err := s.clientset.CoreV1().Pods(s.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}
	if !podHasContainer(pod, containerName) {
		return nil, status.Error(codes.NotFound, "container_not_found")
	}

	options := &corev1.PodLogOptions{
		Container:  containerName,
		Follow:     req.GetFollow(),
		Timestamps: req.GetTimestamps(),
	}
	if req.GetTailLines() > 0 {
		tail := int64(req.GetTailLines())
		options.TailLines = &tail
	} else if req.GetTail() > 0 {
		tail := int64(req.GetTail())
		options.TailLines = &tail
	}
	if req.GetSinceTime() != nil {
		sinceTime := metav1.NewTime(req.GetSinceTime().AsTime())
		options.SinceTime = &sinceTime
	} else if req.GetSince() > 0 {
		sinceTime := metav1.NewTime(time.Unix(req.GetSince(), 0))
		options.SinceTime = &sinceTime
	}

	logStream, err := s.clientset.CoreV1().Pods(s.namespace).GetLogs(podName, options).Stream(ctx)
	if err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}
	return logStream, nil
}

// WorkloadExists is how runner-core tells a container that finished from a
// Pod deleted mid-stream.
func (s *Server) WorkloadExists(ctx context.Context, workloadID string) (bool, error) {
	podAlive, err := podExists(ctx, s.clientset, s.namespace, podNameFromID(workloadID))
	if err != nil {
		return false, grpcErrorFromKube(s.logger, err, codes.Internal)
	}
	return podAlive, nil
}

func podExists(ctx context.Context, clientset kubernetes.Interface, namespace, name string) (bool, error) {
	if _, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func podHasContainer(pod *corev1.Pod, name string) bool {
	if pod == nil {
		return false
	}
	for _, container := range pod.Spec.InitContainers {
		if container.Name == name {
			return true
		}
	}
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) StreamEvents(req *runnerv1.StreamEventsRequest, stream runnerv1.RunnerService_StreamEventsServer) error {
	ctx := stream.Context()
	since := req.GetSince()
	var sinceTime time.Time
	if since > 0 {
		sinceTime = time.Unix(since, 0).UTC()
	}

	watcher, err := s.clientset.CoreV1().Events(s.namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return grpcErrorFromKube(s.logger, err, codes.Internal)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				s.logger.Warn("event watcher closed", zap.String("namespace", s.namespace))
				return stream.Send(&runnerv1.StreamEventsResponse{
					Event: &runnerv1.StreamEventsResponse_Error{
						Error: streamError("events_watcher_closed", fmt.Errorf("event watch closed")),
					},
				})
			}
			if event.Type == watch.Error {
				return stream.Send(&runnerv1.StreamEventsResponse{
					Event: &runnerv1.StreamEventsResponse_Error{Error: streamError("events_stream_error", fmt.Errorf("event watch error"))},
				})
			}
			eventObj, ok := event.Object.(*corev1.Event)
			if !ok {
				continue
			}
			if !matchesEventFilters(eventObj, req.GetFilters()) {
				continue
			}
			eventTime := eventTimestamp(eventObj)
			if !sinceTime.IsZero() && eventTime.Before(sinceTime) {
				continue
			}
			payload, err := json.Marshal(eventObj)
			if err != nil {
				return stream.Send(&runnerv1.StreamEventsResponse{
					Event: &runnerv1.StreamEventsResponse_Error{Error: streamError("events_marshal_error", err)},
				})
			}
			resp := &runnerv1.StreamEventsResponse{
				Event: &runnerv1.StreamEventsResponse_Data{
					Data: &runnerv1.RunnerEventData{
						Json: string(payload),
						Ts:   timestamppb.New(eventTime),
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}

func streamError(code string, err error) *runnerv1.RunnerError {
	return &runnerv1.RunnerError{
		Code:      code,
		Message:   err.Error(),
		Retryable: false,
	}
}

func matchesEventFilters(event *corev1.Event, filters []*runnerv1.EventFilter) bool {
	for _, filter := range filters {
		if filter == nil || len(filter.Values) == 0 {
			continue
		}
		value := eventFieldValue(event, filter.Key)
		if value == "" {
			return false
		}
		matched := false
		for _, candidate := range filter.Values {
			if candidate == value {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func eventFieldValue(event *corev1.Event, key string) string {
	switch strings.TrimSpace(key) {
	case "involvedObject.name":
		return event.InvolvedObject.Name
	case "involvedObject.namespace":
		return event.InvolvedObject.Namespace
	case "involvedObject.kind":
		return event.InvolvedObject.Kind
	case "involvedObject.uid":
		return string(event.InvolvedObject.UID)
	case "reason":
		return event.Reason
	case "type":
		return event.Type
	case "namespace":
		return event.Namespace
	case "metadata.name":
		return event.Name
	default:
		return ""
	}
}

func eventTimestamp(event *corev1.Event) time.Time {
	if !event.EventTime.IsZero() {
		return event.EventTime.Time
	}
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time
	}
	if !event.FirstTimestamp.IsZero() {
		return event.FirstTimestamp.Time
	}
	return time.Now().UTC()
}
