package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestToolCallRuntimeConfig_Pin(t *testing.T) {
	t.Parallel()

	fixtures := newToolCallRuntimeFixtures(t)
	for _, tt := range toolCallRuntimeCases(fixtures) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotRuntime, gotOK := tt.handler.toolCallRuntimeConfig(context.Background(), tt.req)
			if gotOK != tt.wantOK {
				t.Fatalf("toolCallRuntimeConfig() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if !reflect.DeepEqual(gotRuntime, tt.wantRuntime) {
				t.Fatalf("toolCallRuntimeConfig() runtime = %#v, want %#v", gotRuntime, tt.wantRuntime)
			}

			if tt.handler == nil {
				return
			}
			assertToolCallBindingCalls(t, tt.handler.bindingStore, tt.wantBindingCalls)
			assertToolCallThreadCalls(t, tt.handler.threadStore, tt.wantThreadCalls)
		})
	}
}

func TestSpawnAgentPolicy_BindingLookupErrorFailsClosed(t *testing.T) {
	f := newToolCallRuntimeFixtures(t)
	h := &Handler{
		bindingStore: &toolCallBindingStoreStub{err: f.resolveErr},
		threadStore:  &toolCallThreadStoreStub{},
	}

	_, err := h.spawnAgentPolicyMessage(context.Background(), ToolCallRequest{
		Name:    "spawn_agent",
		AgentID: "agent-1",
	})
	if err == nil || !strings.Contains(err.Error(), "policy unavailable") {
		t.Fatalf("spawnAgentPolicyMessage() error = %v, want fail-closed policy unavailable error", err)
	}
}

type toolCallRuntimeCase struct {
	name             string
	handler          *Handler
	req              ToolCallRequest
	wantRuntime      map[string]any
	wantOK           bool
	wantBindingCalls []string
	wantThreadCalls  []string
}

type toolCallRuntimeFixtures struct {
	validRuntime map[string]any
	validRaw     []byte
	emptyRaw     []byte
	invalidRaw   []byte
	getErr       error
	resolveErr   error
}

func newToolCallRuntimeFixtures(t *testing.T) toolCallRuntimeFixtures {
	t.Helper()
	validRuntime := map[string]any{
		"runId": "run-1",
		"sessionFlags": map[string]any{
			"persistent_subagent_default": true,
		},
	}
	return toolCallRuntimeFixtures{
		validRuntime: validRuntime,
		validRaw:     mustRawJSON(t, storedThreadRuntime{Runtime: validRuntime}),
		emptyRaw:     mustRawJSON(t, storedThreadRuntime{Runtime: map[string]any{}}),
		invalidRaw:   []byte("{"),
		getErr:       errors.New("get failed"),
		resolveErr:   errors.New("resolve failed"),
	}
}

func toolCallRuntimeCases(f toolCallRuntimeFixtures) []toolCallRuntimeCase {
	cases := toolCallRuntimeLookupFailureCases(f)
	return append(cases, toolCallRuntimeConfigCases(f)...)
}

func toolCallRuntimeLookupFailureCases(f toolCallRuntimeFixtures) []toolCallRuntimeCase {
	return []toolCallRuntimeCase{
		{
			name:        "nil handler without thread id returns false",
			handler:     nil,
			req:         ToolCallRequest{},
			wantRuntime: nil,
		},
		{
			name: "blank thread id without agent id returns false",
			handler: &Handler{
				bindingStore: &toolCallBindingStoreStub{threadID: "thread-from-agent"},
				threadStore:  &toolCallThreadStoreStub{},
			},
			req:              ToolCallRequest{ThreadID: " ", AgentID: "  "},
			wantBindingCalls: nil,
			wantThreadCalls:  nil,
		},
		{
			name: "binding lookup error returns false",
			handler: &Handler{
				bindingStore: &toolCallBindingStoreStub{err: f.resolveErr},
				threadStore:  &toolCallThreadStoreStub{},
			},
			req:              ToolCallRequest{AgentID: " agent-1 "},
			wantBindingCalls: []string{"agent-1"},
			wantThreadCalls:  nil,
		},
		{
			name: "binding lookup resolving blank thread id returns false",
			handler: &Handler{
				bindingStore: &toolCallBindingStoreStub{threadID: "   "},
				threadStore:  &toolCallThreadStoreStub{},
			},
			req:              ToolCallRequest{AgentID: "agent-2"},
			wantBindingCalls: []string{"agent-2"},
			wantThreadCalls:  nil,
		},
		{
			name: "resolved thread id without thread store returns false",
			handler: &Handler{
				bindingStore: &toolCallBindingStoreStub{threadID: "thread-2"},
			},
			req:              ToolCallRequest{AgentID: "agent-3"},
			wantBindingCalls: []string{"agent-3"},
			wantThreadCalls:  nil,
		},
	}
}

