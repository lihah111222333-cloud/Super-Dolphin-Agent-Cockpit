package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

type failFastBindingStore struct {
	stubBindingStore
	agentErr      map[string]error
	providerErr   map[string]error
	providerCalls []string
	binding       *BindingRecord
}

func (s *failFastBindingStore) GetByAgentID(_ context.Context, agentID string) (*BindingRecord, error) {
	if err := s.agentErr[agentID]; err != nil {
		return nil, err
	}
	if s.binding != nil && s.binding.AgentID == agentID {
		binding := *s.binding
		return &binding, nil
	}
	return nil, platformdb.ErrNotFound
}

func (s *failFastBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*BindingRecord, error) {
	s.providerCalls = append(s.providerCalls, provider)
	if err := s.providerErr[provider]; err != nil {
		return nil, err
	}
	if s.binding != nil && s.binding.Provider == provider && s.binding.ProviderThreadID == providerThreadID {
		binding := *s.binding
		return &binding, nil
	}
	return nil, platformdb.ErrNotFound
}

func TestResolveBindingChainFailsFastOnPrimaryLookupError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("binding store unavailable")
	store := &failFastBindingStore{
		agentErr: map[string]error{"thread-1": storeErr},
		binding:  &BindingRecord{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1"},
	}
	svc := &service{bindingStore: store}

	binding, err := svc.resolveBindingChain(context.Background(), "thread-1")
	if !errors.Is(err, storeErr) {
		t.Fatalf("resolveBindingChain() error = %v, want %v", err, storeErr)
	}
	if binding != nil {
		t.Fatalf("resolveBindingChain() binding = %#v, want nil", binding)
	}
	if len(store.providerCalls) != 0 {
		t.Fatalf("provider fallback calls = %v, want none after primary lookup error", store.providerCalls)
	}
}

func TestResolveBindingChainFailsFastOnRememberedAgentLookupError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("remembered binding lookup failed")
	store := &failFastBindingStore{
		agentErr: map[string]error{
			"thread-1": platformdb.ErrNotFound,
			"agent-1":  storeErr,
		},
		binding: &BindingRecord{AgentID: "fallback-agent", Provider: "codex", ProviderThreadID: "thread-1"},
	}
	svc := &service{bindingStore: store}
	svc.rememberThreadAgent("thread-1", "agent-1")

	binding, err := svc.resolveBindingChain(context.Background(), "thread-1")
	if !errors.Is(err, storeErr) {
		t.Fatalf("resolveBindingChain() error = %v, want %v", err, storeErr)
	}
	if binding != nil {
		t.Fatalf("resolveBindingChain() binding = %#v, want nil", binding)
	}
}

func TestResolveBindingChainFailsFastOnPersistedAgentLookupError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("thread store unavailable")
	store := &failFastBindingStore{
		agentErr: map[string]error{"thread-1": platformdb.ErrNotFound},
		binding:  &BindingRecord{AgentID: "fallback-agent", Provider: "codex", ProviderThreadID: "thread-1"},
	}
	svc := &service{bindingStore: store, threadStore: &stubThreadStore{getErr: storeErr}}

	binding, err := svc.resolveBindingChain(context.Background(), "thread-1")
	if !errors.Is(err, storeErr) {
		t.Fatalf("resolveBindingChain() error = %v, want %v", err, storeErr)
	}
	if binding != nil {
		t.Fatalf("resolveBindingChain() binding = %#v, want nil", binding)
	}
}

func TestResolveBindingChainFallsBackToProviderThreadWhenPersistedThreadMissing(t *testing.T) {
	t.Parallel()

	threadID := "thread-1"
	want := &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: threadID,
	}
	store := &failFastBindingStore{
		agentErr: map[string]error{threadID: platformdb.ErrNotFound},
		binding:  want,
	}
	svc := &service{
		bindingStore: store,
		threadStore:  &stubThreadStore{threadByID: map[string]*ThreadRecord{}},
	}

	binding, err := svc.resolveBindingChain(context.Background(), threadID)
	if err != nil {
		t.Fatalf("resolveBindingChain() error = %v, want nil", err)
	}
	if binding == nil || binding.AgentID != want.AgentID || binding.ProviderThreadID != want.ProviderThreadID {
		t.Fatalf("resolveBindingChain() binding = %#v, want %#v", binding, want)
	}
	if len(store.providerCalls) != 1 || store.providerCalls[0] != "codex" {
		t.Fatalf("provider fallback calls = %v, want [codex]", store.providerCalls)
	}
}

