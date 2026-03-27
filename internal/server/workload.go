package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func (s *Server) StartWorkload(ctx context.Context, req *runnerv1.StartWorkloadRequest) (*runnerv1.StartWorkloadResponse, error) {
	if req == nil || req.Main == nil {
		return nil, status.Error(codes.InvalidArgument, "main_container_required")
	}
	if strings.TrimSpace(req.Main.Image) == "" {
		return nil, status.Error(codes.InvalidArgument, "main_container_image_required")
	}

	workloadID := fmt.Sprintf("workload-%s", uuid.NewString())

	labels, err := buildLabels(workloadID, req.AdditionalProperties)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid_label: %v", err)
	}

	volumes, pvcNames, err := s.buildVolumes(ctx, req.Volumes, labels)
	if err != nil {
		return nil, err
	}

	containers, initContainers, sidecarNames, err := buildContainers(req, volumes)
	if err != nil {
		return nil, err
	}

	annotations := map[string]string{}
	if len(pvcNames) > 0 {
		annotations[pvcAnnotationKey] = strings.Join(pvcNames, ",")
	}

	// TODO(k8s-runner#19): Map req.DnsConfig to pod.Spec.DNSPolicy + pod.Spec.DNSConfig
	// once agynio/api#70 (DnsConfig proto field) is published to BSR.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        workloadID,
			Namespace:   s.namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:  corev1.RestartPolicyNever,
			InitContainers: initContainers,
			Containers:     containers,
			Volumes:        volumes,
		},
	}

	if _, err := s.clientset.CoreV1().Pods(s.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	sidecars := make([]*runnerv1.SidecarInstance, 0, len(sidecarNames))
	for _, name := range sidecarNames {
		sidecars = append(sidecars, &runnerv1.SidecarInstance{
			Name:   name,
			Id:     fmt.Sprintf("%s:%s", workloadID, name),
			Status: "starting",
		})
	}

	return &runnerv1.StartWorkloadResponse{
		Id: workloadID,
		Containers: &runnerv1.WorkloadContainers{
			Main:     workloadID,
			Sidecars: sidecars,
		},
		Status: runnerv1.WorkloadStatus_WORKLOAD_STATUS_STARTING,
	}, nil
}

func (s *Server) StopWorkload(ctx context.Context, req *runnerv1.StopWorkloadRequest) (*runnerv1.StopWorkloadResponse, error) {
	workloadID := strings.TrimSpace(req.GetWorkloadId())
	if workloadID == "" {
		return nil, status.Error(codes.InvalidArgument, "workload_id_required")
	}

	deleteOptions := metav1.DeleteOptions{}
	if grace := int64(req.GetTimeoutSec()); grace > 0 {
		deleteOptions.GracePeriodSeconds = &grace
	}
	if err := s.clientset.CoreV1().Pods(s.namespace).Delete(ctx, workloadID, deleteOptions); err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	return &runnerv1.StopWorkloadResponse{}, nil
}

func (s *Server) RemoveWorkload(ctx context.Context, req *runnerv1.RemoveWorkloadRequest) (*runnerv1.RemoveWorkloadResponse, error) {
	workloadID := strings.TrimSpace(req.GetWorkloadId())
	if workloadID == "" {
		return nil, status.Error(codes.InvalidArgument, "workload_id_required")
	}

	pod, err := s.clientset.CoreV1().Pods(s.namespace).Get(ctx, workloadID, metav1.GetOptions{})
	if err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	var grace *int64
	if req.GetForce() {
		zero := int64(0)
		grace = &zero
	}
	deleteOptions := metav1.DeleteOptions{GracePeriodSeconds: grace}
	if err := s.clientset.CoreV1().Pods(s.namespace).Delete(ctx, workloadID, deleteOptions); err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	if req.GetRemoveVolumes() {
		pvcNames := parsePVCAnnotation(pod.Annotations)
		var deleteErrs []error
		for _, pvc := range pvcNames {
			if err := s.clientset.CoreV1().PersistentVolumeClaims(s.namespace).Delete(ctx, pvc, metav1.DeleteOptions{}); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				deleteErrs = append(deleteErrs, fmt.Errorf("delete pvc %s: %w", pvc, err))
			}
		}
		if len(deleteErrs) > 0 {
			s.logger.Error("failed to delete pvcs", zap.String("workload_id", workloadID), zap.Errors("errors", deleteErrs))
			return nil, status.Error(codes.Internal, "pvc_cleanup_failed")
		}
	}

	return &runnerv1.RemoveWorkloadResponse{}, nil
}

