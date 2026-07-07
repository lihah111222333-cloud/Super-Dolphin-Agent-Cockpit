package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/contracttest"
)

func TestClaudeCLIProviderContract(t *testing.T) {
	contracttest.Run(t, CompleteClaudeCLIContractSpec())
}

func CompleteClaudeCLIContractSpec() contracttest.Spec {
	return contracttest.Spec{
		Name:       "claude",
		Start:      startClaudeContractSession,
		Resume:     resumeClaudeContractSession,
		EventCases: claudeContractEventCases(),
		RequiredCases: map[contracttest.CaseKey]contracttest.Case{
			contracttest.CasePromptParity:  claudePromptParityContractCase(),
			contracttest.CaseApproval:      claudeApprovalContractCase(),
			contracttest.CaseInterrupt:     claudeInterruptContractCase(),
			contracttest.CaseForceComplete: claudeForceCompleteContractCase(),
			contracttest.CaseResume:        claudeResumeIdentityContractCase(),
			contracttest.CaseToolbridge:    claudeToolbridgeContractCase(),
			contracttest.CaseRuntimeReport: claudeRuntimeReportContractCase(),
		},
	}
}

func startClaudeContractSession(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	threadID := strings.TrimSpace(req.AgentID)
	if threadID == "" {
		threadID = "public-thread-contract"
	}
	return newClaudeContractSession(threadID), nil
}

func resumeClaudeContractSession(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	threadID := strings.TrimSpace(req.ProviderThreadID)
	if threadID == "" {
		return nil, errors.New("claude contract resume requires provider thread id")
	}
	return newClaudeContractSession(threadID), nil
}

func claudeContractEventCases() []contracttest.Case {
	return []contracttest.Case{
		claudeEventTranslationContractCase("system init runtime", "system_init_runtime", dto.RawProviderEvent{
			EventType: "system:init",
			Data: map[string]any{
				"timestamp":  "2026-04-13T00:00:00Z",
				"thread_id":  "thread-contract",
				"agent_id":   "agent-contract",
				"session_id": "session-contract",
			},
		}),
		claudeEventTranslationContractCase("turn complete", "turn_complete", dto.RawProviderEvent{
			EventType: "turn:complete",
			Data: map[string]any{
				"timestamp":   "2026-04-13T00:00:00Z",
				"thread_id":   "thread-contract",
				"agent_id":    "agent-contract",
				"turn_id":     "turn-contract",
				"success":     true,
				"status":      "completed",
				"reason":      "stop",
				"result":      "ok",
				"stop_reason": "end_turn",
			},
		}),
		claudeEventTranslationContractCase("tool begin", "tool_begin", dto.RawProviderEvent{
			EventType: "tool:use_begin",
			Data: map[string]any{
				"timestamp":         "2026-04-13T00:00:00Z",
				"thread_id":         "thread-contract",
				"agent_id":          "agent-contract",
				"turn_id":           "turn-contract",
				"call_id":           "call-contract",
				"tool_name":         "Read",
				"arguments_preview": `{"file_path":"README.md"}`,
			},
		}),
	}
}

func claudeEventTranslationContractCase(name, snapshotID string, raw dto.RawProviderEvent) contracttest.Case {
	return contracttest.Case{Name: name, Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		got := contracttest.CaptureProviderEventTranslation(t, "claude-"+snapshotID+"-capture", raw, translateClaudeEvent)
		want := contracttest.NewExpectedEventEvidence(contracttest.LoadExpectedEventSnapshot(t, snapshotID))
		e.RecordEventTranslation(t, name, got, want)
	}}
}

func claudePromptParityContractCase() contracttest.Case {
	return contracttest.Case{Name: "start and resume prompt parity", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		startGot := captureClaudeStartPromptParity(t)
		startWant := contracttest.NewExpectedPromptEvidence(contracttest.LoadExpectedPromptSnapshot(t, "start_prompt_parity"))
		e.RecordPromptParity(t, startGot, startWant)

		resumeGot := captureClaudeResumePromptParity(t)
		resumeWant := contracttest.NewExpectedPromptEvidence(contracttest.LoadExpectedPromptSnapshot(t, "resume_prompt_parity"))
		e.RecordPromptParity(t, resumeGot, resumeWant)
	}}
}

