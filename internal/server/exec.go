package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

const (
	defaultExitTailBytes = 64 * 1024
	maxExitTailBytes     = 256 * 1024
)

type execSession struct {
	id     string
	cancel context.CancelFunc

	mu     sync.Mutex
	reason runnerv1.ExecExitReason
	killed bool

	timerMu      sync.Mutex
	idleTimeout  time.Duration
	idleTimer    *time.Timer
	timeoutTimer *time.Timer
}

func newExecSession(id string, cancel context.CancelFunc) *execSession {
	return &execSession{
		id:     id,
		cancel: cancel,
		reason: runnerv1.ExecExitReason_EXEC_EXIT_REASON_COMPLETED,
	}
}

func (s *execSession) terminate(reason runnerv1.ExecExitReason, killed bool) {
	s.mu.Lock()
	if s.reason == runnerv1.ExecExitReason_EXEC_EXIT_REASON_COMPLETED {
		s.reason = reason
		s.killed = killed
	}
	s.mu.Unlock()
	s.cancel()
}

func (s *execSession) termination() (runnerv1.ExecExitReason, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason, s.killed
}

func (s *execSession) startTimers(timeout, idle time.Duration, killOnTimeout bool) {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	if timeout > 0 {
		s.timeoutTimer = time.AfterFunc(timeout, func() {
			s.terminate(runnerv1.ExecExitReason_EXEC_EXIT_REASON_TIMEOUT, killOnTimeout)
		})
	}
	if idle > 0 {
		s.idleTimeout = idle
		s.idleTimer = time.AfterFunc(idle, func() {
			s.terminate(runnerv1.ExecExitReason_EXEC_EXIT_REASON_IDLE_TIMEOUT, killOnTimeout)
		})
	}
}

func (s *execSession) resetIdleTimer() {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	if s.idleTimer == nil || s.idleTimeout <= 0 {
		return
	}
	if !s.idleTimer.Stop() {
		return
	}
	s.idleTimer.Reset(s.idleTimeout)
}

func (s *execSession) stopTimers() {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	if s.timeoutTimer != nil {
		s.timeoutTimer.Stop()
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
}

type terminalSizeQueue struct {
	ch <-chan remotecommand.TerminalSize
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	if q == nil {
		return nil
	}
	value, ok := <-q.ch
	if !ok {
		return nil
	}
	return &value
}

type execOutputWriter struct {
	stream    runnerv1.RunnerService_ExecServer
	sendMu    *sync.Mutex
	seq       *uint64
	output    outputKind
	idleReset func()
	tail      *tailBuffer
}

type outputKind int

const (
	outputStdout outputKind = iota
	outputStderr
)

func (w *execOutputWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	data := append([]byte(nil), p...)
	if w.tail != nil {
		w.tail.Write(data)
	}
	seq := atomic.AddUint64(w.seq, 1)
	output := &runnerv1.ExecOutput{
		Seq:  seq,
		Data: data,
		Ts:   timestamppb.New(time.Now().UTC()),
	}
	var resp *runnerv1.ExecResponse
	if w.output == outputStdout {
		resp = &runnerv1.ExecResponse{Event: &runnerv1.ExecResponse_Stdout{Stdout: output}}
	} else {
		resp = &runnerv1.ExecResponse{Event: &runnerv1.ExecResponse_Stderr{Stderr: output}}
	}

	w.sendMu.Lock()
	err := w.stream.Send(resp)
	w.sendMu.Unlock()
	if err != nil {
		return 0, err
	}
	if w.idleReset != nil {
		w.idleReset()
	}
	return len(p), nil
}

type tailBuffer struct {
	limit int
	data  []byte
}

func newTailBuffer(limit int) *tailBuffer {
	if limit <= 0 {
		return &tailBuffer{limit: 0}
	}
	return &tailBuffer{limit: limit}
}

func (b *tailBuffer) Write(p []byte) {
	if b == nil || b.limit <= 0 || len(p) == 0 {
		return
	}
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return
	}
	if len(b.data)+len(p) > b.limit {
		trim := len(b.data) + len(p) - b.limit
		b.data = b.data[trim:]
	}
	b.data = append(b.data, p...)
}

