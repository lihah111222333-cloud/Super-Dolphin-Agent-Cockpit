package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/contracttest"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestCodexAppProviderContract(t *testing.T) {
	configureCaptureRuntimeHookForTest(t, func(providershared.ToolResultMeta, string) (providershared.ToolResultRecord, error) {
		return providershared.ToolResultRecord{}, nil
	})
	contracttest.Run(t, CompleteCodexAppContractSpec())
}

func TestCodexAppNativeSuccessCarriesTrustedSummary(t *testing.T) {
	payload := map[string]any{
		"timestamp": "2026-07-07T01:02:03Z", "threadId": "thread-codex-contract",
		"agentId": "agent-codex-contract", "turnId": "turn-codex-contract",
		"success": true, "status": "completed", "summary": "public contract success",
	}
	outcome := canonicalTurnTerminalOutcome("turn/completed", payload)
	event, ok := translateTurnEvent("turn/completed", payload, &outcome)
	completed, typed := event.(turndto.TurnCompleted)
	if !ok || !typed {
		t.Fatalf("translateTurnEvent() = (%T, %v), want TurnCompleted", event, ok)
	}
	terminal, err := turndto.NewTurnTerminalV2(completed, "codex-native-success")
	if err != nil {
		t.Fatalf("NewTurnTerminalV2() error = %v", err)
	}
	if terminal.PublicSummary != "public contract success" {
		t.Fatalf("terminal.PublicSummary = %q, want public contract success", terminal.PublicSummary)
	}
}

func CompleteCodexAppContractSpec() contracttest.Spec {
	return contracttest.Spec{
		Name:       "codex",
		Start:      startCodexAppContractSession,
		Resume:     resumeCodexAppContractSession,
		EventCases: codexAppEventTranslationContractCases(),
		RequiredCases: map[contracttest.CaseKey]contracttest.Case{
			contracttest.CaseEventMatrix:               codexAppEventMatrixContractCase(),
			contracttest.CasePromptMaterializedCarrier: codexAppPromptMaterializedCarrierContractCase(),
			contracttest.CaseApproval:                  codexAppApprovalContractCase(),
			contracttest.CaseInterrupt:                 codexAppInterruptContractCase(),
			contracttest.CaseForceComplete:             codexAppForceCompleteContractCase(),
			contracttest.CaseResume:                    codexAppResumeIdentityContractCase(),
			contracttest.CaseToolbridge:                codexAppToolbridgeContractCase(),
			contracttest.CaseDynamicToolResponder:      codexAppDynamicToolResponderContractCase(),
			contracttest.CaseRuntimeReport:             codexAppRuntimeReportContractCase(),
		},
	}
}

func startCodexAppContractSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	env := newCodexAppContractDriverEnv()
	session, err := env.driver.StartSession(ctx, req)
	if err != nil {
		env.close()
		return nil, err
	}
	return &codexAppContractHarnessSession{Session: session, cleanup: env.close}, nil
}

func resumeCodexAppContractSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	if strings.TrimSpace(req.ProviderThreadID) == "" {
		return nil, errors.New("codexapp contract resume requires provider thread id")
	}
	env := newCodexAppContractDriverEnv()
	session, err := env.driver.ResumeSession(ctx, req)
	if err != nil {
		env.close()
		return nil, err
	}
	return &codexAppContractHarnessSession{Session: session, cleanup: env.close}, nil
}

func codexAppEventTranslationContractCases() []contracttest.Case {
	cases := codexAppTurnEventTranslationContractCases()
	return append(cases, codexAppToolEventTranslationContractCases()...)
}

