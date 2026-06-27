package codexapp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type blockingCloseServer struct {
	calls atomic.Int32
	wait  chan struct{}
}

func (s *blockingCloseServer) ServerURL() string { return "ws://blocked" }

func (s *blockingCloseServer) Close(ctx context.Context) error {
	s.calls.Add(1)
	select {
	case <-s.wait:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *blockingCloseServer) Alive() bool { return true }

func TestServerPoolCloseStopsSerialCloseWhenContextExpires(t *testing.T) {
	t.Parallel()

	p, _ := newPoolForTest(t, nil, PoolConfig{})
	first := &blockingCloseServer{wait: make(chan struct{})}
	second := &blockingCloseServer{wait: make(chan struct{})}
	p.entries[poolEntryKey{home: t.TempDir(), instanceKey: "first", modelProvider: "mp", ownerKey: "agent-1"}] = &poolEntry{server: first}
	p.entries[poolEntryKey{home: t.TempDir(), instanceKey: "second", modelProvider: "mp", ownerKey: "agent-2"}] = &poolEntry{server: second}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := p.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want context deadline", err)
	}
	if got := first.calls.Load() + second.calls.Load(); got != 1 {
		t.Fatalf("Close calls = %d, want stop after first context-expired close", got)
	}
}
