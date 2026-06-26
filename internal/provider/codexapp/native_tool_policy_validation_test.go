package codexapp

import (
	"context"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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
			codexDisabledNativeToolsConfigKey: value,
		},
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
	if !strings.Contains(message, codexDisabledNativeToolsConfigKey) {
		t.Fatalf("error = %v, want %s validation detail", err, codexDisabledNativeToolsConfigKey)
	}
	if !strings.Contains(message, "must be []string or []any of strings") {
		t.Fatalf("error = %v, want strict string list validation detail", err)
	}
}

func TestNativeToolPolicyRejectsInvalidListTypes(t *testing.T) {
	for _, tt := range invalidNativeToolPolicyCases() {
		t.Run(tt.name, func(t *testing.T) {
			assertNativeToolPolicyValidationError(t, resolveInvalidNativeToolPolicy(tt.value))
		})
	}
}
