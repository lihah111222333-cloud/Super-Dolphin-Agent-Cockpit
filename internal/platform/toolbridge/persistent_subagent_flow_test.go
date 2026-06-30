package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threadmod "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// testThreadConfigOverrideStore 把完整 threadstore.Store 收窄成 handler 只需要的 ConfigOverride 读取口。
// 测试用这个适配器确保 toolbridge 不依赖 thread store 的其他写入能力。
type testThreadConfigOverrideStore struct {
	inner threadstore.Store
}

func (a testThreadConfigOverrideStore) GetConfigOverride(ctx context.Context, threadID string) (json.RawMessage, error) {
	if a.inner == nil {
		return nil, nil
	}
	row, err := a.inner.GetByThreadID(ctx, threadID)
	if err != nil || row == nil {
		return nil, err
	}
	return row.ConfigOverride, nil
}

type persistentFlowThreadStore struct {
	threadstore.Store
	row *threadstore.Thread
}

func (s *persistentFlowThreadStore) GetByThreadID(_ context.Context, threadID string) (*threadstore.Thread, error) {
	if s.row == nil || s.row.ThreadID != threadID {
		return nil, platformdb.ErrNotFound
	}
	return s.row, nil
}

func (s *persistentFlowThreadStore) Exists(_ context.Context, threadID string) (bool, error) {
	return s.row != nil && s.row.ThreadID == threadID, nil
}

func (s *persistentFlowThreadStore) Upsert(_ context.Context, params threadstore.UpsertParams) error {
	s.row = &threadstore.Thread{
		ThreadID:         params.ThreadID,
		AgentID:          params.ThreadID,
		ParentAgentID:    params.ParentAgentID,
		AgentType:        params.AgentType,
		AgentMemoryScope: params.AgentMemoryScope,
		Prompt:           params.Prompt,
		Model:            params.Model,
		Cwd:              params.Cwd,
		Status:           params.Status,
		CreatedAt:        params.CreatedAt,
		UpdatedAt:        params.UpdatedAt,
		OwnerThreadID:    params.OwnerThreadID,
		ConfigOverride:   params.ConfigOverride,
		AgentKey:         params.AgentKey,
		PromptVersionID:  params.PromptVersionID,
		PendingLaunch:    params.PendingLaunch,
	}
	return nil
}

func (*persistentFlowThreadStore) SavePromptSnapshot(context.Context, string, threadstore.PromptSnapshot) error {
	return nil
}

type persistentFlowSessions struct {
	byAgent map[string]contract.Session
}

func (s *persistentFlowSessions) GetSession(agentID string) (contract.Session, error) {
	session, ok := s.byAgent[agentID]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	return session, nil
}

func (s *persistentFlowSessions) RemoveSession(agentID string) {
	delete(s.byAgent, agentID)
}

type persistentFlowStarter struct {
	sessions *persistentFlowSessions
	captured dto.StartSessionRequest
}

func (s *persistentFlowStarter) StartSession(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	s.captured = req
	session := &persistentFlowSession{
		threadID: "123e4567-e89b-12d3-a456-426614174000",
		rollout:  "/tmp/rollout",
		runtime:  map[string]any{"cwd": req.CWD},
	}
	s.sessions.byAgent[req.AgentID] = session
	return session, nil
}

func (*persistentFlowStarter) ResumeSession(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	return nil, errors.New("unexpected resume")
}

type persistentFlowOrchestration struct{}

func (*persistentFlowOrchestration) LaunchAgent(context.Context, threadmod.LaunchAgentRequest) error {
	return nil
}

func (*persistentFlowOrchestration) StopAgent(context.Context, string) error { return nil }

func (*persistentFlowOrchestration) Recover(context.Context, string) error { return nil }

func (*persistentFlowOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}

type persistentFlowSession struct {
	contract.Session
	threadID string
	rollout  string
	runtime  map[string]any
}

