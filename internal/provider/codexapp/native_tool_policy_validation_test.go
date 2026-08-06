package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// invalidNativeToolPolicyCases 覆盖会被旧逻辑静默吞掉或当成单项字符串的错误形态。
func invalidNativeToolPolicyCases() []struct {
	name  string
	value any
} {
	return []struct {
		name  string
		value any
	}{
		{name: "bare string", value: "shell"},
		{name: "mixed array", value: []any{"shell", 42}},
		{name: "integer", value: 42},
		{name: "object", value: map[string]any{"shell": true}},
	}
}

// resolveInvalidNativeToolPolicy 走 StartSession 会调用的 session option 解析路径。
func resolveInvalidNativeToolPolicy(value any) error {
	_, err := (&driver{}).resolveSessionOptions(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-1",
		Config: map[string]any{
			contract.RuntimeConfigCodexDisabledNativeTools().Canonical: value,
		},
	})
	return err
}

// resolveInvalidResumeNativeToolPolicy 走 ResumeSession 会调用的 typed native tool 策略解析路径。
func resolveInvalidResumeNativeToolPolicy(ids []string) error {
	_, err := (&driver{}).resolveResumeOptions(context.Background(), dto.ResumeSessionRequest{
		AgentID:                  "agent-1",
		CodexDisabledNativeTools: ids,
	})
	return err
}

// assertNativeToolPolicyValidationError 要求错误明确指向 codexDisabledNativeTools 配置形态。
func assertNativeToolPolicyValidationError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("resolveSessionOptions() error = nil, want invalid native tool policy")
	}
	message := err.Error()
	key := contract.RuntimeConfigCodexDisabledNativeTools().Canonical
	if !strings.Contains(message, key) {
		t.Fatalf("error = %v, want %s validation detail", err, key)
	}
	if !strings.Contains(message, "must be []string or []any of strings") {
		t.Fatalf("error = %v, want strict string list validation detail", err)
	}
}

// assertNativeToolPolicyUnknownError 要求 unknown native tool ID 在启动前 fail-fast。
func assertNativeToolPolicyUnknownError(t *testing.T, err error, unknown string) {
	t.Helper()
	if err == nil {
		t.Fatal("native tool policy error = nil, want unknown native tool ID")
	}
	message := err.Error()
	key := contract.RuntimeConfigCodexDisabledNativeTools().Canonical
	if !strings.Contains(message, key) {
		t.Fatalf("error = %v, want %s validation detail", err, key)
	}
	if !strings.Contains(message, unknown) {
		t.Fatalf("error = %v, want unknown native tool ID %q", err, unknown)
	}
}

func TestNativeToolPolicyRejectsInvalidListTypes(t *testing.T) {
	for _, tt := range invalidNativeToolPolicyCases() {
		t.Run(tt.name, func(t *testing.T) {
			assertNativeToolPolicyValidationError(t, resolveInvalidNativeToolPolicy(tt.value))
		})
	}
}

func TestNativeToolPolicyRejectsUnknownToolIDs(t *testing.T) {
	const unknown = "not_a_tool"
	cases := []struct {
		name  string
		value any
	}{
		{name: "typed string list", value: []string{unknown}},
		{name: "wire any list", value: []any{unknown}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assertNativeToolPolicyUnknownError(t, resolveInvalidNativeToolPolicy(tt.value), unknown)
		})
	}
}

func TestNativeToolPolicyRejectsUnknownTypedResumeToolIDs(t *testing.T) {
	const unknown = "not_a_tool"
	assertNativeToolPolicyUnknownError(t, resolveInvalidResumeNativeToolPolicy([]string{unknown}), unknown)
}

func TestCodexSandboxReadOnlyParsingIsConservative(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{name: "string read-only", raw: json.RawMessage(`"read-only"`), want: true},
		{name: "string readOnly", raw: json.RawMessage(`"readOnly"`), want: true},
		{name: "object read-only key", raw: json.RawMessage(`{"read-only":null}`), want: true},
		{name: "object readOnly key", raw: json.RawMessage(`{"readOnly":true}`), want: true},
		{name: "object mode read-only", raw: json.RawMessage(`{"mode":"read-only"}`), want: true},
		{name: "readOnlyHint only", raw: json.RawMessage(`{"readOnlyHint":true}`), want: false},
		{name: "workspace write", raw: json.RawMessage(`{"mode":"workspace-write"}`), want: false},
		{name: "malformed json", raw: json.RawMessage(`{"mode":`), want: false},
		{name: "empty", raw: nil, want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexSandboxIsReadOnly(tt.raw); got != tt.want {
				t.Fatalf("codexSandboxIsReadOnly(%s) = %v, want %v", string(tt.raw), got, tt.want)
			}
		})
	}
}