func TestResolveBindingChainMissingPersistedThreadReturnsSemanticNotFoundWithoutBindingDetails(t *testing.T) {
	t.Parallel()

	threadID := "thread-1"
	store := &failFastBindingStore{
		agentErr: map[string]error{
			threadID:  platformdb.WrapStoreError(platformdb.ErrNotFound, "get_by_agent_id", "binding"),
			"agent-1": platformdb.WrapStoreError(platformdb.ErrNotFound, "get_by_agent_id", "binding"),
		},
	}
	svc := &service{
		bindingStore: store,
		threadStore:  &stubThreadStore{threadByID: map[string]*ThreadRecord{}},
	}
	svc.rememberThreadAgent(threadID, "agent-1")

	binding, err := svc.resolveBindingChain(context.Background(), threadID)
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("resolveBindingChain() error = %v, want not found", err)
	}
	if binding != nil {
		t.Fatalf("resolveBindingChain() binding = %#v, want nil", binding)
	}
	text := err.Error()
	if !strings.Contains(text, threadID) || !strings.Contains(text, "not found") {
		t.Fatalf("resolveBindingChain() error = %q, want thread id and not found", text)
	}
	if strings.Contains(text, "get_by_agent_id") || strings.Contains(text, "binding") {
		t.Fatalf("resolveBindingChain() error = %q, want semantic thread not found without binding store details", text)
	}
}

func TestResolveBindingChainFailsFastWhenPersistedAgentBindingMissing(t *testing.T) {
	t.Parallel()

	threadID := "thread-1"
	store := &failFastBindingStore{
		agentErr: map[string]error{
			threadID:  platformdb.ErrNotFound,
			"agent-1": platformdb.ErrNotFound,
		},
		binding: &BindingRecord{
			AgentID:          "fallback-agent",
			Provider:         "codex",
			ProviderThreadID: threadID,
		},
	}
	svc := &service{
		bindingStore: store,
		threadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: threadID,
			AgentID:  "agent-1",
		}},
	}

	binding, err := svc.resolveBindingChain(context.Background(), threadID)
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("resolveBindingChain() error = %v, want not found", err)
	}
	if binding != nil {
		t.Fatalf("resolveBindingChain() binding = %#v, want nil", binding)
	}
	if !strings.Contains(err.Error(), "binding for resolved agent_id") {
		t.Fatalf("resolveBindingChain() error = %q, want resolved agent binding not found", err.Error())
	}
	if len(store.providerCalls) != 0 {
		t.Fatalf("provider fallback calls = %v, want none after persisted agent binding miss", store.providerCalls)
	}
}

func TestResolveBindingChainFailsFastOnProviderLookupError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("provider binding lookup failed")
	store := &failFastBindingStore{
		agentErr:    map[string]error{"thread-1": platformdb.ErrNotFound},
		providerErr: map[string]error{"codex": storeErr},
	}
	svc := &service{bindingStore: store}

	binding, err := svc.resolveBindingChain(context.Background(), "thread-1")
	if !errors.Is(err, storeErr) {
		t.Fatalf("resolveBindingChain() error = %v, want %v", err, storeErr)
	}
	if binding != nil {
		t.Fatalf("resolveBindingChain() binding = %#v, want nil", binding)
	}
	if len(store.providerCalls) != 1 || store.providerCalls[0] != "codex" {
		t.Fatalf("provider fallback calls = %v, want [codex]", store.providerCalls)
	}
}

func TestBuildOfflineConfigFailsFastOnMalformedConfigOverride(t *testing.T) {
	t.Parallel()

	svc := &service{
		threadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID:       "thread-1",
			AgentID:        "agent-1",
			ConfigOverride: json.RawMessage("{"),
		}},
	}

	_, err := svc.buildOfflineConfig(context.Background(), "thread-1", &BindingRecord{Provider: "codex"})
	if err == nil || !strings.Contains(err.Error(), "decode thread config override") {
		t.Fatalf("buildOfflineConfig() error = %v, want config decode failure", err)
	}
}