func captureClaudeStartPromptParity(t *testing.T) contracttest.PromptParityEvidence {
	t.Helper()
	req := claudeStartPromptParityRequest(t)
	next := newBufferedTransport(t, "provider-thread-prompt-start")
	var capturedConfig cliLaunchConfig
	var capturedInstructions string
	launchFn := func(_, _, _, instructions string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		capturedInstructions = instructions
		capturedConfig = cfg
		return next.tr, next.finish, nil
	}
	driver := claudeContractDriverWithLaunch(launchFn)
	_, err := driver.StartSession(context.Background(), req)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(next.finish)
	if capturedInstructions != req.StartAssembly.BaseInstructions {
		t.Fatalf("captured start instructions = %q, want %q", capturedInstructions, req.StartAssembly.BaseInstructions)
	}
	return contracttest.NewProviderPromptEvidence("claude-start-prompt-capture", promptParityFieldsFromSnapshot(t, capturedConfig.PromptSnapshot))
}

func captureClaudeResumePromptParity(t *testing.T) contracttest.PromptParityEvidence {
	t.Helper()
	req := claudeResumePromptParityRequest(t)
	next := newBufferedTransport(t, req.ProviderThreadID)
	var capturedConfig cliLaunchConfig
	var capturedInstructions string
	launchFn := func(_, _, _, instructions string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		capturedInstructions = instructions
		capturedConfig = cfg
		return next.tr, next.finish, nil
	}
	driver := claudeContractDriverWithLaunch(launchFn)
	_, err := driver.ResumeSession(context.Background(), req)
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	t.Cleanup(next.finish)
	if capturedInstructions != req.PromptSnapshot.BaseInstructions {
		t.Fatalf("captured resume instructions = %q, want %q", capturedInstructions, req.PromptSnapshot.BaseInstructions)
	}
	return contracttest.NewProviderPromptEvidence("claude-resume-prompt-capture", promptParityFieldsFromSnapshot(t, capturedConfig.PromptSnapshot))
}

func claudeApprovalContractCase() contracttest.Case {
	return contracttest.Case{Name: "permission mode policy", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		mode, err := resolvePermissionMode("on-failure", "")
		e.AssertNoError(t, contracttest.EvidenceKey("approval.permission_mode_error"), err)
		e.AssertEqual(t, contracttest.EvidenceKey("approval.permission_mode"), mode, "default")
		e.RecordOutcome(t, contracttest.EvidenceApprovalOutcome, contracttest.OutcomeEvidence{
			ObservedActionID: "claude-permission-mode-policy",
			StateBefore:      "approval_policy:on-failure",
			StateAfter:       "permission_mode:" + mode,
		})
	}}
}

func claudeInterruptContractCase() contracttest.Case {
	return contracttest.Case{Name: "session interrupt", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		active := newTurnHandle("local-interrupt-contract", "provider-interrupt-contract")
		session := &session{
			threadID:        "thread-interrupt-contract",
			sessionID:       "thread-interrupt-contract",
			publicThreadID:  "public-thread-interrupt-contract",
			activeTurn:      active,
			suppressedTurns: map[string]struct{}{},
		}
		if err := session.Interrupt(context.Background(), dto.InterruptRequest{Source: "contract"}); err != nil {
			t.Fatalf("Interrupt() error = %v", err)
		}
		select {
		case <-active.Done():
		default:
			t.Fatal("Interrupt() did not complete active turn")
		}
		if !errors.Is(active.Err(), context.Canceled) {
			t.Fatalf("Interrupt() turn error = %v, want context.Canceled", active.Err())
		}
		e.RecordOutcome(t, contracttest.EvidenceInterruptOutcome, contracttest.OutcomeEvidence{
			ObservedActionID: "claude-session-interrupt",
			StateBefore:      "active_turn:provider-interrupt-contract",
			StateAfter:       "active_turn:canceled",
		})
	}}
}

