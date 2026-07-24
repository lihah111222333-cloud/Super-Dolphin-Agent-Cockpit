package turn

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTurnStartParamsRejectsUnknownField(t *testing.T) {
	t.Parallel()

	var params turnStartParams
	err := json.Unmarshal([]byte(`{"threadId":"thread-1","cwd":"/repo/app","prompt":"hi","surprise":true}`), &params)
	if err == nil {
		t.Fatal("json.Unmarshal(turnStartParams) error = nil, want unknown-field rejection")
	}
	if !strings.Contains(err.Error(), `turn/start: unknown field "surprise"`) {
		t.Fatalf("json.Unmarshal(turnStartParams) error = %q", err)
	}
}

func TestTurnSteerParamsRejectsUnknownField(t *testing.T) {
	t.Parallel()

	var params turnSteerParams
	err := json.Unmarshal([]byte(`{"threadId":"thread-1","expectedTurnId":"turn-1","prompt":"hi","surprise":true}`), &params)
	if err == nil {
		t.Fatal("json.Unmarshal(turnSteerParams) error = nil, want unknown-field rejection")
	}
	if !strings.Contains(err.Error(), `turn/steer: unknown field "surprise"`) {
		t.Fatalf("json.Unmarshal(turnSteerParams) error = %q", err)
	}
}

func TestTurnParamsRejectUnknownSkillRefSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		payload   string
		newTarget func() any
		want      string
	}{
		{
			name:      "start",
			payload:   `{"threadId":"thread-1","selectedSkillRefs":[{"name":"docs","source":"robot"}]}`,
			newTarget: func() any { return &turnStartParams{} },
			want:      `turn/start: selected skill ref source "robot" is invalid`,
		},
		{
			name:      "steer",
			payload:   `{"threadId":"thread-1","selectedSkillRefs":[{"name":"docs","source":"robot"}]}`,
			newTarget: func() any { return &turnSteerParams{} },
			want:      `turn/steer: selected skill ref source "robot" is invalid`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := json.Unmarshal([]byte(tt.payload), tt.newTarget())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json.Unmarshal(%s params) error = %v, want %q", tt.name, err, tt.want)
			}
		})
	}
}

func TestTurnStartParamsAcceptsCamelRuntimeAliases(t *testing.T) {
	t.Parallel()

	var params turnStartParams
	err := json.Unmarshal([]byte(`{
		"threadID":"thread-1",
		"gitRoot":"/repo",
		"isWorktree":true,
		"enabledTools":["file"],
		"additionalWorkingDirectories":["/repo/extra"],
		"sessionFlags":{"verification_required":true}
	}`), &params)
	if err != nil {
		t.Fatalf("json.Unmarshal(turnStartParams) error = %v", err)
	}
	if params.ThreadID != "thread-1" || params.GitRoot != "/repo" || !params.IsWorktree {
		t.Fatalf("turnStartParams identity/runtime = %#v", params)
	}
	if len(params.EnabledTools) != 1 || params.EnabledTools[0] != "file" {
		t.Fatalf("EnabledTools = %#v, want file", params.EnabledTools)
	}
	if len(params.AdditionalWorkingDirectories) != 1 || params.AdditionalWorkingDirectories[0] != "/repo/extra" {
		t.Fatalf("AdditionalWorkingDirectories = %#v", params.AdditionalWorkingDirectories)
	}
	if !params.SessionFlags["verification_required"] {
		t.Fatalf("SessionFlags = %#v, want verification_required", params.SessionFlags)
	}
}