func TestPersistThreadConfigFailsFastOnMalformedConfigOverride(t *testing.T) {
	t.Parallel()

	model := "gpt-5"
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		ConfigOverride: json.RawMessage("{"),
	}}
	svc := &service{threadStore: threads}

	err := svc.persistThreadConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Model: &model}, dto.ThreadConfig{})
	if err == nil || !strings.Contains(err.Error(), "decode thread config override") {
		t.Fatalf("persistThreadConfig() error = %v, want config decode failure", err)
	}
	if threads.upsert.ThreadID != "" {
		t.Fatalf("thread was upserted despite malformed config: %#v", threads.upsert)
	}
}

func TestPersistThreadConfigFailsWhenThreadStoreMissing(t *testing.T) {
	t.Parallel()

	model := "gpt-5"
	svc := &service{}

	err := svc.persistThreadConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Model: &model}, dto.ThreadConfig{})

	if err == nil || !strings.Contains(err.Error(), "thread store is not configured") {
		t.Fatalf("persistThreadConfig() error = %v, want thread store not configured", err)
	}
}

func TestConfigReadsFailFastWithoutSessionProvider(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID: "thread-1",
		AgentID:  "agent-1",
		Model:    "gpt-5.5",
	}}
	svc := NewService(silentLogger(), threads, testCodexBindingStore(), nil, nil, nil, nil, nil).(*service)

	_, err := svc.GetConfig(context.Background(), "thread-1")
	if err == nil || !strings.Contains(err.Error(), "session provider is not configured") {
		t.Fatalf("GetConfig() error = %v, want session provider not configured", err)
	}

	_, err = svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err == nil || !strings.Contains(err.Error(), "session provider is not configured") {
		t.Fatalf("ReadRuntimeConfig() error = %v, want session provider not configured", err)
	}
}

func TestReadRuntimeConfigsFailsFastOnMalformedConfigOverride(t *testing.T) {
	t.Parallel()

	svc := &service{threadStore: &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		ConfigOverride: json.RawMessage("{"),
	}}}

	_, err := svc.ReadRuntimeConfigs(context.Background(), []string{"thread-1"})
	if err == nil || !strings.Contains(err.Error(), "decode thread config override") {
		t.Fatalf("ReadRuntimeConfigs() error = %v, want config decode failure", err)
	}
}

func TestReadRuntimeConfigsFailsFastOnMissingThread(t *testing.T) {
	t.Parallel()

	svc := &service{threadStore: &stubThreadStore{threadByID: map[string]*ThreadRecord{}}}
	_, err := svc.ReadRuntimeConfigs(context.Background(), []string{"thread-missing"})
	if err == nil || !strings.Contains(err.Error(), "thread-missing") {
		t.Fatalf("ReadRuntimeConfigs() error = %v, want missing thread failure", err)
	}
}

func TestReadMessagesFailsFastOnPendingLaunchLookupError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("thread store unavailable")
	svc := &service{threadStore: &stubThreadStore{getErr: storeErr}}
	_, err := svc.ReadMessages(context.Background(), "thread-1", 0, "")
	if !errors.Is(err, storeErr) {
		t.Fatalf("ReadMessages() error = %v, want %v", err, storeErr)
	}
}

func TestReadRuntimeConfigsFailsFastOnMissingSessionForBinding(t *testing.T) {
	t.Parallel()

	thread := ThreadRecord{
		ThreadID: "thread-1",
		AgentID:  "agent-1",
		Model:    "gpt-5.5",
		Cwd:      "/tmp/demo",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Approvals:   "never",
			Personality: "balanced",
		}),
	}
	svc := &service{
		threadStore: &stubThreadStore{thread: &thread},
		bindingStore: &stubBindingStore{binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
		}},
		sessions: &stubSessionProvider{},
	}

	got, err := svc.ReadRuntimeConfigs(context.Background(), []string{"thread-1"})
	if err != nil {
		t.Fatalf("ReadRuntimeConfigs() error = %v, want offline runtime config", err)
	}
	runtime := got["thread-1"]
	if runtime["approvalPolicy"] != "never" || runtime["personality"] != "balanced" || runtime["model"] != "gpt-5.5" || runtime["cwd"] != "/tmp/demo" {
		t.Fatalf("ReadRuntimeConfigs()[thread-1] = %#v", runtime)
	}
}

