package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

// The SDK retries a rejected bind forever, so the runner stayed Running with no
// terminator and the platform kept scheduling onto something nothing could
// reach. Giving up lets Kubernetes restart it into a fresh enrolment.
func TestWatchZitiIdentityGivesUpAfterThreshold(t *testing.T) {
	calls := 0
	err := watchZitiIdentity(context.Background(), func() error {
		calls++
		return errors.New("credential submission failed with status 401")
	}, time.Millisecond, 3, zap.NewNop())

	if err == nil {
		t.Fatal("expected an error once the threshold was reached")
	}
	if calls != 3 {
		t.Fatalf("expected 3 checks, got %d", calls)
	}
}

// A single failed check is not a lost identity.
func TestWatchZitiIdentityResetsAfterRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := watchZitiIdentity(ctx, func() error {
		calls++
		switch {
		case calls < 3:
			return errors.New("transient")
		case calls >= 6:
			cancel()
		}
		return nil
	}, time.Millisecond, 3, zap.NewNop())

	if err != nil {
		t.Fatalf("expected recovery to clear the failures, got %v", err)
	}
}

func TestWatchZitiIdentityStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := watchZitiIdentity(ctx, func() error { return errors.New("unused") }, time.Millisecond, 3, zap.NewNop()); err != nil {
		t.Fatalf("expected a clean stop, got %v", err)
	}
}