func (s *Server) InspectWorkload(ctx context.Context, req *runnerv1.InspectWorkloadRequest) (*runnerv1.InspectWorkloadResponse, error) {
	workloadID := strings.TrimSpace(req.GetWorkloadId())
	if workloadID == "" {
		return nil, status.Error(codes.InvalidArgument, "workload_id_required")
	}

	pod, err := s.clientset.CoreV1().Pods(s.namespace).Get(ctx, workloadID, metav1.GetOptions{})
	if err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}
	if len(pod.Spec.Containers) == 0 {
		return nil, status.Error(codes.Internal, "pod_missing_containers")
	}

	mainContainer := pod.Spec.Containers[0]
	image := mainContainer.Image
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == mainContainer.Name {
			image = status.Image
			break
		}
	}

	stateStatus := strings.ToLower(string(pod.Status.Phase))
	stateRunning := pod.Status.Phase == corev1.PodRunning

	return &runnerv1.InspectWorkloadResponse{
		Id:           pod.Name,
		Name:         pod.Name,
		Image:        image,
		ConfigImage:  mainContainer.Image,
		ConfigLabels: pod.Labels,
		Mounts:       mountsForPod(pod, mainContainer.Name),
		StateStatus:  stateStatus,
		StateRunning: stateRunning,
	}, nil
}

func (s *Server) TouchWorkload(ctx context.Context, req *runnerv1.TouchWorkloadRequest) (*runnerv1.TouchWorkloadResponse, error) {
	workloadID := strings.TrimSpace(req.GetWorkloadId())
	if workloadID == "" {
		return nil, status.Error(codes.InvalidArgument, "workload_id_required")
	}

	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	patch := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, touchedAtAnnotationKey, timestamp)
	if _, err := s.clientset.CoreV1().Pods(s.namespace).Patch(ctx, workloadID, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		return nil, grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	return &runnerv1.TouchWorkloadResponse{}, nil
}

func buildLabels(workloadID string, additional map[string]string) (map[string]string, error) {
	labels := map[string]string{
		managedByLabelKey:  managedByLabelValue,
		workloadIDLabelKey: workloadID,
	}

	for key, value := range additional {
		if !strings.HasPrefix(key, "label.") {
			continue
		}
		labelKey := strings.TrimPrefix(key, "label.")
		if labelKey == "" {
			return nil, fmt.Errorf("empty label key")
		}
		if errs := validation.IsQualifiedName(labelKey); len(errs) > 0 {
			return nil, fmt.Errorf("invalid label key %q: %s", labelKey, strings.Join(errs, ", "))
		}
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			return nil, fmt.Errorf("invalid label value for %q: %s", labelKey, strings.Join(errs, ", "))
		}
		labels[labelKey] = value
	}

	return labels, nil
}

func (s *Server) buildVolumes(ctx context.Context, volumes []*runnerv1.VolumeSpec, labels map[string]string) ([]corev1.Volume, []string, error) {
	volumeNames := make(map[string]struct{})
	createdVolumes := make([]corev1.Volume, 0, len(volumes))
	pvcNames := make([]string, 0)
	for _, volume := range volumes {
		if volume == nil {
			continue
		}
		name := strings.TrimSpace(volume.Name)
		if name == "" {
			return nil, nil, status.Error(codes.InvalidArgument, "volume_name_required")
		}
		if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
			return nil, nil, status.Errorf(codes.InvalidArgument, "invalid_volume_name: %s", strings.Join(errs, ", "))
		}
		if _, exists := volumeNames[name]; exists {
			return nil, nil, status.Errorf(codes.InvalidArgument, "duplicate_volume_name: %s", name)
		}
		volumeNames[name] = struct{}{}

		switch volume.Kind {
		case runnerv1.VolumeKind_VOLUME_KIND_EPHEMERAL:
			createdVolumes = append(createdVolumes, corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			})
		case runnerv1.VolumeKind_VOLUME_KIND_NAMED:
			pvcName, err := s.ensurePVC(ctx, volume, labels)
			if err != nil {
				return nil, nil, err
			}
			createdVolumes = append(createdVolumes, corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
					},
				},
			})
			pvcNames = append(pvcNames, pvcName)
		default:
			return nil, nil, status.Error(codes.InvalidArgument, "volume_kind_required")
		}
	}

	return createdVolumes, pvcNames, nil
}