func TestReadRuntimeConfigsFailsFastOnMissingBindingForPersistedAgent(t *testing.T) {
	t.Parallel()

	thread := ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1"}
	svc := &service{
		threadStore:  &stubThreadStore{thread: &thread},
		bindingStore: &stubBindingStore{},
	}

	_, err := svc.ReadRuntimeConfigs(context.Background(), []string{"thread-1"})
	if err == nil || !strings.Contains(err.Error(), "binding missing") {
		t.Fatalf("ReadRuntimeConfigs() error = %v, want missing binding failure", err)
	}
}

func TestReadThreadStateRuntimeConfigFailsFastOnBindingLookupError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("binding lookup failed")
	svc := &service{
		threadStore: &stubThreadStore{thread: &ThreadRecord{ThreadID: "thread-1"}},
		bindingStore: &failFastBindingStore{
			agentErr: map[string]error{"thread-1": storeErr},
		},
	}

	_, err := svc.ReadThreadStateRuntimeConfig(context.Background(), "thread-1")
	if !errors.Is(err, storeErr) {
		t.Fatalf("ReadThreadStateRuntimeConfig() error = %v, want %v", err, storeErr)
	}
}

func TestReadThreadStateRuntimeConfigIncludesPersistedCWD(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		thread  *ThreadRecord
		binding *BindingRecord
		want    string
	}{
		{
			name:    "thread cwd",
			thread:  &ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Cwd: "/thread/worktree"},
			binding: &BindingRecord{AgentID: "agent-1", Cwd: "/thread/worktree"},
			want:    "/thread/worktree",
		},
		{
			name:    "binding cwd",
			thread:  &ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1"},
			binding: &BindingRecord{AgentID: "agent-1", Cwd: "/binding/worktree"},
			want:    "/binding/worktree",
		},
		{
			name:    "thread cwd overrides empty runtime cwd",
			thread:  &ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Cwd: "/thread/worktree", ConfigOverride: json.RawMessage(`{"runtime":{"cwd":""}}`)},
			binding: &BindingRecord{AgentID: "agent-1", Cwd: "/thread/worktree"},
			want:    "/thread/worktree",
		},
		{
			name:    "binding cwd overrides stale runtime cwd",
			thread:  &ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", ConfigOverride: json.RawMessage(`{"runtime":{"cwd":"/stale/worktree"}}`)},
			binding: &BindingRecord{AgentID: "agent-1", Cwd: "/binding/worktree"},
			want:    "/binding/worktree",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{threadStore: &stubThreadStore{thread: tc.thread}, bindingStore: &stubBindingStore{binding: tc.binding}}
			got, err := svc.ReadThreadStateRuntimeConfig(context.Background(), "thread-1")
			if err != nil {
				t.Fatalf("ReadThreadStateRuntimeConfig() error = %v", err)
			}
			if got["cwd"] != tc.want {
				t.Fatalf("ReadThreadStateRuntimeConfig()[cwd] = %v, want %q; runtime=%#v", got["cwd"], tc.want, got)
			}
		})
	}
}

func TestPersistResumedSessionFailsFastOnPersistError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("thread store write failed")
	svc := &service{
		logger:      silentLogger(),
		threadStore: &stubThreadStore{upsertErr: storeErr},
	}

	_, err := svc.persistResumedSession(
		context.Background(),
		ResumeRequest{ThreadID: "thread-1", AgentID: "agent-1", Provider: "codex", CWD: wantStartCWD(t)},
		resumeState{PublicThreadID: "thread-1", ProviderRecoveryHome: t.TempDir(), CWD: wantStartCWD(t)},
		"resumed thread",
		&stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b9"},
	)
	if !errors.Is(err, storeErr) {
		t.Fatalf("persistResumedSession() error = %v, want %v", err, storeErr)
	}
}

func mustDecodeStoredThreadConfig(t testing.TB, raw json.RawMessage) storedThreadConfig {
	t.Helper()
	cfg, err := decodeStoredThreadConfig(raw)
	if err != nil {
		t.Fatalf("decodeStoredThreadConfig() error = %v", err)
	}
	return cfg
}
