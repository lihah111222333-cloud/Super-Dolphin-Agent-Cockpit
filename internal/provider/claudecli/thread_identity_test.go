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
