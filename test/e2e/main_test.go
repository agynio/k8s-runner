//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	runnerv1 "github.com/agynio/k8s-runner/internal/.gen/agynio/api/runner/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultRunnerAddr = "k8s-runner:50051"
	dialTimeout       = 20 * time.Second
)

var (
	runnerClient runnerv1.RunnerServiceClient
	runnerConn   *grpc.ClientConn
)

func TestMain(m *testing.M) {
	addr := os.Getenv("K8S_RUNNER_ADDR")
	if addr == "" {
		addr = defaultRunnerAddr
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to %s: %v\n", addr, err)
		os.Exit(1)
	}

	runnerConn = conn
	runnerClient = runnerv1.NewRunnerServiceClient(conn)

	exitCode := m.Run()
	if err := conn.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to close gRPC connection: %v\n", err)
	}
	os.Exit(exitCode)
}
