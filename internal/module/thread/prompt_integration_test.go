package thread

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestStartSessionUsesPromptAssembly(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.Instructions != "assembled system" {
				t.Fatalf("instructions = %q, want assembled system", req.Instructions)
			}
			if got := req.Config["developerInstructions"]; got != "assembled dev" {
				t.Fatalf("developerInstructions = %#v, want assembled dev", got)
			}
			session := &stubSession{threadID: "provider-thread-assembly"}
			sessions.session = session
			return session, nil
		},
	}
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
		&stubPromptAssemblyService{start: contract.StartAssembly{
			DisplayName:           "assembled name",
			BaseInstructions:      "assembled system",
			DeveloperInstructions: "assembled dev",
		}},
		nil,
		nil,
	).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:               "agent-assembly",
		Provider:              "codex",
		Prompt:                "legacy prompt",
		BaseInstructions:      "raw system",
		DeveloperInstructions: "raw dev",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "assembled name" {
		t.Fatalf("launch name = %q, want assembled name", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "assembled name" {
		t.Fatalf("persisted prompt = %q, want assembled name", threads.upsert.Prompt)
	}
	if threads.promptSnapshot == nil || threads.promptSnapshotID != "agent-assembly" {
		t.Fatalf("saved prompt snapshot = %#v, thread = %q", threads.promptSnapshot, threads.promptSnapshotID)
	}
	if threads.promptSnapshot.BaseInstructions != "assembled system" || threads.promptSnapshot.DeveloperInstructions != "assembled dev" {
		t.Fatalf("stored prompt snapshot = %#v", threads.promptSnapshot)
	}
}

func TestBaseInstructionsNotFoldedIntoPrompt(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.Instructions != "system prompt" {
				t.Fatalf("instructions = %q, want system prompt", req.Instructions)
			}
			session := &stubSession{threadID: "provider-thread-base"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:          "agent-base",
		Provider:         "codex",
		BaseInstructions: "system prompt",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "" {
		t.Fatalf("launch name = %q, want empty", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "" {
		t.Fatalf("persisted prompt = %q, want empty", threads.upsert.Prompt)
	}
}

func TestResumeRestoresFromSnapshot(t *testing.T) {
	t.Parallel()

	snapshot := contract.PromptAssemblySnapshot{
		DisplayName:           "assembled name",
		BaseInstructions:      "assembled system",
		DeveloperInstructions: "assembled dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  "snapshot-hash",
		Generation:            7,
	}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-assembly",
		AgentID:   "agent-assembly",
		Prompt:    snapshot.DisplayName,
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-assembly",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-assembly",
		CodexThreadID:    "thread-assembly",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.ThreadID != "thread-assembly" {
				t.Fatalf("ThreadID = %q, want thread-assembly", req.ThreadID)
			}
			if req.ProviderThreadID != "provider-thread-assembly" {
				t.Fatalf("ProviderThreadID = %q, want provider-thread-assembly", req.ProviderThreadID)
			}
			if !reflect.DeepEqual(req.PromptSnapshot, snapshot) {
				t.Fatalf("PromptSnapshot = %#v, want %#v", req.PromptSnapshot, snapshot)
			}
			session := &stubSession{threadID: "provider-thread-assembly"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID:       "thread-assembly",
		PromptSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.ThreadID != "thread-assembly" || result.SessionID != "provider-thread-assembly" {
		t.Fatalf("Resume() result = %#v", result)
	}
	if orch.launchReq.Name != snapshot.DisplayName {
		t.Fatalf("launch name = %q, want %q", orch.launchReq.Name, snapshot.DisplayName)
	}
	if threads.upsert.Prompt != snapshot.DisplayName {
		t.Fatalf("persisted prompt = %q, want %q", threads.upsert.Prompt, snapshot.DisplayName)
	}
}

func TestResumeDoesNotInvalidatePromptAssemblyWithoutWorktreeRestore(t *testing.T) {
	t.Parallel()

	promptAssembly := &stubPromptAssemblyService{}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-resume",
		AgentID:   "agent-resume",
		Prompt:    "resume name",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-resume",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-resume",
		CodexThreadID:    "thread-resume",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		session := &stubSession{threadID: req.ProviderThreadID}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewServiceWithPromptAssembly(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil, promptAssembly, nil, nil).(*service)

	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-resume"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if got := promptAssembly.invalidated; len(got) != 0 {
		t.Fatalf("Invalidate calls = %#v, want none", got)
	}
}

func TestResumeInvalidatesPromptAssemblyForWorktreeRestore(t *testing.T) {
	t.Parallel()

	_, worktreeCWD := newPromptGitFixture(t)
	promptAssembly := &stubPromptAssemblyService{}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-resume",
		AgentID:   "agent-resume",
		Prompt:    "resume name",
		Model:     "gpt-5.4",
		Cwd:       worktreeCWD,
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-resume",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-resume",
		CodexThreadID:    "thread-resume",
		Cwd:              worktreeCWD,
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		session := &stubSession{threadID: req.ProviderThreadID}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewServiceWithPromptAssembly(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil, promptAssembly, nil, nil).(*service)

	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-resume"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateResumeRestore {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateResumeRestore)
	}
}

func TestSendCommandClearInvalidatesPromptAssembly(t *testing.T) {
	t.Parallel()

	promptAssembly := &stubPromptAssemblyService{}
	svc := NewServiceWithPromptAssembly(silentLogger(), nil, nil, nil, nil, nil, nil, nil, promptAssembly, nil, nil).(*service)

	got, err := svc.SendCommand(context.Background(), "thread-clear", "/clear", "")
	if err != nil {
		t.Fatalf("SendCommand(/clear) error = %v", err)
	}
	result, ok := got.(threadCommandResult)
	if !ok {
		t.Fatalf("SendCommand(/clear) result type = %T, want threadCommandResult", got)
	}
	if result.Command != "/clear" || result.ThreadID != "thread-clear" {
		t.Fatalf("SendCommand(/clear) result = %#v", result)
	}
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateClear {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateClear)
	}
}

func TestForkPreservesPromptAssembly(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "provider-thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "provider-thread-fork"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "assembled name",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.AgentID != "thread-fork" || req.ThreadID != "thread-fork" {
				t.Fatalf("ResumeSession request = %#v, want thread-fork agent/thread", req)
			}
			if req.Model != "gpt-5.4" {
				t.Fatalf("Model = %q, want gpt-5.4", req.Model)
			}
			sessions.session = forkedSession
			return forkedSession, nil
		},
	}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Fork(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if result != (ForkResult{NewThreadID: "thread-fork", ForkedFrom: "thread-parent"}) {
		t.Fatalf("Fork() result = %#v", result)
	}
	if originalSession.forkRequest.ThreadID != "provider-thread-parent" {
		t.Fatalf("forkRequest.ThreadID = %q, want provider-thread-parent", originalSession.forkRequest.ThreadID)
	}
	if orch.launch.Name != "assembled name" {
		t.Fatalf("launch name = %q, want assembled name", orch.launch.Name)
	}
	if threads.upsert.Prompt != "assembled name" {
		t.Fatalf("persisted prompt = %q, want assembled name", threads.upsert.Prompt)
	}
}