func codexAppTurnEventTranslationContractCases() []contracttest.Case {
	return []contracttest.Case{
		codexAppEventTranslationContractCase("turn completed translation", "turn_completed", dto.RawProviderEvent{
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
		}),
		codexAppEventTranslationContractCase("turn interrupted translation", "turn_interrupted", dto.RawProviderEvent{
			EventType: "turn/interrupted",
			Data: map[string]any{
				"timestamp": "2026-07-07T01:02:03Z",
				"threadId":  "thread-codex-contract",
				"agentId":   "agent-codex-contract",
				"turnId":    "turn-codex-contract",
				"error":     "user_interrupt",
			},
		}),
		codexAppEventTranslationContractCase("turn failed translation", "turn_failed", dto.RawProviderEvent{
			EventType: "turn/failed",
			Data: map[string]any{
				"timestamp": "2026-07-07T01:02:03Z",
				"threadId":  "thread-codex-contract",
				"agentId":   "agent-codex-contract",
				"turnId":    "turn-codex-contract",
				"status":    "failed",
				"error":     "provider failed",
				"message":   "provider failed",
			},
		}),
	}
}

func codexAppToolEventTranslationContractCases() []contracttest.Case {
	return []contracttest.Case{
		codexAppEventTranslationContractCase("tool end failed translation", "tool_end_failed", dto.RawProviderEvent{
			EventType: "item/completed",
			Data: map[string]any{
				"timestamp": "2026-07-07T01:02:03Z",
				"threadId":  "thread-codex-contract",
				"agentId":   "agent-codex-contract",
				"turnId":    "turn-codex-contract",
				"callId":    "call-codex-contract",
				"name":      "grep",
				"result": map[string]any{
					"success": false,
					"error":   "grep failed",
				},
			},
		}),
		codexAppEventTranslationContractCase("approval requested translation", "approval_requested", dto.RawProviderEvent{
			EventType: "item/commandExecution/requestApproval",
			Data: map[string]any{
				"timestamp": "2026-07-07T01:02:03Z",
				"threadId":  "thread-codex-contract",
				"agentId":   "agent-codex-contract",
				"turnId":    "turn-codex-contract",
				"callId":    "call-codex-contract",
				"toolName":  "shell",
				"requestId": 41,
				"reason":    "needs review",
			},
		}),
		codexAppEventTranslationContractCase("approval resolved translation", "approval_resolved", dto.RawProviderEvent{
			EventType: "approval/resolved",
			Data: map[string]any{
				"timestamp":  "2026-07-07T01:02:03Z",
				"threadId":   "thread-codex-contract",
				"agentId":    "agent-codex-contract",
				"turnId":     "turn-codex-contract",
				"callId":     "call-codex-contract",
				"toolName":   "shell",
				"approvalId": "approval-codex-contract",
				"approved":   true,
				"decision":   "approved",
				"reviewedBy": "contract",
			},
		}),
		codexAppEventTranslationContractCase("tool diff translation", "tool_diff_updated", dto.RawProviderEvent{
			EventType: "turn/diff/updated",
			Data: map[string]any{
				"timestamp": "2026-07-07T01:02:03Z",
				"threadId":  "thread-codex-contract",
				"agentId":   "agent-codex-contract",
				"diff":      "diff --git a/contract b/contract\n",
			},
		}),
	}
}

func codexAppEventTranslationContractCase(name, snapshotID string, raw dto.RawProviderEvent) contracttest.Case {
	return contracttest.Case{Name: name, Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		got := contracttest.CaptureProviderEventTranslation(t, "codex-"+snapshotID+"-capture", raw, translateCodexAdapterEvent)
		want := contracttest.NewExpectedEventEvidence(contracttest.LoadExpectedEventSnapshot(t, snapshotID))
		e.RecordEventTranslation(t, name, got, want)
	}}
}