func (s *persistentFlowSession) ThreadID() string                      { return s.threadID }
func (s *persistentFlowSession) RolloutPath() string                   { return s.rollout }
func (s *persistentFlowSession) Capabilities() dto.CapabilitySet       { return nil }
func (s *persistentFlowSession) Close(context.Context) error           { return nil }
func (s *persistentFlowSession) ForceStop() error                      { return nil }
func (s *persistentFlowSession) RuntimeConfigSnapshot() map[string]any { return s.runtime }

func decodePersistentFlowRuntime(raw json.RawMessage) (map[string]any, error) {
	var stored struct {
		Runtime map[string]any `json:"runtime,omitempty"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	return stored.Runtime, nil
}

func runtimeStrings(runtime map[string]any, key string) []string {
	raw, ok := runtime[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			text, _ := value.(string)
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func TestPersistentSubagentDefaultFlow_StartFiltersSpawnAgentAndToolbridgeBlocksIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := &persistentFlowThreadStore{}
	sessions := &persistentFlowSessions{byAgent: map[string]contract.Session{}}
	starter := &persistentFlowStarter{sessions: sessions}
	cfg := &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}
	service := threadmod.NewServiceWithPromptAssemblyAndSharedFiles(nil, store, nil, persistentFlowSharedFiles{}, sessions, starter, nil, &persistentFlowOrchestration{}, nil, &persistentFlowPromptAssembly{}, cfg, nil, nil, nil, nil, nil, nil)

	result, err := service.Start(ctx, threadmod.StartRequest{
		AgentID:       "agent-child-persistent",
		Provider:      "codex",
		CWD:           t.TempDir(),
		Name:          "worker-agent",
		ParentAgentID: "agent-main",
		EnabledTools:  []string{"spawn_agent", "orchestration_launch_agent", "request_user_input"},
		Config: map[string]any{
			contract.CodexHomeKey:          t.TempDir(),
			contract.CodexInstanceKeyKey:   "default",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.row == nil {
		t.Fatal("thread store row = nil")
	}

	startTools, ok := starter.captured.Config["enabledTools"].([]string)
	if !ok {
		t.Fatalf("StartSessionRequest.Config.enabledTools = %#v, want []string", starter.captured.Config["enabledTools"])
	}
	if got, want := fmt.Sprintf("%#v", startTools), `[]string{"orchestration_launch_agent", "request_user_input"}`; got != want {
		t.Fatalf("StartSessionRequest.Config.enabledTools = %s, want %s", got, want)
	}

	runtime, err := decodePersistentFlowRuntime(store.row.ConfigOverride)
	if err != nil {
		t.Fatalf("decode runtime config error = %v", err)
	}
	if got, want := fmt.Sprintf("%#v", runtimeStrings(runtime, "enabledTools")), `[]string{"orchestration_launch_agent", "request_user_input"}`; got != want {
		t.Fatalf("stored runtime enabledTools = %s, want %s", got, want)
	}

	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when spawn_agent is blocked")
		return nil
	}}})
	// handler 只需要读取 ConfigOverride，因此这里用窄口适配器包住完整 thread store fixture。
	h.threadStore = testThreadConfigOverrideStore{inner: store}
	h.cfg = cfg

	toolResult, err := h.routeToolCall(ctx, ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
		ThreadID:  result.ThreadID,
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, toolResult, persistentSubagentDefaultBlockText, false)
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

type persistentFlowSharedFiles struct{}

type persistentFlowPromptAssembly struct{}

func (*persistentFlowPromptAssembly) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (*persistentFlowPromptAssembly) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (*persistentFlowPromptAssembly) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (*persistentFlowPromptAssembly) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

func (persistentFlowSharedFiles) Get(context.Context, string) (*sharedfilestore.SharedFile, error) {
	return nil, platformdb.ErrNotFound
}

func (persistentFlowSharedFiles) List(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

func (persistentFlowSharedFiles) Upsert(_ context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	return &sharedfilestore.SharedFile{Path: params.Path, Content: params.Content, UpdatedBy: params.UpdatedBy}, nil
}

func (persistentFlowSharedFiles) Delete(context.Context, string) (int64, error) {
	return 1, nil
}