func toolCallRuntimeConfigCases(f toolCallRuntimeFixtures) []toolCallRuntimeCase {
	return []toolCallRuntimeCase{
		{
			name: "explicit thread id wins over agent binding lookup",
			handler: &Handler{
				bindingStore: &toolCallBindingStoreStub{threadID: "thread-from-agent"},
				threadStore: &toolCallThreadStoreStub{
					row: &threadstore.Thread{ThreadID: "thread-1", ConfigOverride: f.validRaw},
				},
			},
			req:             ToolCallRequest{ThreadID: " thread-1 ", AgentID: "agent-1"},
			wantRuntime:     f.validRuntime,
			wantOK:          true,
			wantThreadCalls: []string{"thread-1"},
		},
		{
			name: "thread store error returns false",
			handler: &Handler{
				threadStore: &toolCallThreadStoreStub{err: f.getErr},
			},
			req:             ToolCallRequest{ThreadID: "thread-3"},
			wantThreadCalls: []string{"thread-3"},
		},
		{
			name: "missing thread row returns false",
			handler: &Handler{
				threadStore: &toolCallThreadStoreStub{},
			},
			req:             ToolCallRequest{ThreadID: "thread-4"},
			wantThreadCalls: []string{"thread-4"},
		},
		{
			name: "empty config override returns false",
			handler: &Handler{
				threadStore: &toolCallThreadStoreStub{
					row: &threadstore.Thread{ThreadID: "thread-5"},
				},
			},
			req:             ToolCallRequest{ThreadID: "thread-5"},
			wantThreadCalls: []string{"thread-5"},
		},
		{
			name: "invalid config override json returns false",
			handler: &Handler{
				threadStore: &toolCallThreadStoreStub{
					row: &threadstore.Thread{ThreadID: "thread-6", ConfigOverride: f.invalidRaw},
				},
			},
			req:             ToolCallRequest{ThreadID: "thread-6"},
			wantThreadCalls: []string{"thread-6"},
		},
		{
			name: "empty decoded runtime returns false",
			handler: &Handler{
				threadStore: &toolCallThreadStoreStub{
					row: &threadstore.Thread{ThreadID: "thread-7", ConfigOverride: f.emptyRaw},
				},
			},
			req:             ToolCallRequest{ThreadID: "thread-7"},
			wantThreadCalls: []string{"thread-7"},
		},
		{
			name: "resolves thread id from agent binding and returns runtime",
			handler: &Handler{
				bindingStore: &toolCallBindingStoreStub{threadID: " thread-8 "},
				threadStore: &toolCallThreadStoreStub{
					row: &threadstore.Thread{ThreadID: "thread-8", ConfigOverride: f.validRaw},
				},
			},
			req:              ToolCallRequest{AgentID: "agent-8"},
			wantRuntime:      f.validRuntime,
			wantOK:           true,
			wantBindingCalls: []string{"agent-8"},
			wantThreadCalls:  []string{"thread-8"},
		},
	}
}

func assertToolCallBindingCalls(t *testing.T, store agentThreadLookup, want []string) {
	t.Helper()
	if store == nil {
		if len(want) != 0 {
			t.Fatalf("binding store calls = nil, want %#v", want)
		}
		return
	}
	stub, ok := store.(*toolCallBindingStoreStub)
	if !ok {
		t.Fatalf("binding store type = %T, want *toolCallBindingStoreStub", store)
	}
	if !reflect.DeepEqual(stub.agentIDs, want) {
		t.Fatalf("binding store calls = %#v, want %#v", stub.agentIDs, want)
	}
}

