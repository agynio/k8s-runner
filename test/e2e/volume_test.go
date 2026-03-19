//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func TestVolumeQueries(t *testing.T) {
	t.Run("list_workloads_by_volume", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		volumeName := uniqueName("volume")
		workloadID := startWorkload(t, ctx, volumeWorkloadRequest(volumeName))
		waitRunning(t, ctx, workloadID)

		resp, err := runnerClient.ListWorkloadsByVolume(ctx, &runnerv1.ListWorkloadsByVolumeRequest{VolumeName: volumeName})
		require.NoError(t, err)
		require.Contains(t, resp.GetTargetIds(), workloadID)
	})

	t.Run("remove_volume", func(t *testing.T) {
		ctx, cancel := testContext(t)
		t.Cleanup(cancel)

		volumeName := uniqueName("volume")
		workloadID := startWorkload(t, ctx, volumeWorkloadRequest(volumeName))
		waitRunning(t, ctx, workloadID)

		_, err := runnerClient.StopWorkload(ctx, &runnerv1.StopWorkloadRequest{WorkloadId: workloadID, TimeoutSec: 1})
		require.NoError(t, err)
		waitGone(t, ctx, workloadID)

		_, err = runnerClient.RemoveVolume(ctx, &runnerv1.RemoveVolumeRequest{VolumeName: volumeName})
		require.NoError(t, err)
		_, err = runnerClient.RemoveVolume(ctx, &runnerv1.RemoveVolumeRequest{VolumeName: volumeName})
		requireGRPCCode(t, err, codes.NotFound)
	})
}

func volumeWorkloadRequest(volumeName string) *runnerv1.StartWorkloadRequest {
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
	return req
}
