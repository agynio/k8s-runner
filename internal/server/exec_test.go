package server

import (
	"reflect"
	"testing"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
)

func TestBuildExecCommandArgv(t *testing.T) {
	start := &runnerv1.ExecStartRequest{CommandArgv: []string{"ls", "-l"}}
	command, err := buildExecCommand(start)
	if err != nil {
		t.Fatalf("buildExecCommand returned error: %v", err)
	}
	if !reflect.DeepEqual(command, []string{"ls", "-l"}) {
		t.Fatalf("unexpected command: %#v", command)
	}
}

func TestBuildExecCommandShell(t *testing.T) {
	start := &runnerv1.ExecStartRequest{CommandShell: "echo hello"}
	command, err := buildExecCommand(start)
	if err != nil {
		t.Fatalf("buildExecCommand returned error: %v", err)
	}
	expected := []string{"/bin/sh", "-c", "echo hello"}
	if !reflect.DeepEqual(command, expected) {
		t.Fatalf("unexpected command: %#v", command)
	}
}

func TestBuildExecCommandRejectsMutualExclusive(t *testing.T) {
	start := &runnerv1.ExecStartRequest{
		CommandArgv:  []string{"ls"},
		CommandShell: "echo hi",
	}
	if _, err := buildExecCommand(start); err == nil {
		t.Fatalf("expected error for mutually exclusive command fields")
	}
}

func TestBuildExecCommandAppliesEnvAndWorkdir(t *testing.T) {
	start := &runnerv1.ExecStartRequest{
		CommandArgv: []string{"app", "--flag"},
		Options: &runnerv1.ExecOptions{
			Workdir: "/work",
			Env: []*runnerv1.EnvVar{
				{Name: "FOO", Value: "bar"},
			},
		},
	}
	command, err := buildExecCommand(start)
	if err != nil {
		t.Fatalf("buildExecCommand returned error: %v", err)
	}
	expected := []string{
		"env", "FOO=bar",
		"/bin/sh", "-c", "cd \"$1\" && shift && exec \"$@\"", "--", "/work",
		"app", "--flag",
	}
	if !reflect.DeepEqual(command, expected) {
		t.Fatalf("unexpected command: %#v", command)
	}
}

func TestResolveExitTailBytesClamps(t *testing.T) {
	if got := resolveExitTailBytes(nil); got != defaultExitTailBytes {
		t.Fatalf("expected default exit tail bytes %d, got %d", defaultExitTailBytes, got)
	}

	options := &runnerv1.ExecOptions{ExitTailBytes: maxExitTailBytes + 1}
	if got := resolveExitTailBytes(options); got != maxExitTailBytes {
		t.Fatalf("expected clamped exit tail bytes %d, got %d", maxExitTailBytes, got)
	}
}

func TestTailBufferTruncates(t *testing.T) {
	buf := newTailBuffer(4)
	buf.Write([]byte("ab"))
	buf.Write([]byte("cd"))
	if got := string(buf.Bytes()); got != "abcd" {
		t.Fatalf("expected buffer 'abcd', got %q", got)
	}
	buf.Write([]byte("ef"))
	if got := string(buf.Bytes()); got != "cdef" {
		t.Fatalf("expected buffer 'cdef', got %q", got)
	}
	buf.Write([]byte("012345"))
	if got := string(buf.Bytes()); got != "2345" {
		t.Fatalf("expected buffer '2345', got %q", got)
	}
}
