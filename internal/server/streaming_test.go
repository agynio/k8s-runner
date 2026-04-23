package server

import (
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLogReadError(t *testing.T) {
	tests := []struct {
		name        string
		readErr     error
		podAlive    bool
		wantNil     bool
		wantCode    codes.Code
		wantMessage string
	}{
		{
			name:     "eof_pod_alive",
			readErr:  io.EOF,
			podAlive: true,
			wantNil:  true,
		},
		{
			name:        "eof_pod_deleted",
			readErr:     io.EOF,
			podAlive:    false,
			wantCode:    codes.Unavailable,
			wantMessage: "pod_deleted",
		},
		{
			name:        "read_error_pod_deleted",
			readErr:     errors.New("boom"),
			podAlive:    false,
			wantCode:    codes.Unavailable,
			wantMessage: "pod_deleted",
		},
		{
			name:        "read_error_pod_alive",
			readErr:     errors.New("boom"),
			podAlive:    true,
			wantCode:    codes.Internal,
			wantMessage: "logs_stream_error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := logReadError(test.readErr, test.podAlive)
			if test.wantNil {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected grpc status error, got %v", err)
			}
			if st.Code() != test.wantCode {
				t.Fatalf("expected code %v, got %v", test.wantCode, st.Code())
			}
			if st.Message() != test.wantMessage {
				t.Fatalf("expected message %q, got %q", test.wantMessage, st.Message())
			}
		})
	}
}
