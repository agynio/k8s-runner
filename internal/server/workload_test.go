package server

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

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

	containers, initContainers, sidecars, err := buildContainers(req, nil)
	if err != nil {
		t.Fatalf("buildContainers returned error: %v", err)
	}
	if len(sidecars) != 0 {
		t.Fatalf("expected no sidecars, got %d", len(sidecars))
	}
	if len(initContainers) != 0 {
		t.Fatalf("expected no init containers, got %d", len(initContainers))
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

func TestBuildContainerMapsRequiredCapabilities(t *testing.T) {
	container, err := buildContainer(&runnerv1.ContainerSpec{
		Name:                 "main",
		Image:                "busybox",
		RequiredCapabilities: []string{"NET_ADMIN"},
	}, "main", map[string]struct{}{})
	if err != nil {
		t.Fatalf("buildContainer returned error: %v", err)
	}
	if container.SecurityContext == nil || container.SecurityContext.Capabilities == nil {
		t.Fatalf("expected security context capabilities")
	}
	expected := []corev1.Capability{"NET_ADMIN"}
	if !reflect.DeepEqual(container.SecurityContext.Capabilities.Add, expected) {
		t.Fatalf("expected capabilities %#v, got %#v", expected, container.SecurityContext.Capabilities.Add)
	}
}

func TestBuildContainerOmitsSecurityContextWithoutRequiredCapabilities(t *testing.T) {
	container, err := buildContainer(&runnerv1.ContainerSpec{
		Name:  "main",
		Image: "busybox",
	}, "main", map[string]struct{}{})
	if err != nil {
		t.Fatalf("buildContainer returned error: %v", err)
	}
	if container.SecurityContext != nil {
		t.Fatalf("expected no security context when capabilities are nil")
	}

	container, err = buildContainer(&runnerv1.ContainerSpec{
		Name:                 "main",
		Image:                "busybox",
		RequiredCapabilities: []string{},
	}, "main", map[string]struct{}{})
	if err != nil {
		t.Fatalf("buildContainer returned error: %v", err)
	}
	if container.SecurityContext != nil {
		t.Fatalf("expected no security context when capabilities are empty")
	}
}

func TestBuildContainerOmitsSecurityContextForWhitespaceCapabilities(t *testing.T) {
	container, err := buildContainer(&runnerv1.ContainerSpec{
		Name:                 "main",
		Image:                "busybox",
		RequiredCapabilities: []string{" ", ""},
	}, "main", map[string]struct{}{})
	if err != nil {
		t.Fatalf("buildContainer returned error: %v", err)
	}
	if container.SecurityContext != nil {
		t.Fatalf("expected no security context when capabilities are whitespace-only")
	}
}

func TestBuildContainersRejectsDuplicateNames(t *testing.T) {
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		Sidecars: []*runnerv1.ContainerSpec{
			{Name: "main", Image: "busybox"},
		},
	}

	_, _, _, err := buildContainers(req, nil)
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

	_, _, _, err := buildContainers(req, nil)
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

func TestBuildContainersMapsInitRestartPolicy(t *testing.T) {
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		InitContainers: []*runnerv1.ContainerSpec{
			{Name: "setup", Image: "alpine", AdditionalProperties: map[string]string{"restart_policy": "Always"}},
		},
	}

	_, initContainers, _, err := buildContainers(req, nil)
	if err != nil {
		t.Fatalf("buildContainers returned error: %v", err)
	}
	if len(initContainers) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(initContainers))
	}
	if initContainers[0].RestartPolicy == nil {
		t.Fatalf("expected init container restart policy to be set")
	}
	if *initContainers[0].RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Fatalf("expected restart policy Always, got %q", *initContainers[0].RestartPolicy)
	}
}

func TestBuildContainersOmitsInitRestartPolicy(t *testing.T) {
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		InitContainers: []*runnerv1.ContainerSpec{
			{Name: "setup", Image: "alpine"},
		},
	}

	_, initContainers, _, err := buildContainers(req, nil)
	if err != nil {
		t.Fatalf("buildContainers returned error: %v", err)
	}
	if len(initContainers) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(initContainers))
	}
	if initContainers[0].RestartPolicy != nil {
		t.Fatalf("expected init container restart policy to be nil")
	}
}

