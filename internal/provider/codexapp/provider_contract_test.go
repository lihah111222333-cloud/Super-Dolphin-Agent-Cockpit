package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/contracttest"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestCodexAppProviderContract(t *testing.T) {
	contracttest.Run(t, CompleteCodexAppContractSpec())
}

func CompleteCodexAppContractSpec() contracttest.Spec {
	return contracttest.Spec{
		Name:       "codex",
		Start:      startCodexAppContractSession,
		Resume:     resumeCodexAppContractSession,
		EventCases: []contracttest.Case{codexAppEventTranslationContractCase()},
		RequiredCases: map[contracttest.CaseKey]contracttest.Case{
			contracttest.CasePromptParity:  codexAppPromptParityContractCase(),
			contracttest.CaseApproval:      codexAppApprovalContractCase(),
			contracttest.CaseInterrupt:     codexAppInterruptContractCase(),
			contracttest.CaseForceComplete: codexAppForceCompleteContractCase(),
			contracttest.CaseResume:        codexAppResumeIdentityContractCase(),
			contracttest.CaseToolbridge:    codexAppToolbridgeContractCase(),
			contracttest.CaseRuntimeReport: codexAppRuntimeReportContractCase(),
		},
	}
}

func startCodexAppContractSession(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	threadID := strings.TrimSpace(req.AgentID)
	if threadID == "" {
		threadID = "codex-contract-start-thread"
	}
	return newCodexAppContractSession(threadID), nil
}

func resumeCodexAppContractSession(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	threadID := strings.TrimSpace(req.ProviderThreadID)
	if threadID == "" {
		return nil, errors.New("codexapp contract resume requires provider thread id")
	}
	return newCodexAppContractSession(threadID), nil
}

func codexAppEventTranslationContractCase() contracttest.Case {
	return contracttest.Case{Name: "turn completed translation", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		raw := dto.RawProviderEvent{
			EventType: "turn/completed",
			Data: map[string]any{
				"timestamp":   "2026-07-07T01:02:03Z",
				"threadId":    "thread-codex-contract",
				"agentId":     "agent-codex-contract",
				"turnId":      "turn-codex-contract",
				"success":     true,
				"status":      "completed",
				"reason":      "stop",
				"result":      "done",
				"stop_reason": "end_turn",
			},
		}
		got := contracttest.CaptureProviderEventTranslation(t, "codex-turn-completed-capture", raw, translateCodexEvent)
		want := contracttest.NewExpectedEventEvidence(contracttest.LoadExpectedEventSnapshot(t, "turn_completed"))
		e.RecordEventTranslation(t, "codex turn completed", got, want)
	}}
}

func codexAppPromptParityContractCase() contracttest.Case {
	return contracttest.Case{Name: "start and resume prompt parity", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		startGot := captureCodexAppStartPromptParity(t)
		startWant := contracttest.NewExpectedPromptEvidence(contracttest.LoadExpectedPromptSnapshot(t, "start_prompt_parity"))
		e.RecordPromptParity(t, startGot, startWant)

		resumeGot := captureCodexAppResumePromptParity(t)
		resumeWant := contracttest.NewExpectedPromptEvidence(contracttest.LoadExpectedPromptSnapshot(t, "resume_prompt_parity"))
		e.RecordPromptParity(t, resumeGot, resumeWant)
	}}
}

func captureCodexAppStartPromptParity(t *testing.T) contracttest.PromptParityEvidence {
	t.Helper()
	req := codexAppStartPromptParityRequest(t)
	params, err := (&driver{}).buildThreadStartParams(req)
	if err != nil {
		t.Fatalf("buildThreadStartParams() error = %v", err)
	}
	fields := contracttest.PromptParityFields{
		BaseInstructions:      params.BaseInstructions,
		DeveloperInstructions: params.DeveloperInstructions,
		PrefixHash:            req.StartAssembly.PrefixShape.Hash,
		Boundary:              codexAppContractJSON(t, req.StartAssembly.Boundary),
		SectionSnapshot:       codexAppContractJSON(t, req.StartAssembly.Snapshot.SectionSnapshot),
	}
	return contracttest.NewProviderPromptEvidence("codex-start-prompt-capture", fields)
}

