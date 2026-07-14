package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRestoreApprovalPolicyRejectsUnverifiedRemoteConfig(t *testing.T) {
	tests := []struct {
		name   string
		result json.RawMessage
	}{
		{name: "wrong top level type", result: mustJSON("invalid")},
		{name: "missing effective", result: mustJSON(map[string]any{})},
		{name: "wrong effective type", result: mustJSON(map[string]any{"effective": "invalid"})},
		{name: "missing approvals", result: mustJSON(map[string]any{"effective": map[string]any{}})},
		{name: "wrong approvals type", result: mustJSON(map[string]any{"effective": map[string]any{"approvals": true}})},
		{name: "empty approvals", result: mustJSON(map[string]any{"effective": map[string]any{"approvals": " "}})},
		{name: "unknown approvals", result: mustJSON(map[string]any{"effective": map[string]any{"approvals": "sometimes"}})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
				if method == "thread/config/get" {
					return tt.result
				}
				return mustJSON(map[string]any{"ok": true})
			})
			d := approvalRestoreDriverForTest(t, serverURL)
			_, err := d.ResumeSession(context.Background(), codexResumeRequestForRuntimeReportTest(t))
			if err == nil || !strings.Contains(err.Error(), "approval policy") {
				t.Fatalf("ResumeSession() error = %v, want approval policy validation error", err)
			}
		})
	}
}

func TestRestoreApprovalPolicyRejectsRPCError(t *testing.T) {
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		if method == "thread/config/get" {
			return nil
		}
		if method == "thread/resume" {
			return mustJSON(map[string]any{"thread": map[string]any{"id": "provider-thread-1"}})
		}
		return mustJSON(map[string]any{"ok": true})
	})
	d := approvalRestoreDriverForTest(t, serverURL)

	_, err := d.ResumeSession(context.Background(), codexResumeRequestForRuntimeReportTest(t))
	if err == nil || !strings.Contains(err.Error(), "approval policy remote verification failed") {
		t.Fatalf("ResumeSession() error = %v, want approval policy RPC error", err)
	}
}

func TestRestoreApprovalPolicyMarksKnownPolicyVerified(t *testing.T) {
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		if method == "thread/config/get" {
			return mustJSON(map[string]any{"effective": map[string]any{"approvals": "on-request"}})
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
	if !s.approvalPolicyVerified.Load() {
		t.Fatal("approvalPolicyVerified = false after verified remote config")
	}
}

func approvalRestoreDriverForTest(t *testing.T, serverURL string) *driver {
	t.Helper()
	return &driver{
		approvals: testApprovalManager(),
		pool:      newSingleURLPoolForTest(t, serverURL),
		mirror:    &recordingSkillMirrorReconciler{},
		listTools: noopCodexToolLister,
	}
}

func TestBuildApprovalRequestRejectsUnverifiedPolicy(t *testing.T) {
	s := &session{agentID: "agent-1"}
	s.approvalPolicy.Store("never")

	if _, _, ok := s.buildApprovalRequest("request_user_input", map[string]any{"requestId": int64(1)}); ok {
		t.Fatal("buildApprovalRequest() ok = true for unverified approval policy")
	}
}