func codexAppEventMatrixContractCase() contracttest.Case {
	return contracttest.Case{Name: "event translation matrix", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		e.RecordEventMatrix(t, contracttest.EventMatrixEvidence{
			Provider: "codex",
			Categories: []contracttest.EventMatrixCategoryEvidence{
				{Category: "interrupt", SnapshotIDs: []string{"turn_interrupted"}, TranslatorID: "translateCodexEvent"},
				{Category: "tool_end", SnapshotIDs: []string{"tool_end_failed"}, TranslatorID: "translateCodexEvent"},
				{Category: "failed_or_status", SnapshotIDs: []string{"turn_failed", "turn_completed"}, TranslatorID: "translateCodexEvent"},
				{Category: "approval_or_tool_diff", SnapshotIDs: []string{"approval_requested", "approval_resolved", "tool_diff_updated"}, TranslatorID: "translateCodexEvent"},
			},
		})
	}}
}

func codexAppPromptMaterializedCarrierContractCase() contracttest.Case {
	return contracttest.Case{Name: "start and resume materialized prompt carrier", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		startGot := captureCodexAppStartPromptParity(t)
		startWant := contracttest.NewExpectedPromptEvidence(contracttest.LoadExpectedPromptSnapshot(t, "start_prompt_parity"))
		e.RecordPromptMaterializedCarrier(t, startGot, startWant)

		resumeGot := captureCodexAppResumePromptParity(t)
		resumeWant := contracttest.NewExpectedPromptEvidence(contracttest.LoadExpectedPromptSnapshot(t, "resume_prompt_parity"))
		e.RecordPromptMaterializedCarrier(t, resumeGot, resumeWant)
	}}
}

func captureCodexAppStartPromptParity(t *testing.T) contracttest.PromptParityEvidence {
	t.Helper()
	req := codexAppStartPromptParityRequest(t)
	env := newCodexAppContractDriverEnv()
	t.Cleanup(env.close)
	session, err := env.driver.StartSession(context.Background(), req)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	params := decodeCodexAppContractThreadStartParams(t, env.recorder.lastParams(t, "thread/start"))
	fields := contracttest.PromptParityFields{
		BaseInstructions:      params.BaseInstructions,
		DeveloperInstructions: params.DeveloperInstructions,
	}
	return contracttest.NewProviderPromptEvidence("codex-start-rpc-payload-capture", fields)
}

