package codexapp

import "testing"

func TestParseRolloutLineTrimsInjectedAndSystemNoise(t *testing.T) {
	raw := []byte(`{"timestamp":"2026-03-21T01:02:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\nignored\n</environment_context>\nhello world\n[skill:planner]\n摘要: do planning\n使用方式: use planner first\n已注入 LSP mandatory prefix"}]}}`)

	msg, ok := parseRolloutLine(raw, testSkillMetrics(t))
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

	if _, ok := parseRolloutLine(raw, testSkillMetrics(t)); ok {
		t.Fatal("parseRolloutLine() ok = true, want false for pure system noise")
	}
}

func TestParseRolloutLineDropsTurnAbortedControlBlock(t *testing.T) {
	raw := []byte(`{"timestamp":"2026-05-30T07:30:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<turn_aborted>\nThe user interrupted the previous turn on purpose. Any running unified exec processes may still be running in the background. If any tools/commands were aborted, they may have partially executed.\n</turn_aborted>"}]}}`)

	if _, ok := parseRolloutLine(raw, testSkillMetrics(t)); ok {
		t.Fatal("parseRolloutLine() ok = true, want false for turn-aborted control block")
	}
}

func TestParseRolloutLineCountsMissingSkillFooter(t *testing.T) {
	metrics := testSkillMetrics(t)
	raw := []byte(`{"timestamp":"2026-03-21T01:02:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"question\n[skill:foo::full@v1]\nbody without closing footer"}]}}`)

	msg, ok := parseRolloutLine(raw, metrics)
	if !ok || msg.Content != "question" {
		t.Fatalf("parseRolloutLine() = (%#v, %t), want trimmed question", msg, ok)
	}
	if got := metrics.Snapshot().TrimCorruptionFallbackCount; got != 1 {
		t.Fatalf("TrimCorruptionFallbackCount = %d, want 1", got)
	}
}