func TestNameNotPollutedByPrompt(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.StartAssembly.DisplayName != "clean display name" {
				t.Fatalf("display name = %q, want clean display name", req.StartAssembly.DisplayName)
			}
			if req.Instructions != "" {
				t.Fatalf("instructions = %q, want empty", req.Instructions)
			}
			session := &stubSession{threadID: "provider-thread-name"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-name",
		Provider: "codex",
		Name:     "clean display name",
		Prompt:   "legacy prompt should stay out of the name slot",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "clean display name" {
		t.Fatalf("launch name = %q, want clean display name", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "clean display name" {
		t.Fatalf("persisted prompt = %q, want clean display name", threads.upsert.Prompt)
	}
}

func TestTurnAssemblyUserContextText(t *testing.T) {
	t.Parallel()

	assembly := promptpkg.NewService(nil, silentLogger())
	if err := assembly.RegisterDynamicProvider(promptpkg.DynamicTextProvider{
		Name: promptpkg.DynamicSectionSessionGuidance,
		ResolveFunc: func(_ context.Context, in promptpkg.SectionContext) (*string, error) {
			text := fmt.Sprintf("user=%s cwd=%s", in.Turn.UserText, in.BuildCtx.CWD)
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	turnAssembly, err := assembly.AssembleTurn(context.Background(), promptpkg.TurnInput{
		UserText: "please verify the cache",
		CWD:      "/repo",
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if !strings.Contains(turnAssembly.RenderUserContextMessage(), "<system-reminder>") {
		t.Fatalf("RenderUserContextMessage() = %q, want system reminder envelope", turnAssembly.RenderUserContextMessage())
	}
	if turnAssembly.UserContext["currentDate"] == "" {
		t.Fatalf("UserContext = %#v, want currentDate entry", turnAssembly.UserContext)
	}
	if turnAssembly.UserContext["runtimeExtras"] == "" {
		t.Fatalf("UserContext = %#v, want runtimeExtras entry", turnAssembly.UserContext)
	}
	if !strings.Contains(turnAssembly.UserContextText, "# currentDate") {
		t.Fatalf("UserContextText = %q, want currentDate section", turnAssembly.UserContextText)
	}
	if !strings.Contains(turnAssembly.UserContextText, "# runtimeExtras") {
		t.Fatalf("UserContextText = %q, want runtimeExtras section", turnAssembly.UserContextText)
	}
	if content, ok := sectionContent(turnAssembly.ResolvedSections, promptpkg.DynamicSectionSessionGuidance); !ok || !strings.Contains(content, "please verify the cache") || !strings.Contains(content, "/repo") {
		t.Fatalf("ResolvedSections = %#v, want session guidance content", turnAssembly.ResolvedSections)
	}
}

type stubPromptAssemblyService struct {
	start       contract.StartAssembly
	err         error
	invalidated []contract.InvalidateReason
}

func (s *stubPromptAssemblyService) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	if s.err != nil {
		return contract.StartAssembly{}, s.err
	}
	return s.start, nil
}

func (*stubPromptAssemblyService) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (s *stubPromptAssemblyService) Invalidate(_ context.Context, reason contract.InvalidateReason) error {
	s.invalidated = append(s.invalidated, reason)
	return nil
}

func sectionContent(sections []contract.ResolvedPromptSection, name string) (string, bool) {
	for _, section := range sections {
		if section.Name == name {
			return section.Content, true
		}
	}
	return "", false
}
