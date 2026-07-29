package thread

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

type startSnapshotPromptAssembly struct {
	start contract.StartAssembly
}

type phase45StartOnlySessionStarter struct {
	onStart func(context.Context, dto.StartSessionRequest) (contract.Session, error)
}

type phase45PromptAssemblyStub struct {
	startAssembly contract.StartAssembly
}

type resumeMetadataPromptAssembly struct {
	startInput contract.StartInput
}

func (s startSnapshotPromptAssembly) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return s.start, nil
}

func (startSnapshotPromptAssembly) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (startSnapshotPromptAssembly) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (startSnapshotPromptAssembly) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

func (s phase45StartOnlySessionStarter) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	session, err := s.onStart(ctx, req)
	if err != nil {
		return nil, err
	}
	return attachStartedCodexRuntimeIdentityForTest(req, session), nil
}

func (phase45StartOnlySessionStarter) ResumeSession(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	// archguard:ignore panic_count -- test stub must fail loudly if resume path is called.
	panic("unexpected ResumeSession call")
}

func (p phase45PromptAssemblyStub) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return p.startAssembly, nil
}

func (phase45PromptAssemblyStub) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (p phase45PromptAssemblyStub) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return p.startAssembly, nil
}

func (phase45PromptAssemblyStub) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

func (p *resumeMetadataPromptAssembly) AssembleStart(_ context.Context, in contract.StartInput) (contract.StartAssembly, error) {
	p.startInput = in
	return contract.StartAssembly{
		DisplayName:           in.Name,
		BaseInstructions:      "rebuilt base",
		DeveloperInstructions: "rebuilt dev",
	}, nil
}

func (*resumeMetadataPromptAssembly) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (p *resumeMetadataPromptAssembly) AssembleAgent(_ context.Context, in contract.AgentInput) (contract.StartAssembly, error) {
	p.startInput = in.StartInput
	return contract.StartAssembly{
		DisplayName:           in.StartInput.Name,
		BaseInstructions:      "rebuilt base",
		DeveloperInstructions: "rebuilt dev",
	}, nil
}

func (*resumeMetadataPromptAssembly) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

func TestPhase45BaseInstructionsStayOutOfPromptStorage(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &phase45StartOnlySessionStarter{onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		if req.Instructions != "system prompt" {
			t.Fatalf("instructions = %q, want system prompt", req.Instructions)
		}
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f601"}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-base",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		BaseInstructions:  "system prompt",
		PromptAssemblyRef: promptAssemblyForTest("system prompt"),
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "" {
		t.Fatalf("launch name = %q, want empty", orch.launchReq.Name)
	}
	if threads.upsert.Name != "" || threads.upsert.Prompt != "" {
		t.Fatalf("persisted name/prompt = %q/%q, want empty", threads.upsert.Name, threads.upsert.Prompt)
	}
}

func TestPhase45ResumeForwardsPromptSnapshot(t *testing.T) {
	t.Parallel()

	snapshot := contract.PromptAssemblySnapshot{
		DisplayName:           "resume name",
		BaseInstructions:      "snapshot base",
		DeveloperInstructions: "snapshot dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  "snapshot-hash",
	}
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume name",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	const providerThreadID = "11111111-2222-3333-4444-555555555561"
	rolloutPath := writeExistingProviderHistoryFile(t, providerThreadID)
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.PromptSnapshot.DisplayName != snapshot.DisplayName ||
			req.PromptSnapshot.BaseInstructions != snapshot.BaseInstructions ||
			req.PromptSnapshot.DeveloperInstructions != snapshot.DeveloperInstructions ||
			req.PromptSnapshot.Provider != snapshot.Provider ||
			req.PromptSnapshot.Version != snapshot.Version ||
			req.PromptSnapshot.Hash != snapshot.Hash {
			t.Fatalf("PromptSnapshot = %#v, want %#v", req.PromptSnapshot, snapshot)
		}
		session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID:       "thread-1",
		PromptSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.SessionID != providerThreadID || result.Status != "resumed" {
		t.Fatalf("Resume() result = %#v, want %s/resumed", result, providerThreadID)
	}
}

func TestPhaseGResumeRebuildsPromptSnapshotFromStoredAgentIdentity(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:         "thread-1",
		AgentID:          "agent-1",
		ParentAgentID:    "agent-root",
		AgentType:        "worker",
		AgentMemoryScope: "local",
		Prompt:           "resume name",
		Model:            "gpt-5.5",
		Cwd:              "/repo",
		CreatedAt:        123,
		Status:           statusCreated,
		ConfigOverride:   legacyPromptSnapshotMigrationConfig(t),
	}}
	const providerThreadID = "11111111-2222-3333-4444-555555555562"
	rolloutPath := writeExistingProviderHistoryFile(t, providerThreadID)
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		ParentAgentID:    "agent-root",
		AgentType:        "worker",
		AgentMemoryScope: "local",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	assembly := &resumeMetadataPromptAssembly{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.PromptSnapshot.BaseInstructions != "rebuilt base" || req.PromptSnapshot.DeveloperInstructions != "rebuilt dev" {
			t.Fatalf("PromptSnapshot = %#v, want rebuilt snapshot", req.PromptSnapshot)
		}
		session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewServiceWithPromptAssembly(
		silentLogger(),
		threads,
		bindings,
		sessions,
		starter,
		nil,
		orch,
		nil,
		assembly,
		nil,
		nil,
	).(*service)

	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if assembly.startInput.ParentAgentID != "agent-root" ||
		assembly.startInput.AgentType != "worker" ||
		assembly.startInput.AgentMemoryScope != "local" {
		t.Fatalf("resume rebuild start input = %#v", assembly.startInput)
	}
	if orch.launchReq.ParentID != "agent-root" ||
		orch.launchReq.AgentType != "worker" ||
		orch.launchReq.MemoryScope != "local" {
		t.Fatalf("launch request = %#v", orch.launchReq)
	}
}

