package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	promptpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt"
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
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f606"}
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
		CWD:                   wantStartCWD(t),
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

func TestNonForcedStartCarriesAvailableExpertsToProviderAssembly(t *testing.T) {
	t.Setenv("PROMPT_START_CURRENT_DATE", "2026-05-22")

	cwd := resolvePromptCWD("/repo/a")
	general := sqlTemplate("main/general-zh", "main", "assembled default base", []string{"scope.cwd:" + cwd})
	general.ID = 1
	general.Priority = 160
	general.MatchWhen = json.RawMessage(`{}`)
	expert := sqlTemplate("main/expert/prompt", "main", "", []string{"scope.cwd:" + cwd, "intent:expert"})
	expert.ID = 2
	expert.Priority = 120
	expert.Title = "协作编程任务处理助手"
	expert.WhenToUse = "需要创建项目、修改代码、排查 bug 或规划多步骤软件工程任务时使用。"
	store := &fakePromptCatalog{templates: []PromptTemplate{general, expert}}

	promptAssembly := promptpkg.NewService(&promptpkg.Config{}, nil)
	if err := promptAssembly.RegisterDynamicProvider(staticAvailableExpertsProvider{}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.Config["prompt_key"] != "main/general-zh" {
				t.Fatalf("provider config prompt_key = %#v, want default fallback prompt", req.Config["prompt_key"])
			}
			if req.StartAssembly.UserContext["runtimeExtras"] == "" {
				t.Fatalf("StartAssembly.UserContext = %#v, want runtimeExtras", req.StartAssembly.UserContext)
			}
			for _, want := range []string{"可用专家", "main/expert/prompt", "prompt_key='main/expert/prompt'"} {
				if !strings.Contains(req.StartAssembly.UserContext["runtimeExtras"], want) {
					t.Fatalf("runtimeExtras = %q, want substring %q", req.StartAssembly.UserContext["runtimeExtras"], want)
				}
			}
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f608"}
			sessions.session = session
			return session, nil
		},
	}
	svc := NewServiceWithPromptAssemblyAndSharedFiles(
		silentLogger(),
		threads,
		nil,
		sessions,
		starter,
		nil,
		&stubThreadOrchestration{},
		nil,
		promptAssembly,
		testThreadDependencyConfig(),
		nil,
		nil,
		store,
		promptpkg.EvaluateMatchWhen,
		promptpkg.EvaluateEnableWhen,
	).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-non-forced",
		Provider: "codex",
		CWD:      cwd,
		Prompt:   "请分析这个 Go 项目的测试失败，并规划需要改哪些后端和前端文件。",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
}

type staticAvailableExpertsProvider struct{}

func (staticAvailableExpertsProvider) SectionName() string {
	return contract.DynamicSectionAvailableExperts
}

func (staticAvailableExpertsProvider) Resolve(context.Context, contract.SectionContext) (*string, error) {
	text := "可用专家: main/expert/prompt; use prompt_key='main/expert/prompt'"
	return &text, nil
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
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f607"}
			sessions.session = session
			return session, nil
		},
	}
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
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-assembly",
		AgentID:        "agent-assembly",
		Prompt:         snapshot.DisplayName,
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	const providerThreadID = "019d5f6b-fb3c-7760-9d6f-54005553f608"
	rolloutPath := writeExistingProviderHistoryFile(t)
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-assembly",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-assembly",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.ThreadID != "thread-assembly" {
				t.Fatalf("ThreadID = %q, want thread-assembly", req.ThreadID)
			}
			if req.ProviderThreadID != providerThreadID {
				t.Fatalf("ProviderThreadID = %q, want %s", req.ProviderThreadID, providerThreadID)
			}
			if !reflect.DeepEqual(req.PromptSnapshot, snapshot) {
				t.Fatalf("PromptSnapshot = %#v, want %#v", req.PromptSnapshot, snapshot)
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
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
	if result.ThreadID != "thread-assembly" || result.SessionID != providerThreadID {
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

	const providerThreadID = "019d5f6b-fb3c-7760-9d6f-54005553f706"
	promptAssembly := &stubPromptAssemblyService{}
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-resume",
		AgentID:        "agent-resume",
		Prompt:         "resume name",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:       "agent-resume",
		Provider:      "codex",
		CodexThreadID: "thread-resume",
		RolloutPath:   writeExistingProviderHistoryFile(t),
		SessionUUID:   providerThreadID,
		Cwd:           "/repo",
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

	const providerThreadID = "019d5f6b-fb3c-7760-9d6f-54005553f704"
	_, worktreeCWD := newPromptGitFixture(t)
	promptAssembly := &stubPromptAssemblyService{}
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-resume",
		AgentID:        "agent-resume",
		Prompt:         "resume name",
		Model:          "gpt-5.5",
		Cwd:            worktreeCWD,
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:       "agent-resume",
		Provider:      "codex",
		CodexThreadID: "thread-resume",
		RolloutPath:   writeExistingProviderHistoryFile(t),
		SessionUUID:   providerThreadID,
		Cwd:           worktreeCWD,
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
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-parent",
		AgentID:        "agent-parent",
		Prompt:         "assembled name",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.AgentID != "thread-fork" || req.ThreadID != "thread-fork" {
				t.Fatalf("ResumeSession request = %#v, want thread-fork agent/thread", req)
			}
			if req.Model != "gpt-5.5" {
				t.Fatalf("Model = %q, want gpt-5.5", req.Model)
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
	if result != (ForkResult{NewThreadID: "thread-fork", ForkedFrom: "thread-parent", KickoffState: ForkKickoffState("created_only")}) {
		t.Fatalf("Fork() result = %#v", result)
	}
	if originalSession.forkRequest.ThreadID != "provider-thread-parent" {
		t.Fatalf("forkRequest.ThreadID = %q, want provider-thread-parent", originalSession.forkRequest.ThreadID)
	}
	if orch.launch.Name != "assembled name (续)" {
		t.Fatalf("launch name = %q, want assembled name (续)", orch.launch.Name)
	}
	if threads.upsert.Prompt != "assembled name (续)" {
		t.Fatalf("persisted prompt = %q, want assembled name (续)", threads.upsert.Prompt)
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
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f609"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-name",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Name:              "clean display name",
		Prompt:            "legacy prompt should stay out of the name slot",
		PromptAssemblyRef: promptAssemblyForTest(""),
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
	assembly.UnregisterDynamicProvider(promptpkg.DynamicSectionSessionGuidance)
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
	renderedUserContext := contract.RenderUserContextMessage(turnAssembly)
	if !strings.Contains(renderedUserContext, "<system-reminder>") {
		t.Fatalf("RenderUserContextMessage() = %q, want system reminder envelope", renderedUserContext)
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
	assertPromptSectionContains(t, turnAssembly.ResolvedSections, promptpkg.DynamicSectionSessionGuidance, "please verify the cache", "/repo")
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

func (*stubPromptAssemblyService) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
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

func assertPromptSectionContains(t *testing.T, sections []contract.ResolvedPromptSection, name string, wants ...string) {
	t.Helper()

	content, ok := sectionContent(sections, name)
	if !ok {
		t.Fatalf("ResolvedSections = %#v, want section %q", sections, name)
	}
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("section %q = %q, want substring %q", name, content, want)
		}
	}
}
