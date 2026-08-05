package server

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func storageServer(t *testing.T, objects ...runtime.Object) (*Server, *fake.Clientset) {
	t.Helper()
	clientset := fake.NewSimpleClientset(objects...)
	return New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	}), clientset
}

func TestRemoveVolumeDeletesTheClaim(t *testing.T) {
	server, clientset := storageServer(t, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "vol-1", Namespace: "default"},
	})

	if _, err := server.RemoveVolume(context.Background(), &runnerv1.RemoveVolumeRequest{VolumeName: "vol-1"}); err != nil {
		t.Fatalf("RemoveVolume: %v", err)
	}
	_, err := clientset.CoreV1().PersistentVolumeClaims("default").Get(context.Background(), "vol-1", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("claim still present: %v", err)
	}
}

// The caller is a reconciler that retries until the volume is gone. Reporting an
// absent claim as an error leaves it retrying forever on work already done — a
// sandbox sat in failed for exactly this reason.
func TestRemoveVolumeIsIdempotent(t *testing.T) {
	server, _ := storageServer(t)

	if _, err := server.RemoveVolume(context.Background(), &runnerv1.RemoveVolumeRequest{VolumeName: "never-existed"}); err != nil {
		t.Fatalf("RemoveVolume on an absent claim: %v", err)
	}
}

// A claim Kubernetes is already tearing down is on its way out; saying so as an
// error restarts the same loop.
func TestRemoveVolumeAcceptsATerminatingClaim(t *testing.T) {
	deleting := metav1.Now()
	server, _ := storageServer(t, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "vol-2",
			Namespace:         "default",
			DeletionTimestamp: &deleting,
			Finalizers:        []string{"kubernetes.io/pvc-protection"},
		},
	})

	if _, err := server.RemoveVolume(context.Background(), &runnerv1.RemoveVolumeRequest{VolumeName: "vol-2"}); err != nil {
		t.Fatalf("RemoveVolume on a terminating claim: %v", err)
	}
}

func TestRemoveVolumeRequiresAName(t *testing.T) {
	server, _ := storageServer(t)

	_, err := server.RemoveVolume(context.Background(), &runnerv1.RemoveVolumeRequest{VolumeName: "  "})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
}