func claudeForceCompleteContractCase() contracttest.Case {
	return contracttest.Case{Name: "session force complete", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		active := newTurnHandle("local-force-contract", "provider-force-contract")
		session := &session{
			threadID:        "thread-force-contract",
			sessionID:       "thread-force-contract",
			publicThreadID:  "public-thread-force-contract",
			activeTurn:      active,
			suppressedTurns: map[string]struct{}{},
		}
		if err := session.ForceComplete(context.Background(), dto.ForceCompleteRequest{ProviderID: "provider-force-contract"}); err != nil {
			t.Fatalf("ForceComplete() error = %v", err)
		}
		select {
		case <-active.Done():
		default:
			t.Fatal("ForceComplete() did not complete active turn")
		}
		if err := active.Err(); err != nil {
			t.Fatalf("ForceComplete() turn error = %v, want nil", err)
		}
		e.RecordOutcome(t, contracttest.EvidenceForceCompleteOutcome, contracttest.OutcomeEvidence{
			ObservedActionID: "claude-session-force-complete",
			StateBefore:      "active_turn:provider-force-contract",
			StateAfter:       "active_turn:force_completed",
		})
	}}
}

func claudeResumeIdentityContractCase() contracttest.Case {
	return contracttest.Case{Name: "provider thread id resume", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		req := claudeResumePromptParityRequest(t)
		next := newBufferedTransport(t, req.ProviderThreadID)
		var capturedResumeID string
		launchFn := func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
			capturedResumeID = resumeID
			return next.tr, next.finish, nil
		}
		driver := claudeContractDriverWithLaunch(launchFn)
		_, err := driver.ResumeSession(context.Background(), req)
		if err != nil {
			t.Fatalf("ResumeSession() error = %v", err)
		}
		t.Cleanup(next.finish)
		e.RecordResumeIdentity(t, contracttest.ResumeIdentityEvidence{
			PublicThreadID:   req.ThreadID,
			ProviderThreadID: req.ProviderThreadID,
			ResumedThreadID:  capturedResumeID,
		})
	}}
}

func claudeToolbridgeContractCase() contracttest.Case {
	return contracttest.Case{Name: "provider native tool governance", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		args, err := buildCLIArgs("sonnet", "base", "/tmp/contract-mcp.json", cliLaunchConfig{
			BuiltinTools:                []string{"Read", "Bash", "Skill"},
			DisableProviderNativeSkills: true,
		})
		e.AssertNoError(t, contracttest.EvidenceKey("toolbridge.cli_args_error"), err)
		if !slices.Contains(args, "--tools") {
			t.Fatalf("buildCLIArgs() args = %v, want provider-native --tools governance", args)
		}
		tools := args[slices.Index(args, "--tools")+1]
		if strings.Contains(tools, "Skill") {
			t.Fatalf("buildCLIArgs() --tools = %q, want Skill removed when native skills are disabled", tools)
		}
		e.RecordOutcome(t, contracttest.EvidenceToolbridgeDependency, contracttest.OutcomeEvidence{
			ObservedActionID: "claude-provider-native-tool-governance",
			StateBefore:      "builtin_tools:Read,Bash,Skill",
			StateAfter:       "builtin_tools:" + tools,
			DependencyName:   "provider_native_tool_governance",
			Profile:          contract.DependencyProfileTest,
		})
	}}
}

func claudeRuntimeReportContractCase() contracttest.Case {
	return contracttest.Case{Name: "stdio runtime report", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		reporter := &stubRuntimeReporter{}
		runtimeDriver := newDriver(nil, nil, reporter, nil, nil, nil, nil, nil).(*driver)
		if err := runtimeDriver.reportRuntime(" agent-runtime-contract "); err != nil {
			t.Fatalf("reportRuntime() error = %v", err)
		}
		e.AssertEqual(t, contracttest.EvidenceKey("runtime_report.port"), reporter.last.Port, 0)
		e.RecordRuntimeReport(t, contracttest.RuntimeReportEvidence{
			AgentID:   reporter.last.AgentID,
			Provider:  reporter.last.Provider,
			StdioMode: "stdio-no-control-port",
		})

		failingReporter := &stubRuntimeReporter{err: errors.New("runtime reporter down")}
		failingDriver := newDriver(nil, nil, failingReporter, nil, nil, nil, nil, nil).(*driver)
		err := failingDriver.reportRuntime("agent-runtime-contract")
		if err == nil {
			t.Fatal("reportRuntime() error = nil, want explicit failure")
		}
		e.AssertEqual(t, contracttest.EvidenceKey("runtime_report.failure"), err.Error(), "runtime reporter down")
	}}
}

