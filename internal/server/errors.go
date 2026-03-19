package server

import (
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

	switch {
	case apierrors.IsNotFound(err):
		return status.Error(codes.NotFound, "resource not found")
	case apierrors.IsAlreadyExists(err):
		return status.Error(codes.AlreadyExists, "resource already exists")
	case apierrors.IsInvalid(err):
		return status.Error(codes.InvalidArgument, "invalid kubernetes request")
	case apierrors.IsUnauthorized(err):
		return status.Error(codes.Unauthenticated, "unauthenticated")
	case apierrors.IsForbidden(err):
		return status.Error(codes.PermissionDenied, "permission denied")
	case apierrors.IsConflict(err):
		return status.Error(codes.Aborted, "resource conflict")
	default:
		return status.Error(fallback, "kubernetes request failed")
	}
}
