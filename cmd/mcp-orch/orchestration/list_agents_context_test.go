package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

func TestListAgentsReturnsWhenContextExpiresWhileWaitingForReadLock(t *testing.T) {
	svc := &service{agents: map[string]*agentRuntime{
		"agent-1": {id: "agent-1", state: agentdto.StateIdle},
	}}
	svc.mu.Lock()
	defer svc.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	result := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := svc.ListAgents(ctx)
		result <- err
	})

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ListAgents() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ListAgents() remained blocked after context deadline")
	}
}
