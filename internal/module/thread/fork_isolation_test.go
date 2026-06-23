package thread

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type forkPromptAssemblyStub struct {
	invalidated []contract.InvalidateReason
}

func (s *forkPromptAssemblyStub) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (s *forkPromptAssemblyStub) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (s *forkPromptAssemblyStub) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (s *forkPromptAssemblyStub) Invalidate(_ context.Context, reason contract.InvalidateReason) error {
	s.invalidated = append(s.invalidated, reason)
	return nil
}

func forkPromptGitFixture(t *testing.T) (string, string) {
	t.Helper()
	repoRoot := t.TempDir()
	worktreeRoot := filepath.Join(repoRoot, "worktree")
	gitDir := filepath.Join(repoRoot, ".git", "worktrees", "feature")
	if err := os.MkdirAll(filepath.Join(worktreeRoot, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir gitdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeRoot, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatalf("write worktree .git: %v", err)
	}
	return repoRoot, filepath.Join(worktreeRoot, "pkg")
}

func TestServiceForkCreatesIndependentAgentAndBinding(t *testing.T) {
	t.Parallel()

	fixture := newForkServiceFixture(t)
	result, err := fixture.svc.Fork(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	assertForkResult(t, result, fixture)
}

func TestServiceForkUsesRecoverableSessionUUIDWhenProviderThreadIDMissing(t *testing.T) {
	t.Parallel()

	const parentUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	originalSession := &stubSession{
		threadID:   parentUUID,
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:       "agent-parent",
		Provider:      "codex",
		CodexThreadID: "agent-parent",
		RolloutPath:   writeExistingProviderHistoryFile(t),
		SessionUUID:   parentUUID,
		Cwd:           "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "agent-parent",
		AgentID:   "agent-parent",
		Prompt:    "Forked Thread",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &forkOrchestrationStub{}, nil).(*service)

	if _, err := svc.Fork(context.Background(), "agent-parent"); err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if originalSession.forkRequest.ThreadID != parentUUID {
		t.Fatalf("forkRequest.ThreadID = %q, want recoverable session uuid %s", originalSession.forkRequest.ThreadID, parentUUID)
	}
}

func TestServiceForkRejectsMissingCWDBeforeProviderOrchestrationSideEffects(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "thread-parent",
		CodexThreadID:    "thread-parent",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		Prompt:    "Forked Thread",
		Model:     "gpt-5.5",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when fork cwd is missing")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Fork(context.Background(), "thread-parent")
	if err == nil || !strings.Contains(err.Error(), "fork cwd is required") {
		t.Fatalf("Fork() error = %v, want cwd required", err)
	}
	if originalSession.forkRequest.ThreadID != "" {
		t.Fatalf("ForkThread request = %#v, want no provider fork before cwd validation", originalSession.forkRequest)
	}
	if orch.launch.AgentID != "" {
		t.Fatalf("orchestration launch = %#v, want none", orch.launch)
	}
}

type forkServiceFixture struct {
	originalSession *stubSession
	bindings        *stubBindingStore
	threads         *stubThreadStore
	orch            *forkOrchestrationStub
	svc             *service
}

func newForkServiceFixture(t *testing.T) *forkServiceFixture {
	t.Helper()
	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	threads := forkParentThreadStore()
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		assertForkResumeRequest(t, req)
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	orch := &forkOrchestrationStub{}
	return &forkServiceFixture{
		originalSession: originalSession,
		bindings:        bindings,
		threads:         threads,
		orch:            orch,
		svc:             NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service),
	}
}

func forkParentBindingStore() *stubBindingStore {
	return &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "thread-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
}

func forkParentThreadStore() *stubThreadStore {
	return &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		Prompt:    "Forked Thread",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
	}, promptSnapshot: &threadstore.PromptSnapshot{
		DisplayName:           "Forked Thread",
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  promptSnapshotHash("Forked Thread", "stored base", "stored dev", "codex", nil),
	}}
}

func assertForkResumeRequest(t *testing.T, req dto.ResumeSessionRequest) {
	t.Helper()
	if req.Provider != "codex" || req.AgentID != "thread-fork" || req.ThreadID != "thread-fork" {
		t.Fatalf("ResumeSession request = %#v", req)
	}
	if req.Model != "gpt-5.5" {
		t.Fatalf("Model = %q, want gpt-5.5", req.Model)
	}
	if req.PromptSnapshot.BaseInstructions != "stored base" || req.PromptSnapshot.DeveloperInstructions != "stored dev" {
		t.Fatalf("PromptSnapshot = %#v, want stored snapshot", req.PromptSnapshot)
	}
}