func captureCodexAppResumePromptParity(t *testing.T) contracttest.PromptParityEvidence {
	t.Helper()
	req := codexAppResumePromptParityRequest(t)
	env := newCodexAppContractDriverEnv()
	t.Cleanup(env.close)
	session, err := env.driver.ResumeSession(context.Background(), req)
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	params := decodeCodexAppContractThreadResumeParams(t, env.recorder.lastParams(t, "thread/resume"))
	fields := contracttest.PromptParityFields{
		BaseInstructions:      params.BaseInstructions,
		DeveloperInstructions: params.DeveloperInstructions,
	}
	return contracttest.NewProviderPromptEvidence("codex-resume-rpc-payload-capture", fields)
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
				return mustJSON(map[string]any{
					"thread":         map[string]any{"id": req.ProviderThreadID},
					"approvalPolicy": "on-request",
				})
			default:
				return mustJSON(map[string]any{"ok": true})
			}
		})
		reporter := &stubRuntimeReporter{}
		d := &driver{approvals: testApprovalManager(), pool: newSingleURLPoolForTest(t, serverURL), reporter: reporter, mirror: &recordingSkillMirrorReconciler{}}
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
		got := requireToolBridgeDriver(t, newDriver(nil, nil, testApprovalManager(), nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
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

func codexAppDynamicToolResponderContractCase() contracttest.Case {
	return contracttest.Case{Name: "dynamic tool responder", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		ctx := context.Background()
		manager := &ServerManager{}
		var successCalled bool
		var errorCalled bool
		manager.SetToolHandler(func(_ context.Context, msg RawMessage) (any, error) {
			name := toolCallParamString(msg.Params, "name")
			switch name {
			case "contract_success":
				successCalled = true
				return map[string]any{"success": true, "result": "ok"}, nil
			case "contract_error":
				errorCalled = true
				return nil, errors.New("contract dynamic tool failed")
			default:
				return nil, errors.New("unexpected contract tool " + name)
			}
		})
		s := newInboundTestSession(ctx, nil, manager)

		successResp := newRecordingResponder()
		s.onInboundMessage(ctx, successResp, RawMessage{
			ID:     json.RawMessage(`"req-success"`),
			Method: "dynamic_tool_call",
			Params: json.RawMessage(`{"name":"contract_success","arguments":{"value":1},"turnId":"turn-contract","callId":"call-success"}`),
		})
		successCall := waitResponseCall(t, successResp.ch)

		errorResp := newRecordingResponder()
		s.onInboundMessage(ctx, errorResp, RawMessage{
			ID:     json.RawMessage(`"req-error"`),
			Method: "tools/call",
			Params: json.RawMessage(`{"name":"contract_error","arguments":{"value":2},"turnId":"turn-contract","callId":"call-error"}`),
		})
		errorCall := waitResponseCall(t, errorResp.ch)
		if !successCalled || !errorCalled {
			t.Fatalf("dynamic tool handler calls success=%t error=%t", successCalled, errorCalled)
		}
		if string(successCall.id) != `"req-success"` || successCall.err != nil {
			t.Fatalf("success dynamic tool response = id %s err %v, want req-success nil", successCall.id, successCall.err)
		}
		if string(errorCall.id) != `"req-error"` || errorCall.err == nil {
			t.Fatalf("error dynamic tool response = id %s err %v, want req-error error", errorCall.id, errorCall.err)
		}

		e.RecordDynamicToolResponder(t, contracttest.DynamicToolResponderEvidence{
			ToolName:               "contract_success",
			CallID:                 "call-success",
			SuccessResponseID:      string(successCall.id),
			SuccessResponsePayload: "success=true",
			ErrorResponseID:        string(errorCall.id),
			ErrorResponsePayload:   "error_returned=" + strconv.FormatBool(errorCall.err != nil),
		})
	}}
}

func codexAppRuntimeReportContractCase() contracttest.Case {
	return contracttest.Case{Name: "session url runtime report", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		t.Helper()
		reporter := &stubRuntimeReporter{}
		d := &driver{approvals: testApprovalManager(), reporter: reporter}
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

type codexAppContractDriverEnv struct {
	driver   *driver
	pool     *ServerPool
	recorder *codexAppContractRPCRecorder
	close    func()
}

func newCodexAppContractDriverEnv() *codexAppContractDriverEnv {
	recorder := newCodexAppContractRPCRecorder()
	serverURL, closeServer := startCodexAppContractRPCServer(recorder)
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		return newFakeServer(serverURL), nil
	}, PoolConfig{})
	env := &codexAppContractDriverEnv{pool: pool, recorder: recorder}
	env.close = func() {
		_ = pool.Close(context.Background())
		closeServer()
	}
	env.driver = &driver{
		approvals: testApprovalManager(),
		pool:      pool,
		mirror:    &recordingSkillMirrorReconciler{},
		listTools: func(context.Context) ([]codexprotocol.DynamicToolSchema, error) { return nil, nil },
	}
	return env
}

func startCodexAppContractRPCServer(recorder *codexAppContractRPCRecorder) (string, func()) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serveCodexAppContractRPCConnection(conn, recorder)
	}))
	return "ws" + strings.TrimPrefix(server.URL, "http"), server.Close
}

func serveCodexAppContractRPCConnection(conn *websocket.Conn, recorder *codexAppContractRPCRecorder) {
	defer conn.Close()
	for {
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msg, ok := decodeCodexTestRPCMessage(rawBytes)
		if !ok {
			continue
		}
		if recorder != nil {
			recorder.record(msg)
		}
		if err := writeCodexAppContractRPCResponse(conn, msg.ID, codexAppContractRPCResult(msg)); err != nil {
			return
		}
	}
}