func (b *tailBuffer) Bytes() []byte {
	if b == nil || len(b.data) == 0 {
		return nil
	}
	return append([]byte(nil), b.data...)
}

func (s *Server) Exec(stream runnerv1.RunnerService_ExecServer) error {
	first, err := stream.Recv()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return sendExecError(stream, nil, "exec_start_required", "exec start required", false)
	}
	if strings.TrimSpace(start.TargetId) == "" {
		return sendExecError(stream, nil, "target_id_required", "target_id required", false)
	}

	command, err := buildExecCommand(start)
	if err != nil {
		return sendExecError(stream, nil, "invalid_command", err.Error(), false)
	}

	pod, err := s.clientset.CoreV1().Pods(s.namespace).Get(stream.Context(), start.TargetId, metav1.GetOptions{})
	if err != nil {
		return sendExecError(stream, nil, "exec_start_failed", err.Error(), false)
	}
	containerName, err := mainContainerName(pod)
	if err != nil {
		return err
	}

	execID := uuid.NewString()
	execCtx, cancel := context.WithCancel(stream.Context())
	session := newExecSession(execID, cancel)

	s.execSessionsMu.Lock()
	s.execSessions[execID] = session
	s.execSessionsMu.Unlock()
	defer func() {
		s.execSessionsMu.Lock()
		delete(s.execSessions, execID)
		s.execSessionsMu.Unlock()
	}()

	options := start.GetOptions()
	separateStderr := true
	if options != nil {
		separateStderr = options.SeparateStderr
	}
	useTTY := options != nil && options.Tty

	stdinReader, stdinWriter := io.Pipe()
	defer stdinWriter.Close()

	stdoutTail := newTailBuffer(resolveExitTailBytes(options))
	stderrTail := newTailBuffer(resolveExitTailBytes(options))

	sendMu := &sync.Mutex{}
	stdoutSeq := uint64(0)
	stderrSeq := uint64(0)

	stdoutWriter := &execOutputWriter{
		stream:    stream,
		sendMu:    sendMu,
		seq:       &stdoutSeq,
		output:    outputStdout,
		idleReset: session.resetIdleTimer,
		tail:      stdoutTail,
	}
	var stderrWriter io.Writer
	if useTTY {
		stderrWriter = nil
	} else if separateStderr {
		stderrWriter = &execOutputWriter{
			stream:    stream,
			sendMu:    sendMu,
			seq:       &stderrSeq,
			output:    outputStderr,
			idleReset: session.resetIdleTimer,
			tail:      stderrTail,
		}
	} else {
		stderrWriter = stdoutWriter
	}

	resizeCh := make(chan remotecommand.TerminalSize, 1)
	queue := &terminalSizeQueue{ch: resizeCh}

	execOptions := &corev1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdin:     true,
		Stdout:    true,
		Stderr:    !useTTY,
		TTY:       useTTY,
	}

	reqURL := s.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(start.TargetId).
		Namespace(s.namespace).
		SubResource("exec").
		VersionedParams(execOptions, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(s.restConfig, "POST", reqURL.URL())
	if err != nil {
		return sendExecError(stream, sendMu, "exec_start_failed", err.Error(), false)
	}

	if err := sendExecStarted(stream, sendMu, execID); err != nil {
		return err
	}

	if options != nil {
		session.startTimers(timeoutDuration(options.TimeoutMs), timeoutDuration(options.IdleTimeoutMs), options.KillOnTimeout)
	}

	execErrCh := make(chan error, 1)
	go func() {
		execErrCh <- executor.StreamWithContext(execCtx, remotecommand.StreamOptions{
			Stdin:             stdinReader,
			Stdout:            stdoutWriter,
			Stderr:            stderrWriter,
			Tty:               useTTY,
			TerminalSizeQueue: queue,
		})
	}()

	reqCh := make(chan *runnerv1.ExecRequest, 4)
	reqErrCh := make(chan error, 1)
	go recvExecRequests(stream.Context(), stream, reqCh, reqErrCh)

	for {
		select {
		case req := <-reqCh:
			if req == nil {
				reqCh = nil
				continue
			}
			session.resetIdleTimer()
			if stdin := req.GetStdin(); stdin != nil {
				if len(stdin.Data) > 0 {
					if _, err := stdinWriter.Write(stdin.Data); err != nil {
						session.terminate(runnerv1.ExecExitReason_EXEC_EXIT_REASON_RUNNER_ERROR, false)
					}
				}
				if stdin.Eof {
					_ = stdinWriter.Close()
				}
				continue
			}
			if resize := req.GetResize(); resize != nil && useTTY {
				if resize.Cols > 0 && resize.Rows > 0 {
					pushTerminalSize(resizeCh, remotecommand.TerminalSize{Width: uint16(resize.Cols), Height: uint16(resize.Rows)})
				}
				continue
			}
			if req.GetStart() != nil {
				_ = sendExecError(stream, sendMu, "exec_already_started", "duplicate exec start received", false)
			}
		case recvErr := <-reqErrCh:
			if recvErr == io.EOF {
				session.terminate(runnerv1.ExecExitReason_EXEC_EXIT_REASON_CANCELLED, false)
			} else {
				session.terminate(runnerv1.ExecExitReason_EXEC_EXIT_REASON_RUNNER_ERROR, false)
			}
			reqErrCh = nil
		case execErr := <-execErrCh:
			close(resizeCh)
			session.stopTimers()
			return finishExec(stream, sendMu, session, execErr, stdoutTail, stderrTail)
		case <-stream.Context().Done():
			session.terminate(runnerv1.ExecExitReason_EXEC_EXIT_REASON_CANCELLED, false)
		}
	}
}

