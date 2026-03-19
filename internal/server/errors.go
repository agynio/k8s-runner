package server

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func grpcErrorFromKube(err error, fallback codes.Code) error {
	if err == nil {
		return nil
	}

	switch {
	case apierrors.IsNotFound(err):
		return status.Error(codes.NotFound, err.Error())
	case apierrors.IsAlreadyExists(err):
		return status.Error(codes.AlreadyExists, err.Error())
	case apierrors.IsInvalid(err):
		return status.Error(codes.InvalidArgument, err.Error())
	case apierrors.IsUnauthorized(err):
		return status.Error(codes.Unauthenticated, err.Error())
	case apierrors.IsForbidden(err):
		return status.Error(codes.PermissionDenied, err.Error())
	case apierrors.IsConflict(err):
		return status.Error(codes.Aborted, err.Error())
	default:
		return status.Error(fallback, err.Error())
	}
}
