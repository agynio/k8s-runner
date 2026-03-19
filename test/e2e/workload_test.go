//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func TestReady(t *testing.T) {
	ctx, cancel := testContext(t)
	t.Cleanup(cancel)

	resp, err := runnerClient.Ready(ctx, &runnerv1.ReadyRequest{})
	require.NoError(t, err)
	require.Equal(t, "ok", resp.GetStatus())
}

func TestWorkloadLifecycle(t *testing.T) {
	t.Run("start_and_inspect", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		workloadID := startWorkload(t, ctx, sleepWorkloadRequest())
		resp := waitRunning(t, ctx, workloadID)

		require.Equal(t, workloadID, resp.GetId())
		require.Equal(t, workloadID, resp.GetName())
		require.Equal(t, defaultWorkloadImage, resp.GetConfigImage())
		require.NotEmpty(t, resp.GetImage())
		require.True(t, resp.GetStateRunning())
		require.NotEmpty(t, resp.GetStateStatus())
		require.Equal(t, "k8s-runner", resp.GetConfigLabels()["app.kubernetes.io/managed-by"])
		require.Equal(t, workloadID, resp.GetConfigLabels()["agyn.io/workload-id"])
		require.Empty(t, resp.GetMounts())
	})

	t.Run("start_with_env_and_workdir", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		req := &runnerv1.StartWorkloadRequest{
			Main: &runnerv1.ContainerSpec{
				Image:      defaultWorkloadImage,
				Entrypoint: "/bin/sh",
				Cmd:        []string{"-c", "echo \"$FOO\"; pwd; sleep 5"},
				Env: []*runnerv1.EnvVar{{
					Name:  "FOO",
					Value: "hello-e2e",
				}},
				WorkingDir: "/tmp",
			},
		}

		workloadID := startWorkload(t, ctx, req)
		waitRunning(t, ctx, workloadID)

		logs := collectWorkloadLogs(t, ctx, workloadID, false, 0)
		require.Contains(t, logs, "hello-e2e")
		require.Contains(t, logs, "/tmp")
	})

	t.Run("start_with_sidecars", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		req := sleepWorkloadRequest()
		req.Sidecars = []*runnerv1.ContainerSpec{{
			Name:  "sidecar",
			Image: defaultWorkloadImage,
			Cmd:   []string{"sleep", "300"},
		}}
		resp := startWorkloadWithCleanup(t, ctx, req)
		waitRunning(t, ctx, resp.GetId())

		sidecars := resp.GetContainers().GetSidecars()
		require.Len(t, sidecars, 1)
		require.Equal(t, "sidecar", sidecars[0].GetName())
		require.NotEmpty(t, sidecars[0].GetId())
	})

	t.Run("start_with_custom_labels", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		req := sleepWorkloadRequest()
		req.AdditionalProperties = map[string]string{
			"label.team": "platform",
		}
		workloadID := startWorkload(t, ctx, req)
		waitRunning(t, ctx, workloadID)

		labelsResp, err := runnerClient.GetWorkloadLabels(ctx, &runnerv1.GetWorkloadLabelsRequest{WorkloadId: workloadID})
		require.NoError(t, err)
		require.Equal(t, "platform", labelsResp.GetLabels()["team"])

		findResp, err := runnerClient.FindWorkloadsByLabels(ctx, &runnerv1.FindWorkloadsByLabelsRequest{
			Labels: map[string]string{"team": "platform"},
			All:    true,
		})
		require.NoError(t, err)
		require.Contains(t, findResp.GetTargetIds(), workloadID)
	})

	t.Run("touch_workload", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		workloadID := startWorkload(t, ctx, sleepWorkloadRequest())
		waitRunning(t, ctx, workloadID)

		_, err := runnerClient.TouchWorkload(ctx, &runnerv1.TouchWorkloadRequest{WorkloadId: workloadID})
		require.NoError(t, err)
	})

	t.Run("stop_workload", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		workloadID := startWorkload(t, ctx, sleepWorkloadRequest())
		waitRunning(t, ctx, workloadID)

		_, err := runnerClient.StopWorkload(ctx, &runnerv1.StopWorkloadRequest{
			WorkloadId: workloadID,
			TimeoutSec: 1,
		})
		require.NoError(t, err)
		waitGone(t, ctx, workloadID)
	})

	t.Run("remove_workload_with_volumes", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		volumeName := uniqueName("volume")
		req := sleepWorkloadRequest()
		req.Volumes = []*runnerv1.VolumeSpec{{
			Name:           "data",
			Kind:           runnerv1.VolumeKind_VOLUME_KIND_NAMED,
			PersistentName: volumeName,
		}}
		req.Main.Mounts = []*runnerv1.VolumeMount{{
			Volume:    "data",
			MountPath: "/data",
		}}

		workloadID := startWorkload(t, ctx, req)
		waitRunning(t, ctx, workloadID)

		_, err := runnerClient.RemoveWorkload(ctx, &runnerv1.RemoveWorkloadRequest{
			WorkloadId:    workloadID,
			Force:         true,
			RemoveVolumes: true,
		})
		require.NoError(t, err)
		waitGone(t, ctx, workloadID)

		_, err = runnerClient.RemoveVolume(ctx, &runnerv1.RemoveVolumeRequest{VolumeName: volumeName})
		requireGRPCCode(t, err, codes.NotFound)
	})
}
