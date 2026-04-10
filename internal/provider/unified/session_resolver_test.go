package unified

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type stubThreadLookup struct {
	thread *threadstore.Thread
	err    error
}

func (s stubThreadLookup) GetByThreadID(context.Context, string) (*threadstore.Thread, error) {
	return s.thread, s.err
}

type stubBindingLookup struct {
	bindings map[string]*bindingstore.Binding
	errs     map[string]error
}

func (s stubBindingLookup) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*bindingstore.Binding, error) {
	key := provider + ":" + providerThreadID
	if err, ok := s.errs[key]; ok {
		return nil, err
	}
	if binding, ok := s.bindings[key]; ok {
		return binding, nil
	}
	return nil, platformdb.ErrNotFound
}

func (s stubBindingLookup) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	for _, b := range s.bindings {
		if b != nil && b.AgentID == agentID {
			return b, nil
		}
	}
	return nil, platformdb.ErrNotFound
}

func TestSessionResolverResolveSessionUsesThreadStoreAgent(t *testing.T) {
	sessions := NewSessionManager(nil)
	session := &generationTestSession{threadID: "thread-1"}
	sessions.Register("agent-1", session)

	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &threadstore.Thread{ThreadID: "thread-1", AgentID: "agent-1"}},
		registry:    NewRegistry(RegistryParams{}),
		sessions:    sessions,
	}

	got, err := resolver.ResolveSession(context.Background(), "thread-1")
	if err != nil || got != session {
		t.Fatalf("ResolveSession() = (%#v, %v)", got, err)
	}
}

func TestSessionResolverResolveSessionFallsBackToProviderThreadBinding(t *testing.T) {
	sessions := NewSessionManager(nil)
	session := &generationTestSession{threadID: "provider-thread-1"}
	sessions.Register("agent-2", session)

	resolver := &sessionResolver{
		threadStore:  stubThreadLookup{err: platformdb.ErrNotFound},
		bindingStore: stubBindingLookup{bindings: map[string]*bindingstore.Binding{"codex:provider-thread-1": {AgentID: "agent-2"}}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return nil }},
			{Name: "claude", Create: func() contract.Driver { return nil }},
		}}),
		sessions: sessions,
	}

	got, err := resolver.ResolveSession(context.Background(), "provider-thread-1")
	if err != nil || got != session {
		t.Fatalf("ResolveSession() = (%#v, %v)", got, err)
	}
}

func TestSessionResolverResolveSessionReturnsLookupErrors(t *testing.T) {
	sessions := NewSessionManager(nil)
	wantErr := errors.New("db unavailable")
	resolver := &sessionResolver{
		threadStore:  stubThreadLookup{err: wantErr},
		bindingStore: stubBindingLookup{errs: map[string]error{"codex:thread-404": wantErr}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return nil }},
		}}),
		sessions: sessions,
	}

	_, err := resolver.ResolveSession(context.Background(), "thread-404")
	if err == nil || !strings.Contains(err.Error(), "db unavailable") {
		t.Fatalf("ResolveSession() error = %v", err)
	}
}
