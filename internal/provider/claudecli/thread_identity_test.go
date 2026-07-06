package claudecli

import (
	"context"
	"testing"
	"time"
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

func TestStartupMarksThreadReadyImmediately(t *testing.T) {
	// Claude CLI may not emit system:init until after the first user message.
	// Startup must mark ready immediately for both fresh sessions and resume
	// sessions so that StartTurn can send that message.
	cases := []struct {
		name   string
		specID string
	}{
		{"empty", ""},
		{"agent placeholder", "agent_1778254389737948000"},
		{"pending placeholder", "pending"},
		{"real v4 uuid resume path", "11111111-2222-3333-4444-555555555555"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newBufferedTransport(t, tc.specID)
			defer st.finish()
			s := (&driver{}).newStartedSession(startSpec{
				agentID:      "agent-1",
				threadID:     tc.specID,
				publicThread: "thread-public",
			}, preparedStartSession{transport: st.tr})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if err := s.awaitResolvedThreadID(ctx); err != nil {
				t.Fatalf("expected immediate ready, got %v", err)
			}
		})
	}
}

func TestAwaitResolvedThreadID(t *testing.T) {
	s := &session{threadReady: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		time.Sleep(10 * time.Millisecond)
		s.setResolvedThreadID("thread-123")
	})

	if err := s.awaitResolvedThreadID(ctx); err != nil {
		t.Fatalf("awaitResolvedThreadID() error = %v", err)
	}
	if got := s.ThreadID(); got != "thread-123" {
		t.Fatalf("ThreadID() = %q, want %q", got, "thread-123")
	}
}
