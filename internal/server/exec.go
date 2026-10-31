package server

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/agynio/runner-core/backend"
)

// PrepareExec resolves the target Pod's main container and readies the
// Kubernetes exec for it. runner-core owns the session around it: the stream
// protocol, timeouts, cancellation and the exit event.
func (s *Server) PrepareExec(ctx context.Context, targetID string, command []string, tty bool) (backend.Execution, error) {
	containerName, err := s.resolveExecTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}

	execOptions := buildPodExecOptions(containerName, command, tty)
	reqURL := s.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(targetID).
		Namespace(s.namespace).
		SubResource("exec").
		VersionedParams(execOptions, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(s.restConfig, "POST", reqURL.URL())
	if err != nil {
		return nil, err
	}
	return &podExecution{executor: executor}, nil
}

type podExecution struct {
	executor remotecommand.Executor
}

// Run streams the command through the exec subresource. A non-zero exit comes
// back as client-go's CodeExitError, which exposes the ExitStatus runner-core
// reads.
func (e *podExecution) Run(ctx context.Context, streams backend.ExecStreams) error {
	return e.executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             streams.Stdin,
		Stdout:            streams.Stdout,
		Stderr:            streams.Stderr,
		Tty:               streams.TTY,
		TerminalSizeQueue: &terminalSizeQueue{ch: streams.Resize},
	})
}

type terminalSizeQueue struct {
	ch <-chan backend.TerminalSize
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	if q == nil {
		return nil
	}
	value, ok := <-q.ch
	if !ok {
		return nil
	}
	return &remotecommand.TerminalSize{Width: value.Width, Height: value.Height}
}

func (s *Server) resolveExecTarget(ctx context.Context, targetID string) (string, error) {
	pod, err := s.clientset.CoreV1().Pods(s.namespace).Get(ctx, targetID, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	containerName, err := mainContainerName(pod)
	if err != nil {
		return "", err
	}
	return containerName, nil
}

func buildPodExecOptions(containerName string, command []string, useTTY bool) *corev1.PodExecOptions {
	return &corev1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdin:     true,
		Stdout:    true,
		Stderr:    !useTTY,
		TTY:       useTTY,
	}
}
