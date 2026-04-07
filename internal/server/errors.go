package server

import (
	"errors"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func grpcErrorFromKube(logger *zap.Logger, err error, fallback codes.Code) error {
	if err == nil {
		return nil
	}
	if logger != nil {
		logger.Error("kubernetes api error", zap.Error(err))
	}

	message := kubeStatusMessage(err)

	switch {
	case apierrors.IsNotFound(err):
		return status.Error(codes.NotFound, formatKubeError("resource not found", message))
	case apierrors.IsAlreadyExists(err):
		return status.Error(codes.AlreadyExists, formatKubeError("resource already exists", message))
	case apierrors.IsInvalid(err):
		return status.Error(codes.InvalidArgument, formatKubeError("invalid kubernetes request", message))
	case apierrors.IsUnauthorized(err):
		return status.Error(codes.Unauthenticated, formatKubeError("unauthenticated", message))
	case apierrors.IsForbidden(err):
		return status.Error(codes.PermissionDenied, formatKubeError("permission denied", message))
	case apierrors.IsConflict(err):
		return status.Error(codes.Aborted, formatKubeError("resource conflict", message))
	default:
		return status.Error(fallback, formatKubeError("kubernetes request failed", message))
	}
}

func formatKubeError(base, detail string) string {
	if detail == "" {
		return base
	}
	return base + ": " + detail
}

func kubeStatusMessage(err error) string {
	var apiStatus apierrors.APIStatus
	if !errors.As(err, &apiStatus) {
		return ""
	}

	message := strings.TrimSpace(apiStatus.Status().Message)
	if message == "" {
		return ""
	}

	return message
}
