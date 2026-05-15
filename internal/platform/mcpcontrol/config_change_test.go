package mcpcontrol

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

type stubConfigPeer struct {
	mu              sync.Mutex
	methods         []string
	params          []any
	callbackMethods []string
	callbackParams  []any
	notifyFn        func(context.Context, string, any) error
	callbackFn      func(context.Context, string, any, any) error
}

func (s *stubConfigPeer) Notify(ctx context.Context, method string, params any) error {
	s.mu.Lock()
	s.methods = append(s.methods, method)
	s.params = append(s.params, params)
	s.mu.Unlock()
	if s.notifyFn != nil {
		return s.notifyFn(ctx, method, params)
	}
	return nil
}

func (s *stubConfigPeer) Callback(ctx context.Context, method string, params any, result any) error {
	s.mu.Lock()
	s.callbackMethods = append(s.callbackMethods, method)
	s.callbackParams = append(s.callbackParams, params)
	s.mu.Unlock()
	if typed, ok := result.(*dto.LSPReleaseScopeResult); ok {
		*typed = dto.LSPReleaseScopeResult{MatchedManagers: 1, ClosedManagers: 1, ScopeKeys: []string{"lsp\x00agent-1\x00thread-1"}}
	}
	if s.callbackFn != nil {
		return s.callbackFn(ctx, method, params, result)
	}
	return nil
}

func (s *stubConfigPeer) Close() error { return nil }

func (s *stubConfigPeer) snapshotLast() (string, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.methods) == 0 {
		return "", nil
	}
	return s.methods[len(s.methods)-1], s.params[len(s.params)-1]
}

func (s *stubConfigPeer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.methods)
}

func (s *stubConfigPeer) callbackCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.callbackMethods)
}

func (s *stubConfigPeer) snapshotLastCallback() (string, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.callbackMethods) == 0 {
		return "", nil
	}
	return s.callbackMethods[len(s.callbackMethods)-1], s.callbackParams[len(s.callbackParams)-1]
}

func waitForNotifyCount(t *testing.T, peer *stubConfigPeer, want int) {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if peer.count() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Notify() count = %d, want %d", peer.count(), want)
}

func waitForCallbackCount(t *testing.T, peer *stubConfigPeer, want int) {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if peer.callbackCount() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Callback() count = %d, want %d", peer.callbackCount(), want)
}

func TestNotifyConfigChanged_MapsScopeFromTopic(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "orch-1", Generation: 1}
	peer := &stubConfigPeer{}
	instance := &ToolInstance{
		Lease:         lease,
		Peer:          peer,
		Status:        dto.StatusActive,
		Subscriptions: []string{configTopicAgent},
	}
	registry.instances[lease] = instance
	registry.latestByInstance[lease.InstanceID] = lease
	registry.indexLocked(instance)

	payload := json.RawMessage(`{"event":"agent/launched"}`)
	if err := registry.NotifyConfigChanged(context.Background(), configTopicAgent, nil, 7, payload); err != nil {
		t.Fatalf("NotifyConfigChanged() error = %v", err)
	}

	method, params := peer.snapshotLast()
	if method != dto.MethodConfigChanged {
		t.Fatalf("Notify() method = %q, want %q", method, dto.MethodConfigChanged)
	}
	notify, ok := params.(dto.ConfigChangedNotify)
	if !ok {
		t.Fatalf("Notify() params type = %T, want dto.ConfigChangedNotify", params)
	}
	if notify.Scope != dto.ScopeAgentRuntime {
		t.Fatalf("ConfigChangedNotify.Scope = %q, want %q", notify.Scope, dto.ScopeAgentRuntime)
	}
	if notify.Selector.Subscription != configTopicAgent {
		t.Fatalf("ConfigChangedNotify.Selector.Subscription = %q, want %q", notify.Selector.Subscription, configTopicAgent)
	}
	if notify.Selector.Scope != nil {
		t.Fatalf("ConfigChangedNotify.Selector.Scope = %#v, want nil for direct topic-only notify", notify.Selector.Scope)
	}
	if notify.ConfigVersion != 7 {
		t.Fatalf("ConfigChangedNotify.ConfigVersion = %d, want 7", notify.ConfigVersion)
	}
	if got := string(notify.Payload); got != string(payload) {
		t.Fatalf("ConfigChangedNotify.Payload = %s, want %s", got, string(payload))
	}
}