func TestTurnStartParamsRejectsConflictingBoolAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "manual skill selection",
			payload: `{"manual_skill_selection":false,"manualSkillSelection":true}`,
			want:    `conflicting manual skill selection values`,
		},
		{
			name:    "is worktree",
			payload: `{"is_worktree":false,"isWorktree":true}`,
			want:    `conflicting is worktree values`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var params turnStartParams
			err := json.Unmarshal([]byte(tt.payload), &params)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json.Unmarshal(turnStartParams) error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTurnStartParamsRejectsInvalidBoolAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "manual skill selection null",
			payload: `{"manualSkillSelection":null}`,
			want:    `turn/start: manualSkillSelection must be a boolean`,
		},
		{
			name:    "is worktree string",
			payload: `{"isWorktree":"true"}`,
			want:    `turn/start: isWorktree must be a boolean`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var params turnStartParams
			err := json.Unmarshal([]byte(tt.payload), &params)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json.Unmarshal(turnStartParams) error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTurnSteerParamsRejectsConflictingBoolAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "manual skill selection",
			payload: `{"thread_id":"thread-1","manual_skill_selection":false,"manualSkillSelection":true}`,
			want:    `conflicting manual skill selection values`,
		},
		{
			name:    "is worktree",
			payload: `{"thread_id":"thread-1","is_worktree":false,"isWorktree":true}`,
			want:    `conflicting is worktree values`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var params turnSteerParams
			err := json.Unmarshal([]byte(tt.payload), &params)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json.Unmarshal(turnSteerParams) error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTurnSteerParamsRejectsInvalidBoolAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "manual skill selection number",
			payload: `{"thread_id":"thread-1","manualSkillSelection":1}`,
			want:    `turn/steer: manualSkillSelection must be a boolean`,
		},
		{
			name:    "is worktree null",
			payload: `{"thread_id":"thread-1","isWorktree":null}`,
			want:    `turn/steer: isWorktree must be a boolean`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var params turnSteerParams
			err := json.Unmarshal([]byte(tt.payload), &params)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json.Unmarshal(turnSteerParams) error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTurnThreadScopedParamsRejectUnknownFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		payload   string
		newTarget func() any
		want      string
	}{
		{
			name:      "force complete",
			payload:   `{"threadId":"thread-1","surprise":true}`,
			newTarget: func() any { return &threadIDOnlyParams{} },
			want:      `turn/forceComplete: unknown field "surprise"`,
		},
		{
			name:      "approval respond",
			payload:   `{"requestId":7,"approved":true,"surprise":true}`,
			newTarget: func() any { return &approvalRespondParams{} },
			want:      `approval/respond: unknown field "surprise"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := json.Unmarshal([]byte(tt.payload), tt.newTarget())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json.Unmarshal(%s) error = %v, want %q", tt.name, err, tt.want)
			}
		})
	}
}

func TestApprovalRespondParamsPreservesCompositeIdentityAliases(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		`{"session_scope":"session-a","call_id":"call-a","request_id":7,"approved":true}`,
		`{"sessionScope":"session-a","callId":"call-a","requestId":7,"approved":true}`,
		`{"session_scope":"session-a","sessionScope":"session-a","call_id":"call-a","callId":"call-a","request_id":7,"requestId":7,"approved":true}`,
	} {
		var params approvalRespondParams
		if err := json.Unmarshal([]byte(payload), &params); err != nil {
			t.Fatalf("json.Unmarshal(approvalRespondParams) error = %v", err)
		}
		if params.SessionScope != "session-a" || params.CallID != "call-a" || params.RequestID == nil || *params.RequestID != 7 {
			t.Fatalf("approval identity = (%q, %q, %v), want (%q, %q, %d)", params.SessionScope, params.CallID, params.RequestID, "session-a", "call-a", 7)
		}
	}
}

func TestApprovalRespondParamsRejectsConflictingIdentityAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		payload string
		want    string
	}{
		{
			payload: `{"session_scope":"session-a","sessionScope":"session-b","call_id":"call-a","request_id":7,"approved":true}`,
			want:    "conflicting sessionScope values",
		},
		{
			payload: `{"session_scope":"session-a","call_id":"call-a","callId":"call-b","request_id":7,"approved":true}`,
			want:    "conflicting callId values",
		},
		{
			payload: `{"session_scope":"session-a","call_id":"call-a","request_id":7,"requestId":8,"approved":true}`,
			want:    "conflicting requestId values",
		},
		{
			payload: `{"session_scope":"","sessionScope":"session-a","call_id":"call-a","request_id":7,"approved":true}`,
			want:    "conflicting sessionScope values",
		},
	}
	for _, tt := range tests {
		var params approvalRespondParams
		err := json.Unmarshal([]byte(tt.payload), &params)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("json.Unmarshal(approvalRespondParams) error = %v, want %q", err, tt.want)
		}
	}
}
