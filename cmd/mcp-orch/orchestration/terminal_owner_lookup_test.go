package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveTerminalOwnerPrefersDurableBinding verifies that a persisted
// thread owner wins over a conflicting in-memory registry entry.
func TestResolveTerminalOwnerPrefersDurableBinding(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.agentThreads = terminalOwnerLookupThreadStore{owner: "durable-agent"}
	svc.registry.agents["memory-agent"] = &agentRuntime{id: "memory-agent", remoteThreadID: "thread-1"}

	owner, err := svc.resolveTerminalOwner(context.Background(), "thread-1")
	require.NoError(t, err)
	require.Equal(t, "durable-agent", owner)
}

// TestResolveTerminalOwnerRejectsMissingDurableLookup verifies that a
// configured persistence store without the narrow lookup port cannot silently
// fall back to the process-local registry.
func TestResolveTerminalOwnerRejectsMissingDurableLookup(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.agentThreads = terminalOwnerThreadStoreWithoutLookup{}
	svc.registry.agents["memory-agent"] = &agentRuntime{id: "memory-agent", remoteThreadID: "thread-1"}

	owner, err := svc.resolveTerminalOwner(context.Background(), "thread-1")
	require.Empty(t, owner)
	require.Error(t, err)
	require.Contains(t, err.Error(), "durable terminal owner lookup is unavailable")
}

type terminalOwnerLookupThreadStore struct{ owner string }

func (s terminalOwnerLookupThreadStore) ListAll(context.Context) ([]PersistedThread, error) {
	return nil, nil
}

func (s terminalOwnerLookupThreadStore) GetByThreadID(context.Context, string) (*PersistedThread, error) {
	return nil, errors.New("not used")
}

func (s terminalOwnerLookupThreadStore) UpdateStatus(context.Context, PersistedThreadStatusUpdate) error {
	return nil
}

func (s terminalOwnerLookupThreadStore) LookupOwnerByThread(context.Context, string) (string, error) {
	if s.owner == "" {
		return "", errors.New("owner missing")
	}
	return s.owner, nil
}

type terminalOwnerThreadStoreWithoutLookup struct{}

func (terminalOwnerThreadStoreWithoutLookup) ListAll(context.Context) ([]PersistedThread, error) {
	return nil, nil
}

func (terminalOwnerThreadStoreWithoutLookup) GetByThreadID(context.Context, string) (*PersistedThread, error) {
	return nil, errors.New("not used")
}

func (terminalOwnerThreadStoreWithoutLookup) UpdateStatus(context.Context, PersistedThreadStatusUpdate) error {
	return nil
}
