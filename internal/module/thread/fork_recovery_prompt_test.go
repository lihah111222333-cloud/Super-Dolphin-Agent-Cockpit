package thread

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestServiceRecoverDoesNotInvalidatePromptAssemblyWithoutWorktreeRestore(t *testing.T) {
	t.Parallel()

	promptAssembly := &forkPromptAssemblyStub{}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
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
	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              worktreeCWD,
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