func (s *Server) CancelExecution(ctx context.Context, req *runnerv1.CancelExecutionRequest) (*runnerv1.CancelExecutionResponse, error) {
	execID := strings.TrimSpace(req.GetExecutionId())
	if execID == "" {
		return nil, status.Error(codes.InvalidArgument, "execution_id_required")
	}

	s.execSessionsMu.Lock()
	session := s.execSessions[execID]
	s.execSessionsMu.Unlock()

	if session == nil {
		return &runnerv1.CancelExecutionResponse{Cancelled: false}, nil
	}

	session.terminate(runnerv1.ExecExitReason_EXEC_EXIT_REASON_CANCELLED, req.GetForce())
	return &runnerv1.CancelExecutionResponse{Cancelled: true}, nil
}

func recvExecRequests(ctx context.Context, stream runnerv1.RunnerService_ExecServer, reqCh chan<- *runnerv1.ExecRequest, errCh chan<- error) {
	defer close(reqCh)
	for {
		req, err := stream.Recv()
		if err != nil {
			errCh <- err
			return
		}
		select {
		case reqCh <- req:
		case <-ctx.Done():
			errCh <- ctx.Err()
			return
		}
	}
}

func buildExecCommand(start *runnerv1.ExecStartRequest) ([]string, error) {
	if start == nil {
		return nil, errors.New("exec start required")
	}
	argv := start.CommandArgv
	shell := strings.TrimSpace(start.CommandShell)
	if len(argv) == 0 && shell == "" {
		return nil, errors.New("command required")
	}
	if len(argv) > 0 && shell != "" {
		return nil, errors.New("command_argv and command_shell are mutually exclusive")
	}
	var command []string
	if len(argv) > 0 {
		command = append([]string{}, argv...)
	} else {
		command = []string{"/bin/sh", "-c", shell}
	}
	if start.Options != nil && start.Options.Workdir != "" {
		command = append([]string{"/bin/sh", "-c", "cd \"$1\" && shift && exec \"$@\"", "--", start.Options.Workdir}, command...)
	}
	if start.Options != nil && len(start.Options.Env) > 0 {
		envArgs := []string{"env"}
		for _, env := range start.Options.Env {
			if env == nil {
				continue
			}
			name := strings.TrimSpace(env.Name)
			if name == "" {
				return nil, errors.New("env name required")
			}
			if errs := validation.IsEnvVarName(name); len(errs) > 0 {
				return nil, fmt.Errorf("invalid env name: %s", strings.Join(errs, ", "))
			}
			envArgs = append(envArgs, fmt.Sprintf("%s=%s", name, env.Value))
		}
		command = append(envArgs, command...)
	}
	return command, nil
}

