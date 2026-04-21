package server

import (
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
	"github.com/agynio/k8s-runner/internal/config"
)

const (
	dockerCapability              = "docker"
	dockerSidecarName             = "docker-daemon"
	dockerDataVolumeName          = "docker-data"
	dockerRunVolumeName           = "docker-run"
	dockerRootlessImage           = "docker:27-dind-rootless"
	dockerPrivilegedImage         = "docker:27-dind"
	dockerTLSCertDirEnvName       = "DOCKER_TLS_CERTDIR"
	dockerTLSCertDirDisabledValue = ""
	dockerHostEnvName             = "DOCKER_HOST"
	dockerHostEnvValue            = "tcp://localhost:2375"
	dockerRootlessDataMountPath   = "/home/rootless/.local/share/docker"
	dockerRootlessRunMountPath    = "/run/user/1000"
	dockerPrivilegedDataMountPath = "/var/lib/docker"
)

type capabilityPlan struct {
	dockerImplementation config.DockerImplementation
}

func resolveCapabilityPlan(req *runnerv1.StartWorkloadRequest, implementations config.CapabilityImplementations) (capabilityPlan, error) {
	plan := capabilityPlan{}
	capabilities, err := normalizeCapabilities(req.GetCapabilities())
	if err != nil {
		return plan, err
	}
	if len(capabilities) == 0 {
		return plan, nil
	}

	containerNames := collectContainerNames(req)
	volumeNames := collectVolumeNames(req.GetVolumes())

	for _, capability := range capabilities {
		switch capability {
		case dockerCapability:
			implementation := implementations.Docker
			if implementation == "" {
				return plan, status.Error(codes.InvalidArgument, "docker_capability_not_configured")
			}
			if implementation != config.DockerImplementationRootless && implementation != config.DockerImplementationPrivileged {
				return plan, status.Errorf(codes.InvalidArgument, "unknown_docker_implementation: %s", implementation)
			}
			if err := validateDockerInjection(containerNames, volumeNames, implementation); err != nil {
				return plan, err
			}
			plan.dockerImplementation = implementation
		default:
			return plan, status.Errorf(codes.InvalidArgument, "unknown_capability: %s", capability)
		}
	}
	return plan, nil
}

func (plan capabilityPlan) apply(containers *[]corev1.Container, volumes *[]corev1.Volume, sidecarNames *[]string) *bool {
	if plan.dockerImplementation == "" {
		return nil
	}
	if len(*containers) == 0 {
		panic("capability plan requires main container")
	}

	(*containers)[0].Env = upsertEnvVar((*containers)[0].Env, dockerHostEnvName, dockerHostEnvValue)
	*containers = append(*containers, dockerSidecarContainer(plan.dockerImplementation))
	*sidecarNames = append(*sidecarNames, dockerSidecarName)
	*volumes = append(*volumes, dockerVolumes(plan.dockerImplementation)...)

	if plan.dockerImplementation == config.DockerImplementationRootless {
		return boolPtr(false)
	}
	return nil
}

func normalizeCapabilities(capabilities []string) ([]string, error) {
	if len(capabilities) == 0 {
		return nil, nil
	}
	unique := make(map[string]struct{}, len(capabilities))
	normalized := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		name := strings.ToLower(strings.TrimSpace(capability))
		if name == "" {
			return nil, status.Error(codes.InvalidArgument, "capability_required")
		}
		if _, exists := unique[name]; exists {
			continue
		}
		unique[name] = struct{}{}
		normalized = append(normalized, name)
	}
	return normalized, nil
}

func collectContainerNames(req *runnerv1.StartWorkloadRequest) map[string]struct{} {
	names := make(map[string]struct{})
	addName := func(spec *runnerv1.ContainerSpec, fallback string) {
		if spec == nil {
			return
		}
		name := containerName(spec, fallback)
		if name == "" {
			return
		}
		names[name] = struct{}{}
	}
	addName(req.Main, "main")
	for idx, sidecar := range req.Sidecars {
		addName(sidecar, fmt.Sprintf("sidecar-%d", idx+1))
	}
	for idx, initContainer := range req.InitContainers {
		addName(initContainer, fmt.Sprintf("init-%d", idx+1))
	}
	return names
}

func collectVolumeNames(volumes []*runnerv1.VolumeSpec) map[string]struct{} {
	names := make(map[string]struct{}, len(volumes))
	for _, volume := range volumes {
		if volume == nil {
			continue
		}
		name := strings.TrimSpace(volume.Name)
		if name == "" {
			continue
		}
		names[name] = struct{}{}
	}
	return names
}

func containerName(spec *runnerv1.ContainerSpec, fallback string) string {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return fallback
	}
	return name
}

func validateDockerInjection(containerNames, volumeNames map[string]struct{}, implementation config.DockerImplementation) error {
	if _, exists := containerNames[dockerSidecarName]; exists {
		return status.Errorf(codes.InvalidArgument, "capability_container_name_conflict: %s", dockerSidecarName)
	}
	if _, exists := volumeNames[dockerDataVolumeName]; exists {
		return status.Errorf(codes.InvalidArgument, "capability_volume_name_conflict: %s", dockerDataVolumeName)
	}
	if implementation == config.DockerImplementationRootless {
		if _, exists := volumeNames[dockerRunVolumeName]; exists {
			return status.Errorf(codes.InvalidArgument, "capability_volume_name_conflict: %s", dockerRunVolumeName)
		}
	}
	return nil
}

func dockerSidecarContainer(implementation config.DockerImplementation) corev1.Container {
	env := []corev1.EnvVar{{Name: dockerTLSCertDirEnvName, Value: dockerTLSCertDirDisabledValue}}
	switch implementation {
	case config.DockerImplementationRootless:
		return corev1.Container{
			Name:  dockerSidecarName,
			Image: dockerRootlessImage,
			Env:   env,
			VolumeMounts: []corev1.VolumeMount{
				{Name: dockerDataVolumeName, MountPath: dockerRootlessDataMountPath},
				{Name: dockerRunVolumeName, MountPath: dockerRootlessRunMountPath},
			},
		}
	case config.DockerImplementationPrivileged:
		privileged := true
		return corev1.Container{
			Name:  dockerSidecarName,
			Image: dockerPrivilegedImage,
			Env:   env,
			VolumeMounts: []corev1.VolumeMount{
				{Name: dockerDataVolumeName, MountPath: dockerPrivilegedDataMountPath},
			},
			SecurityContext: &corev1.SecurityContext{
				Privileged:               &privileged,
				AllowPrivilegeEscalation: &privileged,
			},
		}
	default:
		panic(fmt.Sprintf("unsupported docker implementation: %s", implementation))
	}
}

func dockerVolumes(implementation config.DockerImplementation) []corev1.Volume {
	dataVolume := corev1.Volume{
		Name: dockerDataVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
	switch implementation {
	case config.DockerImplementationRootless:
		runVolume := corev1.Volume{
			Name: dockerRunVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}
		return []corev1.Volume{dataVolume, runVolume}
	case config.DockerImplementationPrivileged:
		return []corev1.Volume{dataVolume}
	default:
		panic(fmt.Sprintf("unsupported docker implementation: %s", implementation))
	}
}

func upsertEnvVar(envs []corev1.EnvVar, name, value string) []corev1.EnvVar {
	result := make([]corev1.EnvVar, 0, len(envs)+1)
	for _, env := range envs {
		if env.Name == name {
			continue
		}
		result = append(result, env)
	}
	return append(result, corev1.EnvVar{Name: name, Value: value})
}

func boolPtr(value bool) *bool {
	return &value
}
