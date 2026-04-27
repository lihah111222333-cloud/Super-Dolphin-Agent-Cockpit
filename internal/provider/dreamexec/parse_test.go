package dreamexec

import (
	"strings"
	"testing"
)

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