func writeCodexAppContractRPCResponse(conn *websocket.Conn, id json.RawMessage, result json.RawMessage) error {
	resp, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), id...)),
		"result":  json.RawMessage(append([]byte(nil), result...)),
	})
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, resp)
}

type codexAppContractRPCRecorder struct {
	mu     sync.Mutex
	params map[string][]json.RawMessage
}

func newCodexAppContractRPCRecorder() *codexAppContractRPCRecorder {
	return &codexAppContractRPCRecorder{params: map[string][]json.RawMessage{}}
}

func (r *codexAppContractRPCRecorder) record(msg jsonRPCMessage) {
	if r == nil {
		return
	}
	method := strings.TrimSpace(msg.Method)
	if method == "" {
		return
	}
	params := append(json.RawMessage(nil), msg.Params...)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.params[method] = append(r.params[method], params)
}

func (r *codexAppContractRPCRecorder) lastParams(t *testing.T, method string) json.RawMessage {
	t.Helper()
	if r == nil {
		t.Fatalf("codex contract RPC recorder is nil")
	}
	method = strings.TrimSpace(method)
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := r.params[method]
	if len(calls) == 0 {
		t.Fatalf("codex contract RPC did not capture %s params; captured methods=%v", method, codexAppContractCapturedMethods(r.params))
	}
	return append(json.RawMessage(nil), calls[len(calls)-1]...)
}

func codexAppContractCapturedMethods(params map[string][]json.RawMessage) []string {
	methods := make([]string, 0, len(params))
	for method := range params {
		methods = append(methods, method)
	}
	return methods
}

func codexAppContractRPCResult(msg jsonRPCMessage) json.RawMessage {
	switch strings.TrimSpace(msg.Method) {
	case "thread/start":
		return mustJSON(map[string]any{"thread": map[string]any{"id": "provider-thread-contract-start"}})
	case "thread/resume":
		params := decodeCodexContractParamsNoT(msg.Params)
		threadID, _ := params["threadId"].(string)
		if strings.TrimSpace(threadID) == "" {
			threadID = "provider-thread-contract"
		}
		return mustJSON(map[string]any{
			"thread":         map[string]any{"id": threadID},
			"approvalPolicy": "on-request",
		})
	case "thread/fork":
		return mustJSON(map[string]any{"thread": map[string]any{"id": "provider-thread-contract-fork"}})
	case "model/list":
		return validCodexModelListResult()
	case "turn/start":
		return mustJSON(map[string]any{"turn": map[string]any{"id": "provider-turn-contract"}})
	case "turn/interrupt", "turn/forceComplete", "shutdown":
		return mustJSON(map[string]any{"ok": true})
	default:
		return mustJSON(map[string]any{"ok": true})
	}
}

func decodeCodexAppContractThreadStartParams(t *testing.T, raw json.RawMessage) threadStartParams {
	t.Helper()
	var params threadStartParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode thread/start params: %v", err)
	}
	return params
}

func decodeCodexAppContractThreadResumeParams(t *testing.T, raw json.RawMessage) threadResumeParams {
	t.Helper()
	var params threadResumeParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode thread/resume params: %v", err)
	}
	return params
}

func decodeCodexContractParamsNoT(raw json.RawMessage) map[string]any {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return map[string]any{}
	}
	return params
}

type codexAppContractHarnessSession struct {
	contract.Session
	cleanup func()
	once    sync.Once
}

func (s *codexAppContractHarnessSession) Close(ctx context.Context) error {
	err := s.Session.Close(ctx)
	s.closeHarness()
	return err
}

func (s *codexAppContractHarnessSession) ForceStop() error {
	err := s.Session.ForceStop()
	s.closeHarness()
	return err
}

func (s *codexAppContractHarnessSession) closeHarness() {
	s.once.Do(func() {
		if s.cleanup != nil {
			s.cleanup()
		}
	})
}