func captureCodexAppResumePromptParity(t *testing.T) contracttest.PromptParityEvidence {
	t.Helper()
	req := codexAppResumePromptParityRequest(t)
	params, err := buildThreadResumeParams(req)
	if err != nil {
		t.Fatalf("buildThreadResumeParams() error = %v", err)
	}
	fields := contracttest.PromptParityFields{
		BaseInstructions:      params.BaseInstructions,
		DeveloperInstructions: params.DeveloperInstructions,
		PrefixHash:            req.PromptSnapshot.Hash,
		Boundary:              codexAppContractJSON(t, req.PromptSnapshot.Boundary),
		SectionSnapshot:       codexAppContractJSON(t, req.PromptSnapshot.SectionSnapshot),
	}
	return contracttest.NewProviderPromptEvidence("codex-resume-prompt-capture", fields)
}

func codexAppApprovalContractCase() contracttest.Case {
	return contracttest.Case{Name: "approval bridge response", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		requests := make(chan rpc.ApprovalRequest, 1)
		responses := make(chan map[string]any, 1)
		serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
			if strings.TrimSpace(msg.Method) == "approval/respond" {
				responses <- decodeCodexContractParams(t, msg.Params)
			}
			return mustJSON(map[string]any{"ok": true})
		})
		s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-approval-contract", nil, rpc.NewApprovalManager(nil, nil), nil)
		if err != nil {
			t.Fatalf("newSession() error = %v", err)
		}
		s.runtime.Start()
		s.setThreadID("provider-thread-approval")
		s.setApprovalPolicy("on-request")
		s.approvalDecisionHook = func(_ context.Context, req rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
			requests <- req
			approved := true
			return contract.ApprovalDecision{Approved: &approved, Reason: "contract-approved"}, nil
		}
		t.Cleanup(func() { closeCodexTestSession(t, s) })

		err = s.requestToolApprovalWithContext(context.Background(), "item/commandExecution/requestApproval", json.RawMessage(`{"requestId":41,"callId":"call-approval-contract","toolName":"shell","threadId":"provider-thread-approval","turnId":"turn-approval-contract","reason":"needs review"}`))
		if err != nil {
			t.Fatalf("requestToolApprovalWithContext() error = %v", err)
		}
		req := receiveCodexContractApprovalRequest(t, requests)
		response := receiveCodexContractParams(t, responses, "approval/respond")
		if req.CallID != "call-approval-contract" || response["requestId"] != float64(41) || response["approved"] != true {
			t.Fatalf("approval bridge req=%#v response=%#v", req, response)
		}
		e.RecordOutcome(t, contracttest.EvidenceApprovalOutcome, contracttest.OutcomeEvidence{
			ObservedActionID: "codex-approval-bridge-call-approval-contract",
			StateBefore:      "approval_policy:on-request;request:41",
			StateAfter:       "approved:" + strconv.FormatBool(response["approved"].(bool)) + ";call:" + req.CallID,
		})
	}}
}

func codexAppInterruptContractCase() contracttest.Case {
	return contracttest.Case{Name: "session interrupt sends active turn", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		paramsCh := make(chan map[string]any, 1)
		s := newInterruptTestSession(t, paramsCh)
		s.setThreadID("thread-interrupt-contract")
		s.mu.Lock()
		s.activeTurnID = "turn-interrupt-contract"
		s.mu.Unlock()
		if err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "contract"}); err != nil {
			t.Fatalf("Interrupt() error = %v", err)
		}
		params := receiveInterruptParams(t, paramsCh)
		if params["turnId"] != "turn-interrupt-contract" || params["source"] != "contract" {
			t.Fatalf("interrupt params = %#v", params)
		}
		e.RecordOutcome(t, contracttest.EvidenceInterruptOutcome, contracttest.OutcomeEvidence{
			ObservedActionID: "codex-turn-interrupt",
			StateBefore:      "active_turn:turn-interrupt-contract",
			StateAfter:       "sent_turn:" + params["turnId"].(string) + ";source:" + params["source"].(string),
		})
	}}
}

func codexAppForceCompleteContractCase() contracttest.Case {
	return contracttest.Case{Name: "session force complete closes active turn", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		fixture := newCountingForceCompleteFixture(t)
		defer fixture.close()
		s := newForceCompleteTestSession(t, fixture.url())
		s.runtime.Start()
		t.Cleanup(func() { closeCodexTestSession(t, s) })
		s.setThreadID("thread-force-contract")
		active := configureSingleForceCompleteTurn(s, "turn-force-contract")
		if err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: "thread-force-contract"}); err != nil {
			t.Fatalf("ForceComplete() error = %v", err)
		}
		assertTurnDone(t, active, "ForceComplete did not close active turn")
		e.RecordOutcome(t, contracttest.EvidenceForceCompleteOutcome, contracttest.OutcomeEvidence{
			ObservedActionID: "codex-turn-force-complete",
			StateBefore:      "active_turn:turn-force-contract",
			StateAfter:       "completed:turn-force-contract",
		})
	}}
}