func assertForkResult(t *testing.T, result ForkResult, fixture *forkServiceFixture) {
	t.Helper()
	if result.NewThreadID != "thread-fork" || result.ForkedFrom != "thread-parent" {
		t.Fatalf("Fork() result = %#v, want thread-fork", result)
	}
	assertForkSessionAndLaunch(t, fixture)
	assertForkPersistence(t, fixture.bindings, fixture.threads)
}

func assertForkSessionAndLaunch(t *testing.T, fixture *forkServiceFixture) {
	t.Helper()
	if fixture.originalSession.forkRequest.ThreadID != "thread-parent" {
		t.Fatalf("forkRequest.ThreadID = %q, want thread-parent", fixture.originalSession.forkRequest.ThreadID)
	}
	if fixture.orch.launch.AgentID != "thread-fork" {
		t.Fatalf("launch.AgentID = %q, want thread-fork", fixture.orch.launch.AgentID)
	}
	if fixture.orch.launch.Cwd != "/repo" || fixture.orch.launch.Name != "Forked Thread (续)" {
		t.Fatalf("launch = %#v", fixture.orch.launch)
	}
}

func assertForkPersistence(t *testing.T, bindings *stubBindingStore, threads *stubThreadStore) {
	t.Helper()
	if bindings.upsert.AgentID != "thread-fork" {
		t.Fatalf("binding.AgentID = %q, want thread-fork", bindings.upsert.AgentID)
	}
	if bindings.upsert.ProviderThreadID != "019d5f6b-aaaa-7760-9d6f-54005553f5b3" || bindings.upsert.CodexThreadID != "thread-fork" {
		t.Fatalf("binding upsert = %#v", bindings.upsert)
	}
	if threads.upsert.ThreadID != "thread-fork" || threads.upsert.OwnerThreadID != "thread-parent" {
		t.Fatalf("thread upsert = %#v", threads.upsert)
	}
	if threads.upsert.Prompt != "Forked Thread (续)" {
		t.Fatalf("persisted prompt = %q, want Forked Thread (续)", threads.upsert.Prompt)
	}
}

func TestServiceRecoverReturnsResumeEnvelopeWhenSessionMissing(t *testing.T) {
	t.Parallel()

	fixture := newResumeRecoverFixture(t)
	result, err := fixture.svc.Recover(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	assertResumeRecoverResult(t, result, fixture)
}

func TestServiceRecoverFallbackLaunchPreservesStoredProviderAndModel(t *testing.T) {
	t.Parallel()

	fixture := newResumeRecoverFixture(t)
	fixture.orch.recoverErr = contract.ErrAgentNotFound

	result, err := fixture.svc.Recover(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	assertResumeRecoverResult(t, result, fixture)
	if fixture.orch.launch.AgentID != "agent-parent" || fixture.orch.launch.Cwd != "/repo" {
		t.Fatalf("fallback launch = %#v, want agent-parent in /repo", fixture.orch.launch)
	}
	if gotProvider := launchEnvValue(fixture.orch.launch.Env, "AGENT_PROVIDER"); gotProvider != "codex" {
		t.Fatalf("fallback launch AGENT_PROVIDER = %q, want codex; env=%v", gotProvider, fixture.orch.launch.Env)
	}
	if gotModel := launchEnvValue(fixture.orch.launch.Env, "AGENT_MODEL"); gotModel != "gpt-5.5" {
		t.Fatalf("fallback launch AGENT_MODEL = %q, want gpt-5.5; env=%v", gotModel, fixture.orch.launch.Env)
	}
}

func TestServiceRecoverRejectsMissingCWDBeforeOrchestrationSideEffects(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.5",
		CreatedAt: 123,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
	}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when recover cwd is missing")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, orch, nil).(*service)

	_, err := svc.Recover(context.Background(), "thread-parent")
	if err == nil || !strings.Contains(err.Error(), "recover cwd is required") {
		t.Fatalf("Recover() error = %v, want cwd required", err)
	}
	if len(orch.recovered) != 0 || orch.launch.AgentID != "" {
		t.Fatalf("orchestration side effects = recovered %#v launch %#v, want none", orch.recovered, orch.launch)
	}
}

func TestResolveForkCWDRejectsMetaBindingMismatch(t *testing.T) {
	t.Parallel()

	_, err := resolveForkCWD("/tmp/project-a", "/tmp/project-b")
	if err == nil || !strings.Contains(err.Error(), "fork cwd mismatch") {
		t.Fatalf("resolveForkCWD() error = %v, want cwd mismatch", err)
	}
}

type recoverServiceFixture struct {
	threads *stubThreadStore
	orch    *forkOrchestrationStub
	svc     *service
}

func newResumeRecoverFixture(t *testing.T) *recoverServiceFixture {
	t.Helper()
	const providerParentUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	rolloutPath := writeExistingProviderHistoryFile(t)
	resumedSession := &stubSession{threadID: providerParentUUID, rolloutPath: rolloutPath}
	sessions := &stubSessionProvider{}
	threads := resumeRecoverThreadStore()
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		assertResumeRecoverRequest(t, req, providerParentUUID)
		sessions.session = resumedSession
		return resumedSession, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, resumeRecoverBindingStore(providerParentUUID, rolloutPath), sessions, starter, nil, orch, nil).(*service)
	return &recoverServiceFixture{threads: threads, orch: orch, svc: svc}
}