func TestConfigChangeSubscriptions_BroadcastsAgentAndThreadUpdates(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() {
		_ = dispatcher.Close()
	})

	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "orch-1", Generation: 1}
	peer := &stubConfigPeer{}
	instance := &ToolInstance{
		Lease:         lease,
		AgentID:       "agent-1",
		ThreadID:      "thread-1",
		Peer:          peer,
		Status:        dto.StatusActive,
		Subscriptions: []string{configTopicAgent, configTopicThread},
		ConfigVersion: 1,
	}
	registry.instances[lease] = instance
	registry.latestByInstance[lease.InstanceID] = lease
	registry.indexLocked(instance)

	worker := newConfigFanoutWorker(registry, registry, nil)
	worker.Start()
	cancels := registerConfigChangeSubscriptions(dispatcher, worker, nil)
	t.Cleanup(func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		_ = worker.Stop(stopCtx)
	})

	event.Publish(dispatcher, agentdto.AgentLaunched{
		AgentSessionHeader: shareddto.AgentSessionHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{
					EventHeader: shareddto.EventHeader{Timestamp: time.Now()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			SessionID: "session-1",
		},
		Model: "gpt-5.5",
		CWD:   "/tmp/project",
	})

	waitForNotifyCount(t, peer, 1)
	method, params := peer.snapshotLast()
	if method != dto.MethodConfigChanged {
		t.Fatalf("Notify() method after agent event = %q, want %q", method, dto.MethodConfigChanged)
	}
	agentNotify, ok := params.(dto.ConfigChangedNotify)
	if !ok {
		t.Fatalf("Notify() params after agent event type = %T, want dto.ConfigChangedNotify", params)
	}
	if agentNotify.Scope != dto.ScopeAgentRuntime {
		t.Fatalf("agent scope = %q, want %q", agentNotify.Scope, dto.ScopeAgentRuntime)
	}
	if agentNotify.Selector.Scope == nil {
		t.Fatal("agent selector scope = nil, want populated scope")
	}
	if agentNotify.Selector.Scope.AgentID != "agent-1" {
		t.Fatalf("agent selector scope agent_id = %q, want agent-1", agentNotify.Selector.Scope.AgentID)
	}
	if agentNotify.Selector.Scope.ThreadID != "thread-1" {
		t.Fatalf("agent selector scope thread_id = %q, want thread-1", agentNotify.Selector.Scope.ThreadID)
	}
	if agentNotify.ConfigVersion != 2 {
		t.Fatalf("agent config version = %d, want 2", agentNotify.ConfigVersion)
	}
	if got := registry.instances[lease].ConfigVersion; got != 2 {
		t.Fatalf("registry instance config version after agent event = %d, want 2", got)
	}
	var agentPayload map[string]any
	if err := json.Unmarshal(agentNotify.Payload, &agentPayload); err != nil {
		t.Fatalf("json.Unmarshal(agent payload) error = %v", err)
	}
	if got := agentPayload["event"]; got != "agent/launched" {
		t.Fatalf("agent payload event = %#v, want agent/launched", got)
	}
	if got := agentPayload["agentId"]; got != "agent-1" {
		t.Fatalf("agent payload agentId = %#v, want agent-1", got)
	}

	event.Publish(dispatcher, threaddto.Stopped{
		EventHeader: shareddto.EventHeader{Timestamp: time.Now()},
		ThreadID:    "thread-1",
		AgentID:     "agent-1",
		Status:      "stopped",
		Reason:      "archived",
	})

	waitForNotifyCount(t, peer, 2)
	_, params = peer.snapshotLast()
	threadNotify, ok := params.(dto.ConfigChangedNotify)
	if !ok {
		t.Fatalf("Notify() params after thread event type = %T, want dto.ConfigChangedNotify", params)
	}
	if threadNotify.Scope != dto.ScopeThreadBinding {
		t.Fatalf("thread scope = %q, want %q", threadNotify.Scope, dto.ScopeThreadBinding)
	}
	if threadNotify.Selector.Scope == nil {
		t.Fatal("thread selector scope = nil, want populated scope")
	}
	if threadNotify.Selector.Scope.AgentID != "agent-1" {
		t.Fatalf("thread selector scope agent_id = %q, want agent-1", threadNotify.Selector.Scope.AgentID)
	}
	if threadNotify.Selector.Scope.ThreadID != "thread-1" {
		t.Fatalf("thread selector scope thread_id = %q, want thread-1", threadNotify.Selector.Scope.ThreadID)
	}
	if threadNotify.ConfigVersion != 3 {
		t.Fatalf("thread config version = %d, want 3", threadNotify.ConfigVersion)
	}
	if got := registry.instances[lease].ConfigVersion; got != 3 {
		t.Fatalf("registry instance config version after thread event = %d, want 3", got)
	}
	var threadPayload map[string]any
	if err := json.Unmarshal(threadNotify.Payload, &threadPayload); err != nil {
		t.Fatalf("json.Unmarshal(thread payload) error = %v", err)
	}
	if got := threadPayload["event"]; got != "thread/stopped" {
		t.Fatalf("thread payload event = %#v, want thread/stopped", got)
	}
	if got := threadPayload["reason"]; got != "archived" {
		t.Fatalf("thread payload reason = %#v, want archived", got)
	}
}

