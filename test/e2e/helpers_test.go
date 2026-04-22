//go:build e2e

package e2e

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

const (
	defaultWorkloadImage = "alpine:3.19"
	perTestTimeout       = 120 * time.Second
	waitRunningTimeout   = 60 * time.Second
	waitGoneTimeout      = 30 * time.Second
	cleanupTimeout       = 30 * time.Second
	pollInterval         = 2 * time.Second
)

type execResult struct {
	stdout  string
	stderr  string
	exit    *runnerv1.ExecExit
	started *runnerv1.ExecStarted
}

func testContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), perTestTimeout)
}

func startWorkload(t *testing.T, ctx context.Context, req *runnerv1.StartWorkloadRequest) string {
	t.Helper()
	resp := startWorkloadWithCleanup(t, ctx, req)
	return resp.GetId()
}

func startWorkloadWithCleanup(t *testing.T, ctx context.Context, req *runnerv1.StartWorkloadRequest) *runnerv1.StartWorkloadResponse {
	t.Helper()
	resp, err := runnerClient.StartWorkload(ctx, req)
	require.NoError(t, err)

	workloadID := strings.TrimSpace(resp.GetId())
	require.NotEmpty(t, workloadID)
	registerWorkloadCleanup(t, workloadID)

	return resp
}

func registerWorkloadCleanup(t *testing.T, workloadID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_, err := runnerClient.RemoveWorkload(ctx, &runnerv1.RemoveWorkloadRequest{
			WorkloadId:    workloadID,
			Force:         true,
			RemoveVolumes: true,
		})
		if err == nil {
			return
		}
		if status.Code(err) == codes.NotFound {
			return
		}
		t.Errorf("cleanup remove workload %s: %v", workloadID, err)
	})
}

func waitRunning(t *testing.T, ctx context.Context, workloadID string) *runnerv1.InspectWorkloadResponse {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, waitRunningTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		resp, err := runnerClient.InspectWorkload(waitCtx, &runnerv1.InspectWorkloadRequest{WorkloadId: workloadID})
		if err == nil {
			if resp.GetStateRunning() {
				return resp
			}
		} else if status.Code(err) != codes.NotFound {
			require.NoError(t, err)
		}

		select {
		case <-waitCtx.Done():
			t.Fatalf("workload %s not running: %v", workloadID, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func waitGone(t *testing.T, ctx context.Context, workloadID string) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, waitGoneTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		_, err := runnerClient.InspectWorkload(waitCtx, &runnerv1.InspectWorkloadRequest{WorkloadId: workloadID})
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return
			}
			require.NoError(t, err)
		}

		select {
		case <-waitCtx.Done():
			t.Fatalf("workload %s still present: %v", workloadID, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func buildTarWithFile(name, content string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(content)),
	}); err != nil {
		panic(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		panic(err)
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func collectExecOutput(t *testing.T, ctx context.Context, start *runnerv1.ExecStartRequest, stdin ...*runnerv1.ExecStdin) execResult {
	t.Helper()
	stream, err := runnerClient.Exec(ctx)
	require.NoError(t, err)

	err = stream.Send(&runnerv1.ExecRequest{Msg: &runnerv1.ExecRequest_Start{Start: start}})
	require.NoError(t, err)

	for _, input := range stdin {
		if input == nil {
			continue
		}
		err = stream.Send(&runnerv1.ExecRequest{Msg: &runnerv1.ExecRequest_Stdin{Stdin: input}})
		require.NoError(t, err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var started *runnerv1.ExecStarted

	for {
		resp, err := stream.Recv()
		require.NoError(t, err)

		switch event := resp.GetEvent().(type) {
		case *runnerv1.ExecResponse_Started:
			started = event.Started
		case *runnerv1.ExecResponse_Stdout:
			stdout.Write(event.Stdout.GetData())
		case *runnerv1.ExecResponse_Stderr:
			stderr.Write(event.Stderr.GetData())
		case *runnerv1.ExecResponse_Exit:
			return execResult{
				stdout:  stdout.String(),
				stderr:  stderr.String(),
				exit:    event.Exit,
				started: started,
			}
		case *runnerv1.ExecResponse_Error:
			t.Fatalf("exec error: %s", event.Error.GetMessage())
		default:
			t.Fatalf("unexpected exec response: %T", event)
		}
	}
}

func collectWorkloadLogs(t *testing.T, ctx context.Context, workloadID string, follow bool, tail uint32) string {
	t.Helper()
	stream, err := runnerClient.StreamWorkloadLogs(ctx, &runnerv1.StreamWorkloadLogsRequest{
		WorkloadId:    workloadID,
		ContainerName: "main",
		Follow:        follow,
		TailLines:     tail,
	})
	require.NoError(t, err)

	var output bytes.Buffer
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return output.String()
		}
		require.NoError(t, err)

		if chunk := resp.GetChunk(); chunk != nil {
			output.Write(chunk.GetData())
			continue
		}
		if resp.GetEnd() != nil {
			return output.String()
		}
		if errResp := resp.GetError(); errResp != nil {
			t.Fatalf("log stream error: %s", errResp.GetMessage())
		}
	}
}

func requireGRPCCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, code, st.Code())
}

func sleepWorkloadRequest(cmd ...string) *runnerv1.StartWorkloadRequest {
	args := cmd
	if len(args) == 0 {
		args = []string{"sleep", "300"}
	}
	return &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{
			Image: defaultWorkloadImage,
			Cmd:   append([]string{}, args...),
		},
	}
}

func uniqueName(prefix string) string {
	base := strings.Trim(prefix, "- ")
	if base == "" {
		base = "e2e"
	}
	return strings.ToLower(fmt.Sprintf("%s-%s", base, uuid.NewString()))
}

func podNameFromID(id string) string {
	return fmt.Sprintf("workload-%s", id)
}
