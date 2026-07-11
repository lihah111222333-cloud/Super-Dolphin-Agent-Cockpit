package cachekeepaliveadapter

import (
	"context"
	"errors"
	"testing"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestCacheKeepaliveProvidersPreserveNilStoreSemantics(t *testing.T) {
	t.Parallel()

	if got := provideCacheKeepaliveBindingLookup(nil); got != nil {
		t.Fatalf("provideCacheKeepaliveBindingLookup(nil) = %#v, want nil", got)
	}
	if got := provideCacheKeepaliveThreadLookup(nil); got != nil {
		t.Fatalf("provideCacheKeepaliveThreadLookup(nil) = %#v, want nil", got)
	}
}

func TestCacheKeepaliveBindingLookupProjectsFieldsAndPreservesError(t *testing.T) {
	t.Parallel()

	lookupErr := errors.New("binding lookup failed")
	errorStore := &bindingStoreStub{err: lookupErr}
	got, err := provideCacheKeepaliveBindingLookup(errorStore).GetCacheKeepaliveBindingByAgentID(context.Background(), "agent-1")
	if !errors.Is(err, lookupErr) || got != nil {
		t.Fatalf("error lookup = (%#v, %v), want (nil, %v)", got, err, lookupErr)
	}

	store := &bindingStoreStub{binding: &bindingstore.Binding{AgentID: "agent-1", Archived: true}}
	got, err = provideCacheKeepaliveBindingLookup(store).GetCacheKeepaliveBindingByAgentID(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("binding lookup error = %v", err)
	}
	if got == nil || got.AgentID != "agent-1" || !got.Archived {
		t.Fatalf("binding lookup = %#v, want projected identity and archived fields", got)
	}
}

func TestCacheKeepaliveThreadLookupProjectsFieldsAndPreservesError(t *testing.T) {
	t.Parallel()

	lookupErr := errors.New("thread lookup failed")
	errorStore := &threadStoreStub{err: lookupErr}
	got, err := provideCacheKeepaliveThreadLookup(errorStore).GetCacheKeepaliveThreadByID(context.Background(), "thread-1")
	if !errors.Is(err, lookupErr) || got != nil {
		t.Fatalf("error lookup = (%#v, %v), want (nil, %v)", got, err, lookupErr)
	}

	store := &threadStoreStub{thread: &threadstore.Thread{ThreadID: "thread-1", AgentID: "agent-1"}}
	got, err = provideCacheKeepaliveThreadLookup(store).GetCacheKeepaliveThreadByID(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("thread lookup error = %v", err)
	}
	if got == nil || got.ThreadID != "thread-1" || got.AgentID != "agent-1" {
		t.Fatalf("thread lookup = %#v, want projected thread and agent identity", got)
	}
}

type bindingStoreStub struct {
	bindingstore.Store
	binding *bindingstore.Binding
	err     error
}

func (s *bindingStoreStub) GetByAgentID(context.Context, string) (*bindingstore.Binding, error) {
	return s.binding, s.err
}

type threadStoreStub struct {
	threadstore.Store
	thread *threadstore.Thread
	err    error
}

func (s *threadStoreStub) GetByThreadID(context.Context, string) (*threadstore.Thread, error) {
	return s.thread, s.err
}
