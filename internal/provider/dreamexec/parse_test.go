package dreamexec

import (
	"strings"
	"testing"
)

// claudeEnvelopeFixture 来自实测：claude -p --output-format json "say hi"。
// 只保留 dream 使用到的字段以防上游 schema 漂移很快击穿测试。
const claudeEnvelopeFixture = `{"type":"result","is_error":false,"result":"Hi!","usage":{"input_tokens":6,"cache_creation_input_tokens":7409,"cache_read_input_tokens":16153,"output_tokens":8}}`

// codexJSONLFixture 来自实测：echo "say hi" | codex exec --json。
// 保留 reasoning + agent_message + turn.completed 三类 event 的完整路径。
const codexJSONLFixture = `Reading prompt from stdin...
{"type":"thread.started","thread_id":"019dce62-856b-7bf1-84d5-425b5fa04014"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"thinking..."}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"hi"}}
{"type":"turn.completed","usage":{"input_tokens":11920,"cached_input_tokens":5504,"output_tokens":92}}
`

func TestExtractClaudeEnvelope(t *testing.T) {
	text, usage, err := ExtractClaudeEnvelope([]byte(claudeEnvelopeFixture))
	if err != nil {
		t.Fatalf("ExtractClaudeEnvelope err = %v", err)
	}
	if text != "Hi!" {
		t.Errorf("text: got %q, want %q", text, "Hi!")
	}
	// 主线 parseLogLineUsage 语义：InputTokens = input + cacheCreation + cacheRead = 6 + 7409 + 16153 = 23568
	if usage.InputTokens != 23568 {
		t.Errorf("InputTokens: got %d, want 23568", usage.InputTokens)
	}
	if usage.OutputTokens != 8 {
		t.Errorf("OutputTokens: got %d, want 8", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 16153 {
		t.Errorf("CacheReadTokens: got %d, want 16153", usage.CacheReadTokens)
	}
}

func TestExtractClaudeEnvelopeRejectsErrorEnvelope(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"is_error true", `{"type":"result","is_error":true,"result":""}`},
		{"unexpected type", `{"type":"error","is_error":false,"result":"x"}`},
		{"empty result", `{"type":"result","is_error":false,"result":"  "}`},
		{"invalid json", `not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ExtractClaudeEnvelope([]byte(tc.in)); err == nil {
				t.Errorf("want error for %s, got nil", tc.name)
			}
		})
	}
}

func TestExtractCodexJSONL(t *testing.T) {
	text, usage, err := ExtractCodexJSONL([]byte(codexJSONLFixture))
	if err != nil {
		t.Fatalf("ExtractCodexJSONL err = %v", err)
	}
	if text != "hi" {
		t.Errorf("text: got %q, want %q", text, "hi")
	}
	if usage.InputTokens != 11920 {
		t.Errorf("InputTokens: got %d, want 11920", usage.InputTokens)
	}
	if usage.OutputTokens != 92 {
		t.Errorf("OutputTokens: got %d, want 92", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 5504 {
		t.Errorf("CacheReadTokens: got %d, want 5504", usage.CacheReadTokens)
	}
}

func TestExtractCodexJSONLPicksLastAgentMessage(t *testing.T) {
	// 多个 agent_message 取最后一个（防御：dream single-turn 下需要带但未来 multi-turn 可能出现）。
	in := `{"type":"item.completed","item":{"type":"agent_message","text":"first"}}
{"type":"item.completed","item":{"type":"agent_message","text":"final"}}
{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":2}}
`
	text, _, err := ExtractCodexJSONL([]byte(in))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if text != "final" {
		t.Errorf("text: got %q, want %q", text, "final")
	}
}

func TestExtractCodexJSONLRejectsNoAgentMessage(t *testing.T) {
	// 只有 reasoning 不是有效输出。
	in := `{"type":"item.completed","item":{"type":"reasoning","text":"..."}}
{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":2}}
`
	if _, _, err := ExtractCodexJSONL([]byte(in)); err == nil {
		t.Errorf("want error when no agent_message, got nil")
	}
}

func TestExtractCodexJSONLAllowsMissingUsage(t *testing.T) {
	// usage 缺失不报错，返回零值。上报 0 不污染 dreammetrics counter。
	in := `{"type":"item.completed","item":{"type":"agent_message","text":"hi"}}
{"type":"turn.completed"}
`
	text, usage, err := ExtractCodexJSONL([]byte(in))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if text != "hi" {
		t.Errorf("text: got %q", text)
	}
	if usage != (TokenUsage{}) {
		t.Errorf("usage: got %+v, want zero", usage)
	}
}

func TestStripJSONFences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no fence returns trimmed input", `  {"a":1}  `, `{"a":1}`},
		{"json fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"JSON uppercase fence", "```JSON\n{\"a\":1}\n```", `{"a":1}`},
		{"plain backticks no language", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"jsonc fence (any tag accepted)", "```jsonc\n{\"a\":1}\n```", `{"a":1}`},
		{"fence with surrounding whitespace", "  \n```json\n{\"a\":1}\n```\n  ", `{"a":1}`},
		{"fence with CRLF", "```json\r\n{\"a\":1}\r\n```", `{"a":1}`},
		{"fence with no trailing fence (truncated)", "```json\n{\"a\":1}", `{"a":1}`},
		{"single-line backticks no body", "```", ``},
		{"empty input", "", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripJSONFences(tc.in)
			if got != tc.want {
				t.Fatalf("StripJSONFences(%q)\ngot:  %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractFirstJSONObject(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      string
		expectErr bool
	}{
		{"simple object", `{"a":1}`, `{"a":1}`, false},
		{"nested object", `{"a":{"b":2},"c":3}`, `{"a":{"b":2},"c":3}`, false},
		{"strings with braces", `{"text":"} not real {","x":1}`, `{"text":"} not real {","x":1}`, false},
		{"escaped quote in string", `{"text":"says \"hi\"","x":1}`, `{"text":"says \"hi\"","x":1}`, false},
		{"escaped backslash before quote", `{"path":"C:\\Users\\","x":1}`, `{"path":"C:\\Users\\","x":1}`, false},
		{"prose preamble before JSON", `Here is the result:\n{"a":1}\nThanks.`, `{"a":1}`, false},
		{"first object wins", `{"a":1}{"b":2}`, `{"a":1}`, false},
		{"deeply nested", `{"a":{"b":{"c":{"d":1}}}}`, `{"a":{"b":{"c":{"d":1}}}}`, false},
		{"no opening brace", `[1,2,3]`, ``, true},
		{"unbalanced missing close", `{"a":1`, ``, true},
		{"empty input", ``, ``, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractFirstJSONObject(tc.in)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("ExtractFirstJSONObject(%q) expected error, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractFirstJSONObject(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ExtractFirstJSONObject(%q)\ngot:  %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripAndExtract_Composed(t *testing.T) {
	// LLM 实战常见输出：fence + 多对象 + prose preamble
	in := "Sure, here's the JSON:\n```json\n{\"memories\":[{\"content\":\"x\",\"type\":\"user\"}]}\n```\n"
	stripped := StripJSONFences(in)
	got, err := ExtractFirstJSONObject(stripped)
	if err != nil {
		t.Fatalf("composed pipeline failed: %v", err)
	}
	if !strings.Contains(got, `"memories"`) || !strings.Contains(got, `"x"`) {
		t.Fatalf("composed pipeline output missing fields: %q", got)
	}
}
