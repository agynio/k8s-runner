package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func TestBuildLabelsFiltersAndValidates(t *testing.T) {
	labels, err := buildLabels("uuid-1", map[string]string{
		"label.team": "core",
		"ignored":    "value",
	}, map[string]string{workloadKeyLabelKey: "workload-1"})
	if err != nil {
		t.Fatalf("buildLabels returned error: %v", err)
	}

	expected := map[string]string{
		managedByLabelKey:   managedByLabelValue,
		workloadIDLabelKey:  "uuid-1",
		workloadKeyLabelKey: "workload-1",
		"team":              "core",
	}
	if !reflect.DeepEqual(labels, expected) {
		t.Fatalf("labels mismatch: got %#v want %#v", labels, expected)
	}
}

func TestBuildLabelsRejectsInvalidKey(t *testing.T) {
	_, err := buildLabels("uuid-1", map[string]string{
		"label.bad key": "value",
	}, nil)
	if err == nil {
		t.Fatalf("expected error for invalid label key")
	}
}

func TestBuildLabelsRejectsInvalidExplicitLabel(t *testing.T) {
	_, err := buildLabels("uuid-1", nil, map[string]string{"bad key": "value"})
	if err == nil {
		t.Fatalf("expected error for invalid explicit label key")
	}
}

func TestBuildLabelsRejectsReservedLabel(t *testing.T) {
	_, err := buildLabels("uuid-1", nil, map[string]string{managedByLabelKey: "override"})
	if err == nil {
		t.Fatalf("expected error for reserved label key")
	}
}

func TestBuildDockerConfigJSON(t *testing.T) {
	payload, err := buildDockerConfigJSON("registry.example.com", "user", "pass")
	if err != nil {
		t.Fatalf("buildDockerConfigJSON returned error: %v", err)
	}

	var config dockerConfig
	if err := json.Unmarshal(payload, &config); err != nil {
		t.Fatalf("failed to unmarshal docker config: %v", err)
	}

	auth, ok := config.Auths["registry.example.com"]
	if !ok {
		t.Fatalf("expected registry auth entry")
	}
	if auth.Username != "user" {
		t.Fatalf("expected username 'user', got %q", auth.Username)
	}
	if auth.Password != "pass" {
		t.Fatalf("expected password 'pass', got %q", auth.Password)
	}
	expectedAuth := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if auth.Auth != expectedAuth {
		t.Fatalf("expected auth %q, got %q", expectedAuth, auth.Auth)
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

func TestStartWorkloadUsesProvidedWorkloadID(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	workloadID := "2db3f1b6-7d0b-4c28-8ed4-5eb9803ccf12"
	req := &runnerv1.StartWorkloadRequest{
		WorkloadId: workloadID,
		Main:       &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
	}

	resp, err := server.StartWorkload(ctx, req)
	if err != nil {
		t.Fatalf("StartWorkload returned error: %v", err)
	}
	if resp.GetId() != workloadID {
		t.Fatalf("expected workload id %q, got %q", workloadID, resp.GetId())
	}

	podName := podNameFromID(workloadID)
	pod, err := clientset.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod created: %v", err)
	}
	if pod.Labels[workloadIDLabelKey] != workloadID {
		t.Fatalf("expected workload label %q, got %q", workloadID, pod.Labels[workloadIDLabelKey])
	}
}

func TestStartWorkloadRejectsInvalidWorkloadID(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	req := &runnerv1.StartWorkloadRequest{
		WorkloadId: "not-a-uuid",
		Main:       &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
	}

	_, err := server.StartWorkload(ctx, req)
	if err == nil {
		t.Fatalf("expected error for invalid workload id")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("expected invalid argument error, got %v", err)
	}
	if !strings.Contains(st.Message(), "workload_id_invalid") {
		t.Fatalf("expected workload id invalid error, got %q", st.Message())
	}
}

func TestStartWorkloadBuildsInitContainers(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
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

	podName := podNameFromID(resp.Id)
	pod, err := clientset.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
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

	podName := podNameFromID(resp.Id)
	pod, err := clientset.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
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

func TestStartWorkloadCreatesImagePullSecrets(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	credentials := []*runnerv1.ImagePullCredential{
		{Registry: "registry.example.com", Username: "user", Password: "pass"},
		{Registry: "registry.internal", Username: "robot", Password: "token"},
	}
	req := &runnerv1.StartWorkloadRequest{
		Main:                 &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		ImagePullCredentials: credentials,
	}

	resp, err := server.StartWorkload(ctx, req)
	if err != nil {
		t.Fatalf("StartWorkload returned error: %v", err)
	}
	if resp == nil || resp.Id == "" {
		t.Fatalf("expected response with id")
	}

	podName := podNameFromID(resp.Id)
	pod, err := clientset.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod created: %v", err)
	}

	expectedNames := []string{
		fmt.Sprintf("workload-%s-pull-0", resp.Id),
		fmt.Sprintf("workload-%s-pull-1", resp.Id),
	}
	if len(pod.Spec.ImagePullSecrets) != len(expectedNames) {
		t.Fatalf("expected %d image pull secrets, got %d", len(expectedNames), len(pod.Spec.ImagePullSecrets))
	}
	gotNames := make([]string, 0, len(pod.Spec.ImagePullSecrets))
	for _, ref := range pod.Spec.ImagePullSecrets {
		gotNames = append(gotNames, ref.Name)
	}
	if !reflect.DeepEqual(gotNames, expectedNames) {
		t.Fatalf("expected image pull secret names %#v, got %#v", expectedNames, gotNames)
	}
	if pod.Annotations[secretAnnotationKey] != strings.Join(expectedNames, ",") {
		t.Fatalf("expected secret annotation %q, got %q", strings.Join(expectedNames, ","), pod.Annotations[secretAnnotationKey])
	}

	for idx, secretName := range expectedNames {
		secret, err := clientset.CoreV1().Secrets("default").Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("expected secret %s: %v", secretName, err)
		}
		if secret.Type != corev1.SecretTypeDockerConfigJson {
			t.Fatalf("expected dockerconfigjson secret type, got %q", secret.Type)
		}
		if secret.Labels[managedByLabelKey] != managedByLabelValue {
			t.Fatalf("expected managed-by label %q, got %q", managedByLabelValue, secret.Labels[managedByLabelKey])
		}
		if secret.Labels[workloadIDLabelKey] != resp.Id {
			t.Fatalf("expected workload id label %q, got %q", resp.Id, secret.Labels[workloadIDLabelKey])
		}
		expectedConfig, err := buildDockerConfigJSON(credentials[idx].Registry, credentials[idx].Username, credentials[idx].Password)
		if err != nil {
			t.Fatalf("buildDockerConfigJSON returned error: %v", err)
		}
		if !reflect.DeepEqual(secret.Data[corev1.DockerConfigJsonKey], expectedConfig) {
			t.Fatalf("expected docker config data to match")
		}
	}
}

