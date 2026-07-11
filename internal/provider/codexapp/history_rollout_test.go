package codexapp

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
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

func TestParseRolloutLineDropsTurnAbortedControlBlock(t *testing.T) {
	raw := []byte(`{"timestamp":"2026-05-30T07:30:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<turn_aborted>\nThe user interrupted the previous turn on purpose. Any running unified exec processes may still be running in the background. If any tools/commands were aborted, they may have partially executed.\n</turn_aborted>"}]}}`)

	if _, ok := parseRolloutLine(raw); ok {
		t.Fatal("parseRolloutLine() ok = true, want false for turn-aborted control block")
	}
}
