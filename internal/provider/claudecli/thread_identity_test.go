package claudecli

import (
	"context"
	"testing"
	"time"
)

func TestRequiresResolvedThreadID(t *testing.T) {
	testCases := []struct {
		threadID string
		want     bool
	}{
		{threadID: "", want: true},
		{threadID: "pending", want: true},
		{threadID: "unknown", want: true},
		{threadID: "thread-123", want: false},
	}
	for _, tc := range testCases {
		if got := requiresResolvedThreadID(tc.threadID); got != tc.want {
			t.Fatalf("requiresResolvedThreadID(%q) = %v, want %v", tc.threadID, got, tc.want)
		}
	}
}

func TestStartSessionWithPublicThreadIsImmediatelyReady(t *testing.T) {
	// Reproduces the bug: StartSession provides publicThread (agentID) but
	// spec.threadID is empty. The current driver.start only checks
	// spec.threadID, so it fails to mark the session ready — causing a
	// timeout waiting for the CLI to send a system:init event.
	s := &session{
		publicThreadID: "agent_123",
		threadID:       "agent_123",
		threadReady:    make(chan struct{}),
	}

	// Use shouldMarkThreadReady which encapsulates the driver.start decision.
	specThreadID := "" // empty for StartSession (no resume)
	if shouldMarkThreadReady(specThreadID, s.publicThreadID) {
		s.markThreadReady()
	}

	// awaitResolvedThreadID must return instantly — 50ms timeout proves it.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.awaitResolvedThreadID(ctx); err != nil {
		t.Fatalf("awaitResolvedThreadID() timed out: %v — session should be immediately ready when publicThread is known", err)
	}
}

func TestAwaitResolvedThreadID(t *testing.T) {
	s := &session{threadReady: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		time.Sleep(10 * time.Millisecond)
		s.setResolvedThreadID("thread-123")
	}()

	if err := s.awaitResolvedThreadID(ctx); err != nil {
		t.Fatalf("awaitResolvedThreadID() error = %v", err)
	}
	if got := s.ThreadID(); got != "thread-123" {
		t.Fatalf("ThreadID() = %q, want %q", got, "thread-123")
	}
}