func resumeRecoverBindingStore(providerParentUUID, rolloutPath string) *stubBindingStore {
	return &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: providerParentUUID,
		CodexThreadID:    "thread-parent",
		RolloutPath:      rolloutPath,
		SessionUUID:      providerParentUUID,
		Cwd:              "/repo",
	}}
}

func resumeRecoverThreadStore() *stubThreadStore {
	return &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
	}, promptSnapshot: &threadstore.PromptSnapshot{
		DisplayName:           "Recovered Thread",
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  promptSnapshotHash("Recovered Thread", "stored base", "stored dev", "codex", nil),
	}}
}

func assertResumeRecoverRequest(t *testing.T, req dto.ResumeSessionRequest, providerParentUUID string) {
	t.Helper()
	if req.Provider != "codex" || req.AgentID != "agent-parent" || req.ThreadID != "thread-parent" {
		t.Fatalf("ResumeSession request = %#v", req)
	}
	if req.ProviderThreadID != providerParentUUID {
		t.Fatalf("ProviderThreadID = %q, want %s", req.ProviderThreadID, providerParentUUID)
	}
	if req.PromptSnapshot.BaseInstructions != "stored base" || req.PromptSnapshot.DeveloperInstructions != "stored dev" {
		t.Fatalf("PromptSnapshot = %#v, want stored snapshot", req.PromptSnapshot)
	}
}

func assertResumeRecoverResult(t *testing.T, result RecoverResult, fixture *recoverServiceFixture) {
	t.Helper()
	if result != (RecoverResult{ThreadID: "thread-parent", Status: "recovering", Recovered: true, Mode: "relaunch_resume"}) {
		t.Fatalf("Recover() result = %#v", result)
	}
	if len(fixture.orch.recovered) != 1 || fixture.orch.recovered[0] != "agent-parent" {
		t.Fatalf("recover calls = %#v", fixture.orch.recovered)
	}
	if len(fixture.orch.bindAgentIDs) != 0 {
		t.Fatalf("bind session calls = %#v, want none without session generation support", fixture.orch.bindAgentIDs)
	}
	if fixture.threads.upsert.ThreadID != "thread-parent" {
		t.Fatalf("thread upsert = %#v", fixture.threads.upsert)
	}
	if fixture.threads.upsert.Prompt != "Recovered Thread" {
		t.Fatalf("persisted prompt = %q, want Recovered Thread", fixture.threads.upsert.Prompt)
	}
}

func launchEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(item, prefix))
		}
	}
	return ""
}

func TestServiceRecoverRehydratesClaudeOverrideConfigWhenSessionMissing(t *testing.T) {
	t.Parallel()

	svc := newClaudeRecoverService(t)
	result, err := svc.Recover(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	assertRecoverResumeEnvelope(t, result)
}

func newClaudeRecoverService(t *testing.T) *service {
	t.Helper()
	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	const providerParentUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	rolloutPath := writeExistingProviderHistoryFile(t)
	resumedSession := &stubSession{threadID: providerParentUUID, rolloutPath: rolloutPath}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		assertClaudeRecoverRequest(t, req, model, effort)
		sessions.session = resumedSession
		return resumedSession, nil
	}}
	return NewService(
		silentLogger(),
		claudeRecoverThreadStore(t, model, effort),
		claudeRecoverBindingStore(providerParentUUID, rolloutPath),
		sessions,
		starter,
		nil,
		&forkOrchestrationStub{},
		nil,
	).(*service)
}

