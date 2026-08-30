package thread

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestServiceRecoverRejectsLegacySnapshotEvenWhenSessionActive(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555594"
	rolloutPath := writeExistingProviderHistoryFile(t, providerThreadID)
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
	}, promptSnapshot: &PromptSnapshotRecord{
		DisplayName:      "Recovered Thread",
		BaseInstructions: "stored base",
		Provider:         "codex",
		Version:          contract.PromptAssemblySnapshotVersion,
		SectionSnapshot:  map[string]string{contract.DynamicSectionMemory: "legacy memory"},
	}}
	threads.promptSnapshot.Hash = promptSnapshotHash(
		threads.promptSnapshot.DisplayName,
		threads.promptSnapshot.BaseInstructions,
		threads.promptSnapshot.DeveloperInstructions,
		threads.promptSnapshot.Provider,
		nil,
		threads.promptSnapshot.SectionSnapshot,
		threads.promptSnapshot.Generation,
	)
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:              "agent-parent",
		Provider:             "codex",
		ProviderThreadID:     providerThreadID,
		CodexThreadID:        "thread-parent",
		RolloutPath:          rolloutPath,
		ProviderRecoveryHome: t.TempDir(),
		Cwd:                  "/repo",
	}}
	sessions := &stubSessionProvider{session: &stubSession{threadID: providerThreadID}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called for a legacy snapshot")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Recover(context.Background(), "thread-parent")
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) {
		t.Fatalf("Recover() error = %v, want legacy memory sentinel", err)
	}
	if len(orch.recovered) != 0 {
		t.Fatalf("orchestration recover calls = %#v, want none before snapshot rejection", orch.recovered)
	}
}

func TestServiceRecoverChecksCanonicalSnapshotWhenCalledByAlias(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555599"
	legacy := validPromptSnapshotRecordForTest("codex", "Recovered Alias")
	legacy.SectionSnapshot = map[string]string{contract.DynamicSectionMemory: "retired memory"}
	legacy.Hash = promptSnapshotHash(
		legacy.DisplayName,
		legacy.BaseInstructions,
		legacy.DeveloperInstructions,
		legacy.Provider,
		toDTOPromptBoundary(legacy.Boundary),
		legacy.SectionSnapshot,
		legacy.Generation,
	)
	threads := &aliasPromptSnapshotThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID:  "agent-recover-alias",
			AgentID:   "agent-recover-alias",
			Prompt:    "Recovered Alias",
			Model:     "gpt-5.5",
			Cwd:       "/repo",
			CreatedAt: 123,
		}},
		snapshots: map[string]*PromptSnapshotRecord{
			"thread-recover-alias": &legacy,
		},
	}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:              "agent-recover-alias",
		Provider:             "codex",
		ProviderThreadID:     providerThreadID,
		CodexThreadID:        "thread-recover-alias",
		ProviderRecoveryHome: t.TempDir(),
		Cwd:                  "/repo",
	}}
	sessions := &stubSessionProvider{session: &stubSession{threadID: providerThreadID}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, &stubSessionStarter{}, nil, orch, nil).(*service)

	_, err := svc.Recover(context.Background(), "agent-recover-alias")
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) {
		t.Fatalf("Recover(alias) error = %v, want legacy memory sentinel", err)
	}
	if len(orch.recovered) != 0 || orch.launch.AgentID != "" {
		t.Fatalf("orchestration side effects = recovered %#v launch %#v, want none before canonical snapshot rejection", orch.recovered, orch.launch)
	}
}

func TestServiceForkChecksCanonicalSnapshotWhenCalledByAlias(t *testing.T) {
	t.Parallel()

	legacy := validPromptSnapshotRecordForTest("codex", "Fork Alias")
	legacy.SectionSnapshot = map[string]string{contract.DynamicSectionMemoryContext: "retired memory"}
	legacy.Hash = promptSnapshotHash(
		legacy.DisplayName,
		legacy.BaseInstructions,
		legacy.DeveloperInstructions,
		legacy.Provider,
		toDTOPromptBoundary(legacy.Boundary),
		legacy.SectionSnapshot,
		legacy.Generation,
	)
	threads := &aliasPromptSnapshotThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID:  "agent-fork-alias",
			AgentID:   "agent-fork-alias",
			Prompt:    "Fork Alias",
			Model:     "gpt-5.5",
			Cwd:       "/repo",
			CreatedAt: 123,
		}},
		snapshots: map[string]*PromptSnapshotRecord{
			"thread-fork-alias": &legacy,
		},
	}
	binding := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-fork-alias",
		Provider:         "codex",
		ProviderThreadID: "provider-fork-alias",
		CodexThreadID:    "thread-fork-alias",
		Cwd:              "/repo",
	}}
	original := &stubSession{threadID: "provider-fork-alias", forkResult: dto.ForkResult{NewThreadID: "thread-fork-child"}}
	sessions := &stubSessionProvider{session: original}
	orch := &forkOrchestrationStub{}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called for a legacy snapshot")
		return nil, nil
	}}
	svc := NewService(silentLogger(), threads, binding, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Fork(context.Background(), "agent-fork-alias")
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) {
		t.Fatalf("Fork(alias) error = %v, want legacy memory sentinel", err)
	}
	if original.forkRequest.ThreadID != "" {
		t.Fatalf("ForkThread request = %#v, want no provider fork before canonical snapshot rejection", original.forkRequest)
	}
	if orch.launch.AgentID != "" || len(orch.recovered) != 0 {
		t.Fatalf("orchestration side effects = launch %#v recovered %#v, want none", orch.launch, orch.recovered)
	}
}

