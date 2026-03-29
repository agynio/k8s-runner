//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func TestExec(t *testing.T) {
	t.Run("basic_command", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		targetID := startRunningWorkloadTarget(t, ctx)
		result := collectExecOutput(t, ctx, &runnerv1.ExecStartRequest{
			TargetId:    targetID,
			CommandArgv: []string{"echo", "hello-e2e"},
		})

		require.NotNil(t, result.exit)
		require.Equal(t, int32(0), result.exit.GetExitCode())
		require.Contains(t, result.stdout, "hello-e2e")
	})

	t.Run("shell_command", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		targetID := startRunningWorkloadTarget(t, ctx)
		result := collectExecOutput(t, ctx, &runnerv1.ExecStartRequest{
			TargetId:     targetID,
			CommandShell: "echo out; echo err 1>&2",
			Options:      &runnerv1.ExecOptions{SeparateStderr: true},
			RequestId:    uniqueName("exec"),
		})

		require.NotNil(t, result.exit)
		require.Contains(t, result.stdout, "out")
		require.Contains(t, result.stderr, "err")
	})

	t.Run("nonzero_exit_code", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		targetID := startRunningWorkloadTarget(t, ctx)
		result := collectExecOutput(t, ctx, &runnerv1.ExecStartRequest{
			TargetId:     targetID,
			CommandShell: "exit 42",
		})

		require.NotNil(t, result.exit)
		require.Equal(t, int32(42), result.exit.GetExitCode())
	})

	t.Run("stdin_and_eof", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		targetID := startRunningWorkloadTarget(t, ctx)
		payload := "stdin-e2e\n"
		result := collectExecOutput(
			t,
			ctx,
			&runnerv1.ExecStartRequest{
				TargetId:    targetID,
				CommandArgv: []string{"cat"},
			},
			&runnerv1.ExecStdin{Data: []byte(payload)},
			&runnerv1.ExecStdin{Eof: true},
		)

		require.NotNil(t, result.exit)
		require.Equal(t, payload, result.stdout)
	})

	t.Run("cancel_execution", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		targetID := startRunningWorkloadTarget(t, ctx)
		stream, err := runnerClient.Exec(ctx)
		require.NoError(t, err)

		err = stream.Send(&runnerv1.ExecRequest{Msg: &runnerv1.ExecRequest_Start{Start: &runnerv1.ExecStartRequest{
			TargetId:    targetID,
			CommandArgv: []string{"sleep", "300"},
		}}})
		require.NoError(t, err)

		var execID string
		for {
			resp, err := stream.Recv()
			require.NoError(t, err)
			if started := resp.GetStarted(); started != nil {
				execID = started.GetExecutionId()
				break
			}
			if errResp := resp.GetError(); errResp != nil {
				t.Fatalf("exec error: %s", errResp.GetMessage())
			}
		}

		require.NotEmpty(t, execID)
		cancelResp, err := runnerClient.CancelExecution(ctx, &runnerv1.CancelExecutionRequest{
			ExecutionId: execID,
			Force:       true,
		})
		require.NoError(t, err)
		require.True(t, cancelResp.GetCancelled())

		var exit *runnerv1.ExecExit
		for {
			resp, err := stream.Recv()
			require.NoError(t, err)
			if exitResp := resp.GetExit(); exitResp != nil {
				exit = exitResp
				break
			}
			if errResp := resp.GetError(); errResp != nil {
				t.Fatalf("exec error: %s", errResp.GetMessage())
			}
		}

		require.NotNil(t, exit)
		require.Equal(t, runnerv1.ExecExitReason_EXEC_EXIT_REASON_CANCELLED, exit.GetReason())
	})

	t.Run("workdir_and_env", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		targetID := startRunningWorkloadTarget(t, ctx)
		result := collectExecOutput(t, ctx, &runnerv1.ExecStartRequest{
			TargetId:     targetID,
			CommandShell: "pwd; echo $FOO",
			Options: &runnerv1.ExecOptions{
				Workdir: "/tmp",
				Env: []*runnerv1.EnvVar{{
					Name:  "FOO",
					Value: "bar",
				}},
			},
		})

		require.NotNil(t, result.exit)
		require.Contains(t, result.stdout, "/tmp")
		require.Contains(t, result.stdout, "bar")
	})
}

func startRunningWorkloadTarget(t *testing.T, ctx context.Context) string {
	t.Helper()
	resp := startWorkloadWithCleanup(t, ctx, sleepWorkloadRequest())
	workloadID := resp.GetId()
	waitRunning(t, ctx, workloadID)
	targetID := resp.GetContainers().GetMain()
	require.NotEmpty(t, targetID)
	return targetID
}