func assertToolCallThreadCalls(t *testing.T, store threadConfigOverrideStore, want []string) {
	t.Helper()
	if store == nil {
		if len(want) != 0 {
			t.Fatalf("thread store calls = nil, want %#v", want)
		}
		return
	}
	stub, ok := store.(*toolCallThreadStoreStub)
	if !ok {
		t.Fatalf("thread store type = %T, want *toolCallThreadStoreStub", store)
	}
	if !reflect.DeepEqual(stub.threadIDs, want) {
		t.Fatalf("thread store calls = %#v, want %#v", stub.threadIDs, want)
	}
}

type toolCallBindingStoreStub struct {
	toolCallBindingStoreReadNoop
	toolCallBindingStoreWriteNoop

	threadID           string
	err                error
	agentIDs           []string
	bindingsByAgent    map[string]toolCallBinding
	bindingsByProvider map[string]toolCallBinding
}

type toolCallBindingStoreReadNoop struct{}

func (toolCallBindingStoreReadNoop) GetByProviderThread(context.Context, string, string) (*bindingstore.Binding, error) {
	return nil, nil
}

func (toolCallBindingStoreReadNoop) GetByAgentID(context.Context, string) (*bindingstore.Binding, error) {
	return nil, nil
}

func (toolCallBindingStoreReadNoop) ListAgentThreadBindings(context.Context) ([]bindingstore.Binding, error) {
	return nil, nil
}

type toolCallBindingStoreWriteNoop struct{}

func (toolCallBindingStoreWriteNoop) Upsert(context.Context, bindingstore.UpsertParams) error {
	return nil
}

func (toolCallBindingStoreWriteNoop) DeleteByAgentID(context.Context, string) error { return nil }

func (toolCallBindingStoreWriteNoop) UpdateSessionUUID(context.Context, bindingstore.UpdateSessionUUIDParams) error {
	return nil
}
func (toolCallBindingStoreWriteNoop) UpdateProviderThreadID(context.Context, bindingstore.UpdateProviderThreadIDParams) error {
	return nil
}

func (toolCallBindingStoreWriteNoop) SetArchived(context.Context, bindingstore.SetArchivedParams) error {
	return nil
}

func (toolCallBindingStoreWriteNoop) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}

func (toolCallBindingStoreWriteNoop) UnbindAgentThread(context.Context, string) error { return nil }

func (s *toolCallBindingStoreStub) GetThreadByAgent(_ context.Context, agentID string) (string, error) {
	s.agentIDs = append(s.agentIDs, agentID)
	return s.threadID, s.err
}

func (toolCallBindingStoreWriteNoop) UpdateAgentCwd(context.Context, bindingstore.UpdateAgentCwdParams) error {
	return nil
}

func (s *toolCallBindingStoreStub) GetBindingByAgent(_ context.Context, agentID string) (toolCallBinding, error) {
	if s.err != nil {
		return toolCallBinding{}, s.err
	}
	if s.bindingsByAgent == nil {
		return toolCallBinding{}, nil
	}
	return s.bindingsByAgent[agentID], nil
}

func (s *toolCallBindingStoreStub) GetBindingByProviderThread(_ context.Context, provider, providerThreadID string) (toolCallBinding, error) {
	if s.err != nil {
		return toolCallBinding{}, s.err
	}
	if s.bindingsByProvider == nil {
		return toolCallBinding{}, nil
	}
	return s.bindingsByProvider[provider+":"+providerThreadID], nil
}

// toolCallThreadStoreStub satisfies the narrow threadConfigOverrideStore
// port from ports.go. Fixtures still construct a *threadstore.Thread so
// the ConfigOverride bytes stay typed, but the stub only exposes the
// narrow method the handler actually calls; the caller may set row to
// nil to simulate "no thread row stored".
type toolCallThreadStoreStub struct {
	row       *threadstore.Thread
	err       error
	threadIDs []string
}

func (s *toolCallThreadStoreStub) GetConfigOverride(_ context.Context, threadID string) (json.RawMessage, error) {
	s.threadIDs = append(s.threadIDs, threadID)
	if s.err != nil {
		return nil, s.err
	}
	if s.row == nil {
		return nil, nil
	}
	return s.row.ConfigOverride, nil
}