func TestBuildContainersRejectsInitDuplicateNameWithMain(t *testing.T) {
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		InitContainers: []*runnerv1.ContainerSpec{
			{Name: "main", Image: "alpine"},
		},
	}

	_, _, _, err := buildContainers(req, nil)
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

func TestBuildContainersRejectsInitDuplicateNames(t *testing.T) {
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		InitContainers: []*runnerv1.ContainerSpec{
			{Name: "setup", Image: "busybox"},
			{Name: "setup", Image: "alpine"},
		},
	}

	_, _, _, err := buildContainers(req, nil)
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

func TestStartWorkloadBuildsInitContainers(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		RunnerID:    "runner-123",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		InitContainers: []*runnerv1.ContainerSpec{
			{Image: "alpine"},
			{Name: "init-setup", Image: "busybox", Cmd: []string{"echo", "ready"}},
		},
	}

	resp, err := server.StartWorkload(ctx, req)
	if err != nil {
		t.Fatalf("StartWorkload returned error: %v", err)
	}
	if resp == nil || resp.Id == "" {
		t.Fatalf("expected response with id")
	}
	if resp.RunnerId != "runner-123" {
		t.Fatalf("expected runner id runner-123, got %q", resp.RunnerId)
	}

	pod, err := clientset.CoreV1().Pods("default").Get(ctx, resp.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod created: %v", err)
	}
	if len(pod.Spec.InitContainers) != 2 {
		t.Fatalf("expected 2 init containers, got %d", len(pod.Spec.InitContainers))
	}
	if pod.Spec.InitContainers[0].Name != "init-1" {
		t.Fatalf("expected fallback init container name, got %q", pod.Spec.InitContainers[0].Name)
	}
	if pod.Spec.InitContainers[0].Image != "alpine" {
		t.Fatalf("expected init container image alpine, got %q", pod.Spec.InitContainers[0].Image)
	}
	if pod.Spec.InitContainers[1].Name != "init-setup" {
		t.Fatalf("expected init container name init-setup, got %q", pod.Spec.InitContainers[1].Name)
	}
	if !reflect.DeepEqual(pod.Spec.InitContainers[1].Args, []string{"echo", "ready"}) {
		t.Fatalf("expected init container args, got %#v", pod.Spec.InitContainers[1].Args)
	}
}

func TestStartWorkloadMapsDnsConfig(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		RunnerID:    "runner-456",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		DnsConfig: &runnerv1.DnsConfig{
			Nameservers: []string{"127.0.0.1", "10.96.0.10"},
			Searches:    []string{"svc.cluster.local"},
		},
	}

	resp, err := server.StartWorkload(ctx, req)
	if err != nil {
		t.Fatalf("StartWorkload returned error: %v", err)
	}
	if resp == nil || resp.Id == "" {
		t.Fatalf("expected response with id")
	}
	if resp.RunnerId != "runner-456" {
		t.Fatalf("expected runner id runner-456, got %q", resp.RunnerId)
	}

	pod, err := clientset.CoreV1().Pods("default").Get(ctx, resp.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod created: %v", err)
	}
	if pod.Spec.DNSPolicy != corev1.DNSNone {
		t.Fatalf("expected DNSPolicy None, got %q", pod.Spec.DNSPolicy)
	}
	if pod.Spec.DNSConfig == nil {
		t.Fatalf("expected DNSConfig to be set")
	}
	if !reflect.DeepEqual(pod.Spec.DNSConfig.Nameservers, req.DnsConfig.Nameservers) {
		t.Fatalf("expected nameservers %#v, got %#v", req.DnsConfig.Nameservers, pod.Spec.DNSConfig.Nameservers)
	}
	if !reflect.DeepEqual(pod.Spec.DNSConfig.Searches, req.DnsConfig.Searches) {
		t.Fatalf("expected searches %#v, got %#v", req.DnsConfig.Searches, pod.Spec.DNSConfig.Searches)
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
