package server

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestGrpcErrorFromKubeIncludesStatusDetail(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
		base string
	}{
		{
			name: "not_found",
			err:  apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "pod-1"),
			code: codes.NotFound,
			base: "resource not found",
		},
		{
			name: "already_exists",
			err:  apierrors.NewAlreadyExists(schema.GroupResource{Resource: "pods"}, "pod-1"),
			code: codes.AlreadyExists,
			base: "resource already exists",
		},
		{
			name: "invalid",
			err: apierrors.NewInvalid(
				schema.GroupKind{Group: "core", Kind: "Pod"},
				"pod-1",
				field.ErrorList{field.Invalid(field.NewPath("spec").Child("node"), "bad", "invalid node")},
			),
			code: codes.InvalidArgument,
			base: "invalid kubernetes request",
		},
		{
			name: "unauthorized",
			err:  apierrors.NewUnauthorized("token missing"),
			code: codes.Unauthenticated,
			base: "unauthenticated",
		},
		{
			name: "forbidden",
			err:  apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "pod-1", errors.New("no access")),
			code: codes.PermissionDenied,
			base: "permission denied",
		},
		{
			name: "conflict",
			err:  apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "pod-1", errors.New("update conflict")),
			code: codes.Aborted,
			base: "resource conflict",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := grpcErrorFromKube(nil, test.err, codes.Internal)
			st, ok := status.FromError(got)
			if !ok {
				t.Fatalf("expected grpc status error")
			}
			if st.Code() != test.code {
				t.Fatalf("expected code %s, got %s", test.code, st.Code())
			}
			apiStatus, ok := test.err.(apierrors.APIStatus)
			if !ok {
				t.Fatalf("expected api status error")
			}
			detail := strings.TrimSpace(apiStatus.Status().Message)
			if detail == "" {
				t.Fatalf("expected status message detail")
			}
			expected := test.base + ": " + detail
			if st.Message() != expected {
				t.Fatalf("expected message %q, got %q", expected, st.Message())
			}
		})
	}
}

func TestGrpcErrorFromKubeFallbackMessage(t *testing.T) {
	got := grpcErrorFromKube(nil, errors.New("boom"), codes.Internal)
	st, ok := status.FromError(got)
	if !ok {
		t.Fatalf("expected grpc status error")
	}
	if st.Code() != codes.Internal {
		t.Fatalf("expected code %s, got %s", codes.Internal, st.Code())
	}
	if st.Message() != "kubernetes request failed: boom" {
		t.Fatalf("expected fallback message, got %q", st.Message())
	}
}

func TestGrpcErrorFromKubeNil(t *testing.T) {
	if err := grpcErrorFromKube(nil, nil, codes.Internal); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