func codexAppResumeIdentityContractCase() contracttest.Case {
	return contracttest.Case{Name: "provider thread id resume", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		req := codexAppResumePromptParityRequest(t)
		serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
			switch method {
			case "thread/resume":
				return mustJSON(map[string]any{"thread": map[string]any{"id": req.ProviderThreadID}})
			case "thread/config/get":
				return mustJSON(map[string]any{"effective": map[string]any{"approvals": "on-request"}})
			default:
				return mustJSON(map[string]any{"ok": true})
			}
		})
		reporter := &stubRuntimeReporter{}
		d := &driver{pool: newSingleURLPoolForTest(t, serverURL), reporter: reporter, mirror: &recordingSkillMirrorReconciler{}}
		resumed, err := d.ResumeSession(context.Background(), req)
		if err != nil {
			t.Fatalf("ResumeSession() error = %v", err)
		}
		s := mustCodexSession(t, resumed, "ResumeSession")
		defer closeCodexTestSession(t, s)
		e.RecordResumeIdentity(t, contracttest.ResumeIdentityEvidence{
			PublicThreadID:   req.ThreadID,
			ProviderThreadID: req.ProviderThreadID,
			ResumedThreadID:  s.ThreadID(),
		})
	}}
}

func codexAppToolbridgeContractCase() contracttest.Case {
	return contracttest.Case{Name: "dynamic toolbridge start surface", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		recorder := &toolBridgeRPCRecorder{}
		serverURL := startToolBridgeRPCServer(t, recorder)
		manager := &ServerManager{}
		listToolsCalls := 0
		got := requireToolBridgeDriver(t, newDriver(nil, nil, nil, nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
			listToolsCalls++
			return []codexprotocol.DynamicToolSchema{{
				Name:        "contract.echo",
				Description: "contract echo payload",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}}, nil
		}))
		sessionAny, err := got.StartSession(context.Background(), dto.StartSessionRequest{
			AgentID:       "agent-toolbridge-contract",
			CWD:           t.TempDir(),
			StartAssembly: validStartAssemblyForTest(),
		})
		if err != nil {
			t.Fatalf("StartSession() error = %v", err)
		}
		s := requireCodexSession(t, sessionAny, "StartSession")
		defer closeCodexTestSession(t, s)
		assertDynamicToolsStartSession(t, recorder, s, listToolsCalls)
		e.RecordOutcome(t, contracttest.EvidenceToolbridgeDependency, contracttest.OutcomeEvidence{
			ObservedActionID: "codex-dynamic-toolbridge-start",
			StateBefore:      "list_tools:configured",
			StateAfter:       "dynamic_tools:bound;calls:" + strconv.Itoa(listToolsCalls),
			DependencyName:   "codexapp.toolbridge.dynamic_tools",
			Profile:          contract.DependencyProfileTest,
		})
	}}
}

func codexAppRuntimeReportContractCase() contracttest.Case {
	return contracttest.Case{Name: "session url runtime report", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		reporter := &stubRuntimeReporter{}
		d := &driver{reporter: reporter}
		s := newRuntimeReportSessionForTest(" agent-1 ", " ws://127.0.0.1:4567/ws ")
		finishRuntimeReportSession(t, d, s)
		assertRuntimeReportFromSessionURL(t, reporter, s, 4567)
		e.RecordRuntimeReport(t, contracttest.RuntimeReportEvidence{
			AgentID:        reporter.last.AgentID,
			Provider:       reporter.last.Provider,
			SessionURLPort: strconv.Itoa(reporter.last.Port),
		})
	}}
}

func codexAppStartPromptParityRequest(t *testing.T) dto.StartSessionRequest {
	t.Helper()
	boundary := &dto.PromptAssemblyBoundary{
		CachedPrefix: "codex contract cached prefix",
		UncachedTail: "codex contract uncached tail",
	}
	sections := map[string]string{
		"developer": "codex contract developer instructions",
		"system":    "codex contract base instructions",
		"tools":     "codex contract tool instructions",
	}
	return dto.StartSessionRequest{
		Provider: "codex",
		AgentID:  "agent-prompt-start",
		CWD:      t.TempDir(),
		StartAssembly: dto.StartAssembly{
			DisplayName:           "codex contract start",
			BaseInstructions:      "codex contract base instructions",
			DeveloperInstructions: "codex contract developer instructions",
			Boundary:              boundary,
			PrefixShape:           dto.PrefixShape{Hash: "codex-contract-prefix-hash"},
			Snapshot: dto.PromptAssemblySnapshot{
				DisplayName:           "codex contract start",
				BaseInstructions:      "codex contract base instructions",
				DeveloperInstructions: "codex contract developer instructions",
				Boundary:              boundary,
				Provider:              "codex",
				Version:               contract.PromptAssemblySnapshotVersion,
				Hash:                  "codex-contract-prefix-hash",
				SectionSnapshot:       sections,
				Generation:            7,
			},
		},
	}
}

