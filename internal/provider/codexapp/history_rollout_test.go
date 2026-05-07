package codexapp

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

func TestParseRolloutLineTrimsInjectedAndSystemNoise(t *testing.T) {
	providershared.SetTrimSkillBlocksHook(skill.TrimInjectedSkillBlocks)
	t.Cleanup(func() { providershared.SetTrimSkillBlocksHook(nil) })
	raw := []byte(`{"timestamp":"2026-03-21T01:02:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\nignored\n</environment_context>\nhello world\n[skill:planner]\n摘要: do planning\n使用方式: use planner first\n已注入 LSP mandatory prefix"}]}}`)

	msg, ok := parseRolloutLine(raw)
	if !ok {
		t.Fatal("parseRolloutLine() ok = false, want true")
	}
	if msg.Role != "user" {
		t.Fatalf("Role = %q, want user", msg.Role)
	}
	if msg.Content != "hello world" {
		t.Fatalf("Content = %q, want trimmed user text", msg.Content)
	}
}

func TestParseRolloutLineDropsPureSystemNoise(t *testing.T) {
	raw := []byte(`{"timestamp":"2026-03-21T01:02:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\nignored\n</environment_context>"}]}}`)

	if _, ok := parseRolloutLine(raw); ok {
		t.Fatal("parseRolloutLine() ok = true, want false for pure system noise")
	}
}