func claudeContractDriverWithLaunch(launchFn testLaunchCLI) *driver {
	return &driver{
		mirror:     &recordingMirrorReconciler{},
		launchCLI:  launchFn,
		authStatus: loggedInClaudeAuthStatus,
	}
}

func claudeStartPromptParityRequest(t *testing.T) dto.StartSessionRequest {
	t.Helper()
	fields := claudeStartPromptParityFields()
	boundary := claudeStartPromptBoundary()
	sections := claudeStartPromptSections()
	return dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-prompt-start",
		CWD:      t.TempDir(),
		StartAssembly: dto.StartAssembly{
			DisplayName:           "claude contract start",
			BaseInstructions:      fields.BaseInstructions,
			DeveloperInstructions: fields.DeveloperInstructions,
			Boundary:              boundary,
			PrefixShape:           dto.PrefixShape{Hash: fields.PrefixHash},
			Snapshot: dto.PromptAssemblySnapshot{
				DisplayName:           "claude contract start",
				BaseInstructions:      fields.BaseInstructions,
				DeveloperInstructions: fields.DeveloperInstructions,
				Boundary:              boundary,
				Provider:              "claude",
				Version:               contract.PromptAssemblySnapshotVersion,
				Hash:                  fields.PrefixHash,
				SectionSnapshot:       sections,
				Generation:            7,
			},
		},
		Config: map[string]any{"claudeHome": t.TempDir()},
	}
}

func claudeResumePromptParityRequest(t *testing.T) dto.ResumeSessionRequest {
	t.Helper()
	fields := claudeResumePromptParityFields()
	return dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-prompt-resume",
		ThreadID:         "public-thread-resume-contract",
		ProviderThreadID: "123e4567-e89b-12d3-a456-426614174000",
		CWD:              t.TempDir(),
		ClaudeHome:       t.TempDir(),
		PromptSnapshot: dto.PromptAssemblySnapshot{
			DisplayName:           "claude contract resume",
			BaseInstructions:      fields.BaseInstructions,
			DeveloperInstructions: fields.DeveloperInstructions,
			Boundary:              claudeResumePromptBoundary(),
			Provider:              "claude",
			Version:               contract.PromptAssemblySnapshotVersion,
			Hash:                  fields.PrefixHash,
			SectionSnapshot:       claudeResumePromptSections(),
			Generation:            8,
		},
	}
}

func claudeStartPromptParityFields() contracttest.PromptParityFields {
	return contracttest.PromptParityFields{
		BaseInstructions:      "claude contract base instructions",
		DeveloperInstructions: "claude contract developer instructions",
		PrefixHash:            "claude-contract-prefix-hash",
		Boundary:              `{"cachedPrefix":"claude contract base instructions","uncachedTail":"claude contract runtime context"}`,
		SectionSnapshot:       `{"developer":"claude contract developer instructions","system":"claude contract base instructions"}`,
	}
}

func claudeResumePromptParityFields() contracttest.PromptParityFields {
	return contracttest.PromptParityFields{
		BaseInstructions:      "claude resume base instructions",
		DeveloperInstructions: "claude resume developer instructions",
		PrefixHash:            "claude-resume-prefix-hash",
		Boundary:              `{"cachedPrefix":"claude resume base instructions","uncachedTail":"claude resume runtime context"}`,
		SectionSnapshot:       `{"developer":"claude resume developer instructions","system":"claude resume base instructions"}`,
	}
}

