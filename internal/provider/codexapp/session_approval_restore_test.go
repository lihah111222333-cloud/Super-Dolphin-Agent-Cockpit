package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeResumeResultRejectsUnverifiedApprovalPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy any
	}{
		{name: "missing", policy: nil},
		{name: "wrong type", policy: true},
		{name: "empty", policy: " "},
		{name: "unknown", policy: "sometimes"},
		{name: "incomplete granular", policy: map[string]any{"granular": map[string]any{"rules": true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := map[string]any{"thread": map[string]any{"id": "provider-thread-1"}}
			if tt.policy != nil {
				response["approvalPolicy"] = tt.policy
			}
			_, err := decodeResumeResult(mustJSON(response))
			if err == nil || !strings.Contains(err.Error(), "approvalPolicy") {
				t.Fatalf("decodeResumeResult() error = %v, want approvalPolicy validation error", err)
			}
		})
	}
}

func TestDecodeResumeResultAcceptsGranularApprovalPolicy(t *testing.T) {
	raw := mustJSON(map[string]any{
		"thread": map[string]any{"id": "provider-thread-1"},
		"approvalPolicy": map[string]any{"granular": map[string]any{
			"mcp_elicitations": true,
			"rules":            false,
			"sandbox_approval": true,
		}},
	})
	got, err := decodeResumeResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.threadID != "provider-thread-1" || !strings.Contains(got.approvalPolicy, `"granular"`) {
		t.Fatalf("decodeResumeResult() = %#v", got)
	}
}

func TestResumeApprovalPolicyComesFromResumeResponse(t *testing.T) {
	calls := make([]string, 0, 2)
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		calls = append(calls, method)
		if method == "thread/resume" {
			return mustJSON(map[string]any{
				"thread":         map[string]any{"id": "provider-thread-1"},
				"approvalPolicy": "on-request",
			})
		}
		return mustJSON(map[string]any{"ok": true})
	})
	d := approvalRestoreDriverForTest(t, serverURL)
	got, err := d.ResumeSession(context.Background(), codexResumeRequestForRuntimeReportTest(t))
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "ResumeSession")
	defer closeCodexTestSession(t, s)
	if !s.approvalPolicyVerified.Load() || s.approvalPolicyValue() != "on-request" {
		t.Fatalf("approval policy was not verified from resume response: %q", s.approvalPolicyValue())
	}
	for _, method := range calls {
		if method == "thread/config/get" {
			t.Fatal("ResumeSession called unsupported thread/config/get")
		}
	}
}

func approvalRestoreDriverForTest(t *testing.T, serverURL string) *driver {
	t.Helper()
	return &driver{logRuntime: testLoggerRuntime(t),
		approvals:    testApprovalManager(),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		skillMetrics: testSkillMetrics(t),
		listTools:    noopCodexToolLister,
	}
}

func TestBuildApprovalRequestRejectsUnverifiedPolicy(t *testing.T) {
	s := &session{agentID: "agent-1"}
	s.approvalPolicy.Store("never")

	if _, _, ok := s.buildApprovalRequest("request_user_input", map[string]any{"requestId": int64(1)}); ok {
		t.Fatal("buildApprovalRequest() ok = true for unverified approval policy")
	}
}
