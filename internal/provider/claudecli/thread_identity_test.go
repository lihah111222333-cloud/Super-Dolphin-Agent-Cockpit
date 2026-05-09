package claudecli

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
)

func TestIsPlaceholderThreadID(t *testing.T) {
	testCases := []struct {
		threadID string
		want     bool
	}{
		{threadID: "", want: true},
		{threadID: "pending", want: true},
		{threadID: "unknown", want: true},
		{threadID: "agent_1778254389737948000", want: true},
		{threadID: "thread-123", want: false},
		{threadID: "11111111-2222-3333-4444-555555555555", want: false},
	}
	for _, tc := range testCases {
		if got := isPlaceholderThreadID(tc.threadID); got != tc.want {
			t.Fatalf("isPlaceholderThreadID(%q) = %v, want %v", tc.threadID, got, tc.want)
		}
	}
}

func TestStartSessionWithPlaceholderThreadIsImmediatelyReady(t *testing.T) {
	// markThreadReady gate (driver.go): if spec.threadID is not a real
	// claude UUID, treat as fresh start and immediately mark ready so
	// StartSession can return without waiting for system:init.
	cases := []struct {
		name     string
		specID   string
		wantMark bool
	}{
		{"empty", "", true},
		{"agent placeholder", "agent_1778254389737948000", true},
		{"pending placeholder", "pending", true},
		{"real v4 uuid (resume path)", "11111111-2222-3333-4444-555555555555", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &session{
				publicThreadID: "ext_thread_123",
				threadID:       "ext_thread_123",
				threadReady:    make(chan struct{}),
			}
			if !identifier.IsClaudeCLISessionUUID(tc.specID) {
				s.markThreadReady()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := s.awaitResolvedThreadID(ctx)
			if tc.wantMark && err != nil {
				t.Fatalf("expected immediate ready, got %v", err)
			}
			if !tc.wantMark && err == nil {
				t.Fatalf("expected awaitResolvedThreadID to block until timeout for resume path")
			}
		})
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