func codexAppResumePromptParityRequest(t *testing.T) dto.ResumeSessionRequest {
	t.Helper()
	boundary := &dto.PromptAssemblyBoundary{
		CachedPrefix: "codex resume cached prefix",
		UncachedTail: "codex resume uncached tail",
	}
	return dto.ResumeSessionRequest{
		Provider:         "codex",
		AgentID:          "agent-prompt-resume",
		ThreadID:         "public-thread-resume-contract",
		ProviderThreadID: "provider-thread-resume-contract",
		CWD:              t.TempDir(),
		CodexHome:        t.TempDir(),
		PromptSnapshot: dto.PromptAssemblySnapshot{
			DisplayName:           "codex contract resume",
			BaseInstructions:      "codex resume base instructions",
			DeveloperInstructions: "codex resume developer instructions",
			Boundary:              boundary,
			Provider:              "codex",
			Version:               contract.PromptAssemblySnapshotVersion,
			Hash:                  "codex-resume-prefix-hash",
			SectionSnapshot: map[string]string{
				"developer": "codex resume developer instructions",
				"system":    "codex resume base instructions",
			},
			Generation: 8,
		},
	}
}

func codexAppContractJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal codex contract evidence: %v", err)
	}
	return string(raw)
}

func decodeCodexContractParams(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode codex contract params: %v", err)
	}
	return params
}

func receiveCodexContractApprovalRequest(t *testing.T, requests <-chan rpc.ApprovalRequest) rpc.ApprovalRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval request")
		return rpc.ApprovalRequest{}
	}
}

func receiveCodexContractParams(t *testing.T, paramsCh <-chan map[string]any, name string) map[string]any {
	t.Helper()
	select {
	case params := <-paramsCh:
		return params
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s params", name)
		return nil
	}
}

type codexAppContractSession struct {
	*codexAppContractSessionIdentity
	codexAppContractTurnOps
	codexAppContractHistoryOps
	codexAppContractLifecycle
}

func newCodexAppContractSession(threadID string) *codexAppContractSession {
	return &codexAppContractSession{
		codexAppContractSessionIdentity: &codexAppContractSessionIdentity{threadID: strings.TrimSpace(threadID)},
	}
}

type codexAppContractSessionIdentity struct {
	threadID string
}

func (s *codexAppContractSessionIdentity) ThreadID() string { return s.threadID }

func (*codexAppContractSessionIdentity) RolloutPath() string {
	return "/tmp/codexapp-contract-rollout.jsonl"
}

func (*codexAppContractSessionIdentity) Capabilities() dto.CapabilitySet { return dto.CapabilitySet{} }

type codexAppContractTurnOps struct{}

func (codexAppContractTurnOps) StartTurn(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	localID := strings.TrimSpace(req.LocalID)
	if localID == "" {
		localID = "codex-contract-turn"
	}
	return newCodexAppContractTurnHandle(localID, "provider-"+localID), nil
}

func (codexAppContractTurnOps) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (codexAppContractTurnOps) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}

type codexAppContractHistoryOps struct{}

func (codexAppContractHistoryOps) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}

func (codexAppContractHistoryOps) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

func (codexAppContractHistoryOps) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, contract.NewCapabilityError(dto.CapThreadList, "codex")
}

func (codexAppContractHistoryOps) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, contract.NewCapabilityError(dto.CapThreadFork, "codex")
}

type codexAppContractLifecycle struct{}

func (codexAppContractLifecycle) Close(context.Context) error { return nil }

func (codexAppContractLifecycle) ForceStop() error { return nil }

type codexAppContractTurnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
}

func newCodexAppContractTurnHandle(localID, providerID string) *codexAppContractTurnHandle {
	done := make(chan struct{})
	close(done)
	return &codexAppContractTurnHandle{localID: localID, providerID: providerID, done: done}
}

func (h *codexAppContractTurnHandle) LocalID() string { return h.localID }

func (h *codexAppContractTurnHandle) ProviderID() string { return h.providerID }

func (h *codexAppContractTurnHandle) Done() <-chan struct{} { return h.done }

func (*codexAppContractTurnHandle) Err() error { return nil }

var _ contract.Session = (*codexAppContractSession)(nil)
var _ contract.TurnHandle = (*codexAppContractTurnHandle)(nil)
