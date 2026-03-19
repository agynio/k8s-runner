//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func TestPutArchive(t *testing.T) {
	ctx, cancel := testContext(t)
	t.Cleanup(cancel)

	workloadID := startWorkload(t, ctx, &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{
			Image:      defaultWorkloadImage,
			Entrypoint: "/bin/sh",
			Cmd:        []string{"-c", "mkdir -p /tmp/e2e && sleep 300"},
		},
	})
	waitRunning(t, ctx, workloadID)

	tarPayload := buildTarWithFile("hello.txt", "hello-storage")
	_, err := runnerClient.PutArchive(ctx, &runnerv1.PutArchiveRequest{
		WorkloadId: workloadID,
		Path:       "/tmp/e2e",
		TarPayload: tarPayload,
	})
	require.NoError(t, err)

	result := collectExecOutput(t, ctx, &runnerv1.ExecStartRequest{
		TargetId:    workloadID,
		CommandArgv: []string{"cat", "/tmp/e2e/hello.txt"},
	})
	require.NotNil(t, result.exit)
	require.Equal(t, int32(0), result.exit.GetExitCode())
	require.Contains(t, result.stdout, "hello-storage")
}