func TestStartWorkloadAppliesLabelsToPodAndPVC(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		Labels: map[string]string{
			workloadKeyLabelKey: "workload-key-1",
			"team":              "core",
		},
		Volumes: []*runnerv1.VolumeSpec{
			{
				Name:           "data",
				Kind:           runnerv1.VolumeKind_VOLUME_KIND_NAMED,
				PersistentName: "pvc-data",
				Labels: map[string]string{
					volumeKeyLabelKey: "volume-key-1",
				},
			},
		},
	}

	resp, err := server.StartWorkload(ctx, req)
	if err != nil {
		t.Fatalf("StartWorkload returned error: %v", err)
	}
	if resp == nil || resp.Id == "" {
		t.Fatalf("expected response with id")
	}

	podName := podNameFromID(resp.Id)
	pod, err := clientset.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod created: %v", err)
	}
	if pod.Labels[managedByLabelKey] != managedByLabelValue {
		t.Fatalf("expected managed-by label %q, got %q", managedByLabelValue, pod.Labels[managedByLabelKey])
	}
	if pod.Labels[workloadIDLabelKey] != resp.Id {
		t.Fatalf("expected workload id label %q, got %q", resp.Id, pod.Labels[workloadIDLabelKey])
	}
	if pod.Labels[workloadKeyLabelKey] != "workload-key-1" {
		t.Fatalf("expected workload key label %q, got %q", "workload-key-1", pod.Labels[workloadKeyLabelKey])
	}
	if pod.Labels["team"] != "core" {
		t.Fatalf("expected team label %q, got %q", "core", pod.Labels["team"])
	}

	pvc, err := clientset.CoreV1().PersistentVolumeClaims("default").Get(ctx, "pvc-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pvc created: %v", err)
	}
	if pvc.Labels[managedByLabelKey] != managedByLabelValue {
		t.Fatalf("expected pvc managed-by label %q, got %q", managedByLabelValue, pvc.Labels[managedByLabelKey])
	}
	if pvc.Labels[volumeKeyLabelKey] != "volume-key-1" {
		t.Fatalf("expected volume key label %q, got %q", "volume-key-1", pvc.Labels[volumeKeyLabelKey])
	}
}

