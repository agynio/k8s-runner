package server

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
			name: "forbidden",
			err:  apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "pod-1", errors.New("no access")),
			code: codes.PermissionDenied,
			base: "permission denied",
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
	if st.Message() != "kubernetes request failed" {
		t.Fatalf("expected fallback message, got %q", st.Message())
	}
}

func TestGrpcErrorFromKubeNil(t *testing.T) {
	if err := grpcErrorFromKube(nil, nil, codes.Internal); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