func resolveExitTailBytes(options *runnerv1.ExecOptions) int {
	limit := defaultExitTailBytes
	if options != nil && options.ExitTailBytes > 0 {
		limit = int(options.ExitTailBytes)
	}
	if limit > maxExitTailBytes {
		limit = maxExitTailBytes
	}
	return limit
}

func timeoutDuration(value uint64) time.Duration {
	if value == 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}

func pushTerminalSize(ch chan remotecommand.TerminalSize, size remotecommand.TerminalSize) {
	select {
	case ch <- size:
	default:
		select {
		case <-ch:
		default:
		}
		ch <- size
	}
}

func sendExecStarted(stream runnerv1.RunnerService_ExecServer, mu *sync.Mutex, execID string) error {
	resp := &runnerv1.ExecResponse{
		Event: &runnerv1.ExecResponse_Started{
			Started: &runnerv1.ExecStarted{
				ExecutionId: execID,
				StartedAt:   timestamppb.New(time.Now().UTC()),
			},
		},
	}
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	return stream.Send(resp)
}

func sendExecError(stream runnerv1.RunnerService_ExecServer, mu *sync.Mutex, code, message string, retryable bool) error {
	resp := &runnerv1.ExecResponse{
		Event: &runnerv1.ExecResponse_Error{
			Error: &runnerv1.ExecError{
				Code:      code,
				Message:   message,
				Retryable: retryable,
			},
		},
	}
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	return stream.Send(resp)
}

func finishExec(stream runnerv1.RunnerService_ExecServer, mu *sync.Mutex, session *execSession, execErr error, stdoutTail, stderrTail *tailBuffer) error {
	reason, killed := session.termination()
	if execErr != nil {
		if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
			if reason == runnerv1.ExecExitReason_EXEC_EXIT_REASON_COMPLETED {
				reason = runnerv1.ExecExitReason_EXEC_EXIT_REASON_CANCELLED
			}
		} else if !isExitError(execErr) && reason == runnerv1.ExecExitReason_EXEC_EXIT_REASON_COMPLETED {
			reason = runnerv1.ExecExitReason_EXEC_EXIT_REASON_RUNNER_ERROR
		}
	}

	exitCode := int32(0)
	if execErr != nil {
		if exitErr, ok := exitStatus(execErr); ok {
			exitCode = int32(exitErr)
		} else {
			exitCode = exitCodeForReason(reason)
		}
	}
	if execErr == nil && reason != runnerv1.ExecExitReason_EXEC_EXIT_REASON_COMPLETED {
		exitCode = exitCodeForReason(reason)
	}

	resp := &runnerv1.ExecResponse{
		Event: &runnerv1.ExecResponse_Exit{
			Exit: &runnerv1.ExecExit{
				ExecutionId: execID(session),
				ExitCode:    exitCode,
				Killed:      killed,
				Reason:      reason,
				StdoutTail:  stdoutTail.Bytes(),
				StderrTail:  stderrTail.Bytes(),
				FinishedAt:  timestamppb.New(time.Now().UTC()),
			},
		},
	}
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	return stream.Send(resp)
}

func execID(session *execSession) string {
	if session == nil {
		return ""
	}
	return session.id
}

func exitCodeForReason(reason runnerv1.ExecExitReason) int32 {
	switch reason {
	case runnerv1.ExecExitReason_EXEC_EXIT_REASON_CANCELLED:
		return 0
	case runnerv1.ExecExitReason_EXEC_EXIT_REASON_TIMEOUT,
		runnerv1.ExecExitReason_EXEC_EXIT_REASON_IDLE_TIMEOUT,
		runnerv1.ExecExitReason_EXEC_EXIT_REASON_RUNNER_ERROR:
		return -1
	default:
		return 0
	}
}

func isExitError(err error) bool {
	_, ok := exitStatus(err)
	return ok
}

func exitStatus(err error) (int, bool) {
	var exitErr utilexec.CodeExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitStatus(), true
	}
	return 0, false
}