func TestStartWorkloadRejectsReservedVolumeLabel(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		Volumes: []*runnerv1.VolumeSpec{
			{
				Name:           "data",
				Kind:           runnerv1.VolumeKind_VOLUME_KIND_NAMED,
				PersistentName: "pvc-data",
				Labels: map[string]string{
					managedByLabelKey: "override",
				},
			},
		},
	}

	_, err := server.StartWorkload(ctx, req)
	if err == nil {
		t.Fatalf("expected error for reserved volume label")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("expected invalid argument error, got %v", err)
	}
	if !strings.Contains(st.Message(), "invalid_volume_label") {
		t.Fatalf("expected invalid volume label error, got %q", st.Message())
	}
}

func TestStartWorkloadNoCredentials(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
	}

	resp, err := server.StartWorkload(ctx, req)
	if err != nil {
		t.Fatalf("StartWorkload returned error: %v", err)
	}
	if resp == nil || resp.Id == "" {
		t.Fatalf("expected response with id")
	}

	podName := podNameFromID(resp.Id)
	pod, err := clientset.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pod created: %v", err)
	}
	if len(pod.Spec.ImagePullSecrets) != 0 {
		t.Fatalf("expected no image pull secrets, got %d", len(pod.Spec.ImagePullSecrets))
	}
	if _, ok := pod.Annotations[secretAnnotationKey]; ok {
		t.Fatalf("expected no secret annotation")
	}

	secrets, err := clientset.CoreV1().Secrets("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("expected secrets list: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Fatalf("expected no secrets, got %d", len(secrets.Items))
	}
}

func TestStartWorkloadRejectsIncompleteCredential(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	req := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		ImagePullCredentials: []*runnerv1.ImagePullCredential{
			{Username: "user", Password: "pass"},
		},
	}

	_, err := server.StartWorkload(ctx, req)
	if err == nil {
		t.Fatalf("expected error for incomplete credential")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("expected invalid argument error, got %v", err)
	}
}

func TestStopWorkloadDeletesPullSecrets(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	startReq := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		ImagePullCredentials: []*runnerv1.ImagePullCredential{
			{Registry: "registry.example.com", Username: "user", Password: "pass"},
		},
	}

	resp, err := server.StartWorkload(ctx, startReq)
	if err != nil {
		t.Fatalf("StartWorkload returned error: %v", err)
	}

	_, err = server.StopWorkload(ctx, &runnerv1.StopWorkloadRequest{WorkloadId: resp.Id})
	if err != nil {
		t.Fatalf("StopWorkload returned error: %v", err)
	}

	secretName := fmt.Sprintf("workload-%s-pull-0", resp.Id)
	_, err = clientset.CoreV1().Secrets("default").Get(ctx, secretName, metav1.GetOptions{})
	if err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("expected secret %s to be deleted", secretName)
	}
}

func TestRemoveWorkloadDeletesPullSecrets(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	server := New(Options{
		Clientset:   clientset,
		Namespace:   "default",
		StorageSize: "1Gi",
		Logger:      zap.NewNop(),
	})

	ctx := context.Background()
	startReq := &runnerv1.StartWorkloadRequest{
		Main: &runnerv1.ContainerSpec{Name: "main", Image: "busybox"},
		ImagePullCredentials: []*runnerv1.ImagePullCredential{
			{Registry: "registry.example.com", Username: "user", Password: "pass"},
		},
	}

	resp, err := server.StartWorkload(ctx, startReq)
	if err != nil {
		t.Fatalf("StartWorkload returned error: %v", err)
	}

	_, err = server.RemoveWorkload(ctx, &runnerv1.RemoveWorkloadRequest{WorkloadId: resp.Id})
	if err != nil {
		t.Fatalf("RemoveWorkload returned error: %v", err)
	}

	secretName := fmt.Sprintf("workload-%s-pull-0", resp.Id)
	_, err = clientset.CoreV1().Secrets("default").Get(ctx, secretName, metav1.GetOptions{})
	if err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("expected secret %s to be deleted", secretName)
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

func TestParseSecretAnnotation(t *testing.T) {
	if got := parseSecretAnnotation(nil); got != nil {
		t.Fatalf("expected nil for nil annotations, got %#v", got)
	}

	got := parseSecretAnnotation(map[string]string{secretAnnotationKey: " secret-a , , secret-b "})
	expected := []string{"secret-a", "secret-b"}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected secret list %#v, got %#v", expected, got)
	}
}
