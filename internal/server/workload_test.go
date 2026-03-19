package server

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func TestBuildLabelsFiltersAndValidates(t *testing.T) {
	labels, err := buildLabels("workload-1", map[string]string{
		"label.team": "core",
		"ignored":    "value",
	})
	if err != nil {
		t.Fatalf("buildLabels returned error: %v", err)
	}

	expected := map[string]string{
		managedByLabelKey:  managedByLabelValue,
		workloadIDLabelKey: "workload-1",
		"team":             "core",
	}
	if !reflect.DeepEqual(labels, expected) {
		t.Fatalf("labels mismatch: got %#v want %#v", labels, expected)
	}
}

func TestBuildLabelsRejectsInvalidKey(t *testing.T) {
	_, err := buildLabels("workload-1", map[string]string{
		"label.bad key": "value",
	})
	if err == nil {
		t.Fatalf("expected error for invalid label key")
	}
}

func TestBuildContainersMapsMainSpec(t *testing.T) {
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{
			Name:       "main",
			Image:      "busybox",
			Entrypoint: "/bin/sh",
			Cmd:        []string{"echo", "hi"},
			WorkingDir: "/work",
		},
	}

	containers, sidecars, err := buildContainers(req, nil)
	if err != nil {
		t.Fatalf("buildContainers returned error: %v", err)
	}
	if len(sidecars) != 0 {
		t.Fatalf("expected no sidecars, got %d", len(sidecars))
	}
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}

	main := containers[0]
	if main.Name != "main" {
		t.Fatalf("expected container name 'main', got %q", main.Name)
	}
	if !reflect.DeepEqual(main.Command, []string{"/bin/sh"}) {
		t.Fatalf("expected entrypoint command, got %#v", main.Command)
	}
	if !reflect.DeepEqual(main.Args, []string{"echo", "hi"}) {
		t.Fatalf("expected args to match, got %#v", main.Args)
	}
	if main.WorkingDir != "/work" {
		t.Fatalf("expected working dir to be /work, got %q", main.WorkingDir)
	}
}

func TestBuildContainersRejectsDuplicateNames(t *testing.T) {
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		Sidecars: []*runnerv1.ContainerSpec{
			{Name: "main", Image: "busybox"},
		},
	}

	_, _, err := buildContainers(req, nil)
	if err == nil {
		t.Fatalf("expected duplicate container error")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("expected invalid argument error, got %v", err)
	}
	if !strings.Contains(st.Message(), "duplicate_container_name") {
		t.Fatalf("expected duplicate container error message, got %q", st.Message())
	}
}

func TestBuildContainersRejectsEntrypointWithSpaces(t *testing.T) {
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox", Entrypoint: "/bin/sh -c"},
	}

	_, _, err := buildContainers(req, nil)
	if err == nil {
		t.Fatalf("expected entrypoint validation error")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("expected invalid argument error, got %v", err)
	}
	if !strings.Contains(st.Message(), "entrypoint_must_be_single_path") {
		t.Fatalf("expected entrypoint error message, got %q", st.Message())
	}
}

func TestParsePVCAnnotation(t *testing.T) {
	if got := parsePVCAnnotation(nil); got != nil {
		t.Fatalf("expected nil for nil annotations, got %#v", got)
	}

	got := parsePVCAnnotation(map[string]string{pvcAnnotationKey: " pvc-a , , pvc-b "})
	expected := []string{"pvc-a", "pvc-b"}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected pvc list %#v, got %#v", expected, got)
	}
}