func claudeRecoverBindingStore(providerParentUUID, rolloutPath string) *stubBindingStore {
	return &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "claude",
		ProviderThreadID: providerParentUUID,
		CodexThreadID:    "thread-parent",
		RolloutPath:      rolloutPath,
		SessionUUID:      providerParentUUID,
		Cwd:              "/repo",
	}}
}

func claudeRecoverThreadStore(t *testing.T, model, effort string) *stubThreadStore {
	t.Helper()
	return &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-parent",
		AgentID:        "agent-parent",
		Prompt:         "Recovered Thread",
		Model:          "sonnet",
		Cwd:            "/repo",
		CreatedAt:      123,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Model: model, Effort: effort}),
	}}
}

func assertClaudeRecoverRequest(t *testing.T, req dto.ResumeSessionRequest, model, effort string) {
	t.Helper()
	if req.Provider != "claude" || req.AgentID != "agent-parent" || req.ThreadID != "thread-parent" {
		t.Fatalf("ResumeSession request = %#v", req)
	}
	if req.Model != model || req.Effort != effort {
		t.Fatalf("ResumeSession request = %#v, want model/effort restored", req)
	}
	if req.ConfigOverride.Model == nil || *req.ConfigOverride.Model != model {
		t.Fatalf("ConfigOverride.Model = %#v, want %q", req.ConfigOverride.Model, model)
	}
	if req.ConfigOverride.Effort == nil || *req.ConfigOverride.Effort != effort {
		t.Fatalf("ConfigOverride.Effort = %#v, want %q", req.ConfigOverride.Effort, effort)
	}
}

func assertRecoverResumeEnvelope(t *testing.T, result RecoverResult) {
	t.Helper()
	if result != (RecoverResult{ThreadID: "thread-parent", Status: "recovering", Recovered: true, Mode: "relaunch_resume"}) {
		t.Fatalf("Recover() result = %#v", result)
	}
}

func TestServiceRecoverReturnsRestoreEnvelopeWhenSessionActive(t *testing.T) {
	t.Parallel()

	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{
		onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
			t.Fatal("ResumeSession should not be called when session is already active")
			return nil, nil
		},
	}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Recover(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if result != (RecoverResult{ThreadID: "thread-parent", Status: "recovering", Recovered: true, Mode: "restore_launch"}) {
		t.Fatalf("Recover() result = %#v", result)
	}
	if len(orch.recovered) != 1 || orch.recovered[0] != "agent-parent" {
		t.Fatalf("recover calls = %#v", orch.recovered)
	}
	if len(orch.bindAgentIDs) != 0 {
		t.Fatalf("bind session calls = %#v, want none", orch.bindAgentIDs)
	}
}

func TestServiceRecoverDoesNotInvalidatePromptAssemblyWithoutWorktreeRestore(t *testing.T) {
	t.Parallel()

	promptAssembly := &forkPromptAssemblyStub{}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when session is already active")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewServiceWithPromptAssembly(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil, promptAssembly, nil, nil).(*service)

	if _, err := svc.Recover(context.Background(), "thread-parent"); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if got := promptAssembly.invalidated; len(got) != 0 {
		t.Fatalf("Invalidate calls = %#v, want none", got)
	}
}

func TestServiceRecoverInvalidatesPromptAssemblyForWorktreeRestore(t *testing.T) {
	t.Parallel()

	_, worktreeCWD := forkPromptGitFixture(t)
	promptAssembly := &forkPromptAssemblyStub{}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              worktreeCWD,
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.5",
		Cwd:       worktreeCWD,
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when session is already active")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewServiceWithPromptAssembly(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil, promptAssembly, nil, nil).(*service)

	if _, err := svc.Recover(context.Background(), "thread-parent"); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateResumeRestore {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateResumeRestore)
	}
}

type forkOrchestrationStub struct {
	launch       LaunchAgentRequest
	recovered    []string
	bindAgentIDs []string
	recoverErr   error
}

func (s *forkOrchestrationStub) LaunchAgent(_ context.Context, req LaunchAgentRequest) error {
	s.launch = req
	return nil
}

func (s *forkOrchestrationStub) StopAgent(context.Context, string) error { return nil }

func (s *forkOrchestrationStub) Recover(_ context.Context, agentID string) error {
	s.recovered = append(s.recovered, agentID)
	return s.recoverErr
}

func (s *forkOrchestrationStub) BindSessionGeneration(_ context.Context, agentID string, _ uint64) error {
	s.bindAgentIDs = append(s.bindAgentIDs, agentID)
	return nil
}