func TestPhase45StartPersistsPromptSnapshot(t *testing.T) {
	t.Parallel()

	startAssembly := contract.StartAssembly{
		DisplayName:           "start name",
		BaseInstructions:      "start base",
		DeveloperInstructions: "start dev",
		Snapshot: contract.PromptAssemblySnapshot{
			DisplayName:           "start name",
			BaseInstructions:      "start base",
			DeveloperInstructions: "start dev",
			Provider:              "codex",
			Version:               contract.PromptAssemblySnapshotVersion,
			Hash:                  "start-hash",
		},
	}
	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &phase45StartOnlySessionStarter{onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f602"}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewServiceWithPromptAssembly(
		silentLogger(),
		threads,
		nil,
		sessions,
		starter,
		nil,
		orch,
		nil,
		startSnapshotPromptAssembly{start: startAssembly},
		nil,
		nil,
	).(*service)

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-start",
		Provider: "codex",
		CWD:      wantStartCWD(t),
		Name:     "start name",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if threads.promptSnapshot == nil {
		t.Fatal("promptSnapshot = nil, want saved snapshot")
	}
	if threads.promptSnapshot.BaseInstructions != "start base" || threads.promptSnapshot.DeveloperInstructions != "start dev" {
		t.Fatalf("promptSnapshot = %#v, want start snapshot", threads.promptSnapshot)
	}
}

func TestPhase45ExplicitNameWinsOverLegacyPromptFallback(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &phase45StartOnlySessionStarter{onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		if req.Instructions != "assembled system" {
			t.Fatalf("instructions = %q, want assembled system", req.Instructions)
		}
		if req.StartAssembly.DisplayName != "clean name" {
			t.Fatalf("start assembly display name = %q, want clean name", req.StartAssembly.DisplayName)
		}
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f603"}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-name",
		Provider: "codex",
		CWD:      wantStartCWD(t),
		Name:     "clean name",
		Prompt:   "legacy prompt",
		PromptAssemblyRef: phase45PromptAssemblyStub{startAssembly: contract.StartAssembly{
			BaseInstructions:      "assembled system",
			DeveloperInstructions: "assembled dev",
		}},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "clean name" {
		t.Fatalf("launch name = %q, want clean name", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "clean name" {
		t.Fatalf("persisted prompt = %q, want clean name", threads.upsert.Prompt)
	}
}