// aliasPromptSnapshotThreadStore keeps snapshots keyed by their canonical
// public thread id so tests can exercise Recover calls made with an alias.
type aliasPromptSnapshotThreadStore struct {
	*stubThreadStore
	snapshots map[string]*PromptSnapshotRecord
}

func (s *aliasPromptSnapshotThreadStore) LoadPromptSnapshot(_ context.Context, threadID string) (*PromptSnapshotRecord, error) {
	if snapshot := s.snapshots[threadID]; snapshot != nil {
		copy := *snapshot
		copy.SectionSnapshot = clonePromptSectionMap(snapshot.SectionSnapshot)
		return &copy, nil
	}
	return nil, nil
}

func toDTOPromptBoundary(boundary *PromptBoundaryRecord) *dto.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	return &dto.PromptAssemblyBoundary{CachedPrefix: boundary.CachedPrefix, UncachedTail: boundary.UncachedTail}
}

func TestServiceRecoverDoesNotInvalidatePromptAssemblyWithoutWorktreeRestore(t *testing.T) {
	t.Parallel()

	promptAssembly := &forkPromptAssemblyStub{}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "11111111-2222-3333-4444-555555555595"}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:              "agent-parent",
		Provider:             "codex",
		ProviderThreadID:     "11111111-2222-3333-4444-555555555595",
		CodexThreadID:        "thread-parent",
		ProviderRecoveryHome: t.TempDir(),
		Cwd:                  "/repo",
	}}
	threads := &stubThreadStore{thread: &ThreadRecord{
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
	sessions := &stubSessionProvider{session: &stubSession{threadID: "11111111-2222-3333-4444-555555555595"}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:              "agent-parent",
		Provider:             "codex",
		ProviderThreadID:     "11111111-2222-3333-4444-555555555595",
		CodexThreadID:        "thread-parent",
		ProviderRecoveryHome: t.TempDir(),
		Cwd:                  worktreeCWD,
	}}
	threads := &stubThreadStore{thread: &ThreadRecord{
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
	bindErr      error
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
	return s.bindErr
}

type generationForkSessionProvider struct {
	*stubSessionProvider
	generation uint64
}

func (p *generationForkSessionProvider) SessionGeneration(string) uint64 {
	return p.generation
}

type recordingForkThreadStore struct {
	*stubThreadStore
	deletedThreadIDs []string
	failAfterUpserts int
	failAfterErr     error
}

func (s *recordingForkThreadStore) Upsert(ctx context.Context, params ThreadUpsert) error {
	if s.failAfterUpserts > 0 && s.upsertCount+1 >= s.failAfterUpserts {
		return s.failAfterErr
	}
	return s.stubThreadStore.Upsert(ctx, params)
}

func (s *recordingForkThreadStore) DeleteByThreadID(_ context.Context, threadID string) error {
	s.deletedThreadIDs = append(s.deletedThreadIDs, threadID)
	if s.thread != nil && s.thread.ThreadID == threadID {
		s.thread = nil
	}
	if s.promptSnapshotID == threadID {
		s.promptSnapshotID = ""
		s.promptSnapshot = nil
	}
	return nil
}

func assertForkFailureCleaned(t *testing.T, threads *recordingForkThreadStore, bindings *stubBindingStore) {
	t.Helper()
	if len(threads.deletedThreadIDs) == 0 || threads.deletedThreadIDs[len(threads.deletedThreadIDs)-1] != "thread-fork" {
		t.Fatalf("DeleteByThreadID calls = %#v, want thread-fork cleanup", threads.deletedThreadIDs)
	}
	if len(bindings.deleteAgentIDs) == 0 || bindings.deleteAgentIDs[len(bindings.deleteAgentIDs)-1] != "thread-fork" {
		t.Fatalf("DeleteByAgentID calls = %#v, want thread-fork cleanup", bindings.deleteAgentIDs)
	}
	if threads.promptSnapshot != nil || threads.promptSnapshotID == "thread-fork" {
		t.Fatalf("prompt snapshot still present after fork failure: id=%q snapshot=%#v", threads.promptSnapshotID, threads.promptSnapshot)
	}
}

func assertForkNotRecoverable(t *testing.T, svc *service, threadID string) {
	t.Helper()
	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: threadID}); err == nil {
		t.Fatalf("Resume(%q) error = nil, want failed/creating fork to be non-recoverable", threadID)
	}
}
