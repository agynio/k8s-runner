//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func TestStreaming(t *testing.T) {
	t.Run("logs_follow", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		req := &runnerv1.StartWorkloadRequest{
			Main: &runnerv1.ContainerSpec{
				Image:      defaultWorkloadImage,
				Entrypoint: "/bin/sh",
				Cmd:        []string{"-c", "echo follow-1; echo follow-2; sleep 2"},
			},
		}
		workloadID := startWorkload(t, ctx, req)
		waitRunning(t, ctx, workloadID)

		logs := collectWorkloadLogs(t, ctx, workloadID, true, 0)
		require.Contains(t, logs, "follow-1")
		require.Contains(t, logs, "follow-2")
	})

	t.Run("logs_tail", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		req := &runnerv1.StartWorkloadRequest{
			Main: &runnerv1.ContainerSpec{
				Image:      defaultWorkloadImage,
				Entrypoint: "/bin/sh",
				Cmd:        []string{"-c", "echo line-1; echo line-2; echo line-3; echo line-4; sleep 2"},
			},
		}
		workloadID := startWorkload(t, ctx, req)
		waitRunning(t, ctx, workloadID)

		logs := collectWorkloadLogs(t, ctx, workloadID, false, 2)
		require.Contains(t, logs, "line-3")
		require.Contains(t, logs, "line-4")
		require.NotContains(t, logs, "line-1")
	})
}

func TestStreamEvents(t *testing.T) {
	ctx, cancel := testContext(t)
	t.Cleanup(cancel)

	streamCtx, streamCancel := context.WithTimeout(ctx, waitRunningTimeout)
	t.Cleanup(streamCancel)

	stream, err := runnerClient.StreamEvents(streamCtx, &runnerv1.StreamEventsRequest{})
	require.NoError(t, err)

	workloadID := startWorkload(t, ctx, sleepWorkloadRequest())
	waitRunning(t, ctx, workloadID)

	for {
		resp, err := stream.Recv()
		require.NoError(t, err)
		if data := resp.GetData(); data != nil {
			if strings.Contains(data.GetJson(), workloadID) {
				return
			}
			continue
		}
		if errResp := resp.GetError(); errResp != nil {
			t.Fatalf("events stream error: %s", errResp.GetMessage())
		}
	}
}