func (s *Server) ensurePVC(ctx context.Context, volume *runnerv1.VolumeSpec, labels map[string]string) (string, error) {
	pvcName := strings.TrimSpace(volume.PersistentName)
	if pvcName == "" {
		pvcName = strings.TrimSpace(volume.Name)
	}
	if pvcName == "" {
		return "", status.Error(codes.InvalidArgument, "pvc_name_required")
	}
	if errs := validation.IsDNS1123Label(pvcName); len(errs) > 0 {
		return "", status.Errorf(codes.InvalidArgument, "invalid_pvc_name: %s", strings.Join(errs, ", "))
	}

	if _, err := s.clientset.CoreV1().PersistentVolumeClaims(s.namespace).Get(ctx, pvcName, metav1.GetOptions{}); err == nil {
		return pvcName, nil
	} else if !apierrors.IsNotFound(err) {
		return "", grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	requestSize, err := resource.ParseQuantity(s.storageSize)
	if err != nil {
		return "", status.Errorf(codes.Internal, "invalid_storage_size: %v", err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: s.namespace,
			Labels: map[string]string{
				managedByLabelKey: managedByLabelValue,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: requestSize},
			},
		},
	}
	if s.storageClass != nil {
		pvc.Spec.StorageClassName = s.storageClass
	}
	for key, value := range labels {
		if key == workloadIDLabelKey {
			continue
		}
		pvc.Labels[key] = value
	}

	if _, err := s.clientset.CoreV1().PersistentVolumeClaims(s.namespace).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		return "", grpcErrorFromKube(s.logger, err, codes.Internal)
	}

	s.logger.Info("created pvc", zap.String("pvc", pvcName))
	return pvcName, nil
}

func buildContainers(req *runnerv1.StartWorkloadRequest, volumes []corev1.Volume) ([]corev1.Container, []corev1.Container, []string, error) {
	volumeLookup := make(map[string]struct{}, len(volumes))
	for _, volume := range volumes {
		volumeLookup[volume.Name] = struct{}{}
	}

	containers := make([]corev1.Container, 0, 1+len(req.Sidecars))
	initContainers := make([]corev1.Container, 0, len(req.InitContainers))
	nameLookup := make(map[string]struct{}, 1+len(req.Sidecars)+len(req.InitContainers))

	mainContainer, err := buildContainer(req.Main, "main", volumeLookup)
	if err != nil {
		return nil, nil, nil, err
	}
	containers = append(containers, mainContainer)
	nameLookup[mainContainer.Name] = struct{}{}

	sidecarNames := make([]string, 0, len(req.Sidecars))
	for idx, sidecar := range req.Sidecars {
		container, err := buildContainer(sidecar, fmt.Sprintf("sidecar-%d", idx+1), volumeLookup)
		if err != nil {
			return nil, nil, nil, err
		}
		if _, exists := nameLookup[container.Name]; exists {
			return nil, nil, nil, status.Errorf(codes.InvalidArgument, "duplicate_container_name: %s", container.Name)
		}
		nameLookup[container.Name] = struct{}{}
		containers = append(containers, container)
		sidecarNames = append(sidecarNames, container.Name)
	}

	for idx, initContainer := range req.InitContainers {
		container, err := buildContainer(initContainer, fmt.Sprintf("init-%d", idx+1), volumeLookup)
		if err != nil {
			return nil, nil, nil, err
		}
		if _, exists := nameLookup[container.Name]; exists {
			return nil, nil, nil, status.Errorf(codes.InvalidArgument, "duplicate_container_name: %s", container.Name)
		}
		nameLookup[container.Name] = struct{}{}
		initContainers = append(initContainers, container)
	}

	return containers, initContainers, sidecarNames, nil
}

