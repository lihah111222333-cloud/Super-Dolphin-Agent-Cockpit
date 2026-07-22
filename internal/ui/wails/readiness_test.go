package wails

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestActivationReadinessWaitsForApplicationStartedOnce(t *testing.T) {
	readiness := NewActivationReadiness()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := readiness.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() error before activation = %v, want deadline", err)
	}
	readiness.MarkApplicationStarted()
	readiness.MarkApplicationStarted()
	if err := readiness.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if !readiness.Activated() {
		t.Fatal("Activated() = false after ApplicationStarted")
	}
}

func TestActivationReadinessPropagatesMissingActivation(t *testing.T) {
	readiness := NewActivationReadiness()
	sentinel := errors.New("wails run returned before activation")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(sentinel)
	if err := readiness.Wait(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want %v", err, sentinel)
	}
}

func TestActivationReadinessRequiresMatchingFrontendEpoch(t *testing.T) {
	readiness := NewActivationReadiness()
	readiness.MarkApplicationStarted()

	epoch, err := readiness.CurrentEpoch()
	if err != nil {
		t.Fatalf("CurrentEpoch() error = %v", err)
	}
	if err := readiness.MarkFrontendReady(epoch + 1); err == nil {
		t.Fatal("MarkFrontendReady() error = nil for a stale epoch")
	}
	if readiness.FrontendReady() {
		t.Fatal("FrontendReady() = true after stale epoch")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := readiness.WaitForFrontendReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForFrontendReady() error before frontend RPC = %v, want deadline", err)
	}
	if err := readiness.MarkFrontendReady(epoch); err != nil {
		t.Fatalf("MarkFrontendReady() error = %v", err)
	}
	if err := readiness.MarkFrontendReady(epoch); err != nil {
		t.Fatalf("duplicate MarkFrontendReady() error = %v", err)
	}
	if err := readiness.WaitForFrontendReady(context.Background()); err != nil {
		t.Fatalf("WaitForFrontendReady() error = %v", err)
	}
}