func TestAgentLifecycleDispatchesLSPReleaseScopeAdminCall(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() {
		_ = dispatcher.Close()
	})

	registry := NewRegistry()
	lspPeer := &stubConfigPeer{}
	unrelatedPeer := &stubConfigPeer{}
	addIndexedInstance(registry, &ToolInstance{
		Lease:         dto.LeaseKey{InstanceID: "lsp-agent-1", Generation: 1},
		AgentID:       "agent-1",
		ThreadID:      "thread-1",
		ClientKind:    dto.ClientKindLSP,
		PeerKind:      dto.PeerKindTool,
		Status:        dto.StatusActive,
		Subscriptions: []string{configTopicAgent},
		Peer:          lspPeer,
	})
	addIndexedInstance(registry, &ToolInstance{
		Lease:      dto.LeaseKey{InstanceID: "lsp-agent-2", Generation: 1},
		AgentID:    "agent-2",
		ThreadID:   "thread-2",
		ClientKind: dto.ClientKindLSP,
		PeerKind:   dto.PeerKindTool,
		Status:     dto.StatusActive,
		Peer:       unrelatedPeer,
	})

	worker := newConfigFanoutWorker(registry, registry, nil)
	worker.Start()
	cancels := registerConfigChangeSubscriptions(dispatcher, worker, nil)
	t.Cleanup(func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		_ = worker.Stop(stopCtx)
	})

	event.Publish(dispatcher, agentdto.AgentStopped{
		AgentSessionHeader: shareddto.AgentSessionHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{
					EventHeader: shareddto.EventHeader{Timestamp: time.Now()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			SessionID: "session-1",
		},
		Reason: "shutdown",
	})

	waitForCallbackCount(t, lspPeer, 1)
	method, params := lspPeer.snapshotLastCallback()
	if method != dto.MethodLSPReleaseScope {
		t.Fatalf("Callback() method = %q, want %q", method, dto.MethodLSPReleaseScope)
	}
	req, ok := params.(dto.LSPReleaseScopeRequest)
	if !ok {
		t.Fatalf("Callback() params type = %T, want dto.LSPReleaseScopeRequest", params)
	}
	if req.ScopeKind != dto.LSPReleaseScopeAgentAllThreads {
		t.Fatalf("release scope kind = %q, want %q", req.ScopeKind, dto.LSPReleaseScopeAgentAllThreads)
	}
	if req.AgentID != "agent-1" || req.ThreadID != "" {
		t.Fatalf("release scope identity = agent %q thread %q, want agent-1/all threads", req.AgentID, req.ThreadID)
	}
	if !req.Drain {
		t.Fatalf("release scope Drain = false, want true for lifecycle stop")
	}
	if req.Reason != "shutdown" {
		t.Fatalf("release scope Reason = %q, want shutdown", req.Reason)
	}
	if unrelatedPeer.callbackCount() != 0 {
		t.Fatalf("unrelated LSP peer received release callback count=%d, want 0", unrelatedPeer.callbackCount())
	}
}