func buildContainer(spec *runnerv1.ContainerSpec, fallbackName string, volumeLookup map[string]struct{}) (corev1.Container, error) {
	if spec == nil {
		return corev1.Container{}, status.Error(codes.InvalidArgument, "container_spec_required")
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = fallbackName
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return corev1.Container{}, status.Errorf(codes.InvalidArgument, "invalid_container_name: %s", strings.Join(errs, ", "))
	}
	image := strings.TrimSpace(spec.Image)
	if image == "" {
		return corev1.Container{}, status.Error(codes.InvalidArgument, "container_image_required")
	}

	volumeMounts := make([]corev1.VolumeMount, 0, len(spec.Mounts))
	for _, mount := range spec.Mounts {
		if mount == nil {
			continue
		}
		volumeName := strings.TrimSpace(mount.Volume)
		if volumeName == "" {
			return corev1.Container{}, status.Error(codes.InvalidArgument, "volume_mount_name_required")
		}
		if _, ok := volumeLookup[volumeName]; !ok {
			return corev1.Container{}, status.Errorf(codes.InvalidArgument, "volume_not_defined: %s", volumeName)
		}
		mountPath := strings.TrimSpace(mount.MountPath)
		if mountPath == "" {
			return corev1.Container{}, status.Error(codes.InvalidArgument, "mount_path_required")
		}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
			ReadOnly:  mount.ReadOnly,
		})
	}

	var envVars []corev1.EnvVar
	for _, env := range spec.Env {
		if env == nil {
			continue
		}
		name := strings.TrimSpace(env.Name)
		if name == "" {
			return corev1.Container{}, status.Error(codes.InvalidArgument, "env_name_required")
		}
		if errs := validation.IsEnvVarName(name); len(errs) > 0 {
			return corev1.Container{}, status.Errorf(codes.InvalidArgument, "invalid_env_name: %s", strings.Join(errs, ", "))
		}
		envVars = append(envVars, corev1.EnvVar{Name: name, Value: env.Value})
	}

	container := corev1.Container{
		Name:         name,
		Image:        image,
		Args:         append([]string{}, spec.Cmd...),
		Env:          envVars,
		WorkingDir:   strings.TrimSpace(spec.WorkingDir),
		VolumeMounts: volumeMounts,
	}
	if entrypoint := strings.TrimSpace(spec.Entrypoint); entrypoint != "" {
		if strings.ContainsAny(entrypoint, " \t\n\r") {
			return corev1.Container{}, status.Error(codes.InvalidArgument, "entrypoint_must_be_single_path")
		}
		// Entrypoint is a single binary path; use Cmd for args.
		container.Command = []string{entrypoint}
	}

	if len(spec.RequiredCapabilities) > 0 {
		caps := make([]corev1.Capability, 0, len(spec.RequiredCapabilities))
		for _, capability := range spec.RequiredCapabilities {
			name := strings.TrimSpace(capability)
			if name == "" {
				continue
			}
			caps = append(caps, corev1.Capability(name))
		}
		if len(caps) > 0 {
			container.SecurityContext = &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Add: caps,
				},
			}
		}
	}

	if policy, ok := spec.AdditionalProperties["restart_policy"]; ok && policy == "Always" {
		always := corev1.ContainerRestartPolicyAlways
		container.RestartPolicy = &always
	}

	return container, nil
}

func parsePVCAnnotation(annotations map[string]string) []string {
	if annotations == nil {
		return nil
	}
	value := strings.TrimSpace(annotations[pvcAnnotationKey])
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

type volumeInfo struct {
	mountType string
	source    string
}

func mountsForPod(pod *corev1.Pod, mainContainerName string) []*runnerv1.TargetMount {
	if pod == nil {
		return nil
	}
	volumeSources := make(map[string]volumeInfo)
	for _, volume := range pod.Spec.Volumes {
		switch {
		case volume.PersistentVolumeClaim != nil:
			volumeSources[volume.Name] = volumeInfo{mountType: "pvc", source: volume.PersistentVolumeClaim.ClaimName}
		case volume.EmptyDir != nil:
			volumeSources[volume.Name] = volumeInfo{mountType: "emptydir", source: volume.Name}
		}
	}

	var mounts []*runnerv1.TargetMount
	for _, container := range pod.Spec.Containers {
		if container.Name != mainContainerName {
			continue
		}
		for _, mount := range container.VolumeMounts {
			info, ok := volumeSources[mount.Name]
			if !ok {
				continue
			}
			mounts = append(mounts, &runnerv1.TargetMount{
				Type:        info.mountType,
				Source:      info.source,
				Destination: mount.MountPath,
				ReadOnly:    mount.ReadOnly,
			})
		}
	}

	return mounts
}