func claudeStartPromptBoundary() *dto.PromptAssemblyBoundary {
	return &dto.PromptAssemblyBoundary{
		CachedPrefix: "claude contract base instructions",
		UncachedTail: "claude contract runtime context",
	}
}

func claudeResumePromptBoundary() *dto.PromptAssemblyBoundary {
	return &dto.PromptAssemblyBoundary{
		CachedPrefix: "claude resume base instructions",
		UncachedTail: "claude resume runtime context",
	}
}

func claudeStartPromptSections() map[string]string {
	return map[string]string{
		"developer": "claude contract developer instructions",
		"system":    "claude contract base instructions",
	}
}

func claudeResumePromptSections() map[string]string {
	return map[string]string{
		"developer": "claude resume developer instructions",
		"system":    "claude resume base instructions",
	}
}

func promptParityFieldsFromSnapshot(t *testing.T, snapshot contract.PromptAssemblySnapshot) contracttest.PromptParityFields {
	t.Helper()
	return contracttest.PromptParityFields{
		BaseInstructions:      snapshot.BaseInstructions,
		DeveloperInstructions: snapshot.DeveloperInstructions,
		PrefixHash:            snapshot.Hash,
		Boundary:              canonicalJSONForContract(t, snapshot.Boundary),
		SectionSnapshot:       canonicalJSONForContract(t, snapshot.SectionSnapshot),
	}
}

func canonicalJSONForContract(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal contract evidence: %v", err)
	}
	return string(raw)
}

type claudeContractSession struct {
	*claudeContractSessionState
	claudeContractTurnOps
	claudeContractThreadOps
	claudeContractLifecycle
}

func newClaudeContractSession(threadID string) *claudeContractSession {
	state := &claudeContractSessionState{threadID: threadID}
	return &claudeContractSession{
		claudeContractSessionState: state,
		claudeContractLifecycle:    claudeContractLifecycle{state: state},
	}
}

type claudeContractSessionState struct {
	threadID string
	closed   bool
}

func (s *claudeContractSessionState) ThreadID() string { return s.threadID }

func (s *claudeContractSessionState) RolloutPath() string {
	return "/tmp/claude-contract-rollout.jsonl"
}

func (s *claudeContractSessionState) Capabilities() dto.CapabilitySet { return dto.CapabilitySet{} }

type claudeContractTurnOps struct{}

func (claudeContractTurnOps) StartTurn(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	localID := strings.TrimSpace(req.LocalID)
	if localID == "" {
		localID = "turn-contract"
	}
	return newClosedClaudeContractTurnHandle(localID, "claude-"+localID), nil
}

func (claudeContractTurnOps) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (claudeContractTurnOps) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}

func (claudeContractTurnOps) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return []dto.Message{}, nil
}

func (claudeContractTurnOps) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

type claudeContractThreadOps struct{}

func (claudeContractThreadOps) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, contract.NewCapabilityError(dto.CapThreadList, "claude")
}

func (claudeContractThreadOps) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, contract.NewCapabilityError(dto.CapThreadFork, "claude")
}

type claudeContractLifecycle struct {
	state *claudeContractSessionState
}

func (l claudeContractLifecycle) Close(context.Context) error {
	l.state.closed = true
	return nil
}

func (l claudeContractLifecycle) ForceStop() error {
	l.state.closed = true
	return nil
}

type claudeContractTurnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
}

func newClosedClaudeContractTurnHandle(localID, providerID string) *claudeContractTurnHandle {
	done := make(chan struct{})
	close(done)
	return &claudeContractTurnHandle{localID: localID, providerID: providerID, done: done}
}

func (h *claudeContractTurnHandle) LocalID() string { return h.localID }

func (h *claudeContractTurnHandle) ProviderID() string { return h.providerID }

func (h *claudeContractTurnHandle) Done() <-chan struct{} { return h.done }

func (h *claudeContractTurnHandle) Err() error { return nil }

var _ contract.Session = (*claudeContractSession)(nil)
var _ contract.TurnHandle = (*claudeContractTurnHandle)(nil)
