package classifier

import (
	"strings"
	"testing"
)

func TestParseClassifierOutput_BareJSON(t *testing.T) {
	t.Parallel()
	key, reason, err := parseClassifierOutput(`{"prompt_key":"main/sql","reason":"user asked for a SQL query"}`)
	if err != nil {
		t.Fatalf("parse err = %v", err)
	}
	if key != "main/sql" {
		t.Fatalf("prompt_key = %q, want main/sql", key)
	}
	if reason != "user asked for a SQL query" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestParseClassifierOutput_MarkdownFence(t *testing.T) {
	t.Parallel()
	raw := "```json\n{\"prompt_key\":\"main/writing\",\"reason\":\"email draft\"}\n```\n"
	key, reason, err := parseClassifierOutput(raw)
	if err != nil {
		t.Fatalf("parse err = %v", err)
	}
	if key != "main/writing" {
		t.Fatalf("prompt_key = %q, want main/writing", key)
	}
	if reason != "email draft" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestParseClassifierOutput_EmptyPick(t *testing.T) {
	t.Parallel()
	key, reason, err := parseClassifierOutput(`{"prompt_key":"","reason":"no strong match"}`)
	if err != nil {
		t.Fatalf("parse err = %v", err)
	}
	if key != "" {
		t.Fatalf("prompt_key = %q, want empty", key)
	}
	if reason != "no strong match" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestParseClassifierOutput_Invalid(t *testing.T) {
	t.Parallel()
	_, _, err := parseClassifierOutput("sorry, I can't do that")
	if err == nil {
		t.Fatal("want error for non-JSON output, got nil")
	}
}

func TestBuildClassifierPrompt_IncludesKeysAndTriggers(t *testing.T) {
	t.Parallel()
	in := Input{
		UserInput: "帮我写个 SQL 查询",
		Candidates: []Candidate{
			{PromptKey: "main/sql", Title: "SQL 与数据建模专家", Description: "queries, schema", Tags: []string{"写 SQL", "JOIN 查询"}},
			{PromptKey: "main/writing", Title: "写作助手", Tags: []string{"写邮件"}},
		},
	}
	out := buildClassifierPrompt(in)
	if !strings.Contains(out, "帮我写个 SQL 查询") {
		t.Fatalf("user input missing from prompt:\n%s", out)
	}
	if !strings.Contains(out, "prompt_key=main/sql") {
		t.Fatalf("sql candidate missing:\n%s", out)
	}
	if !strings.Contains(out, "prompt_key=main/writing") {
		t.Fatalf("writing candidate missing:\n%s", out)
	}
	if !strings.Contains(out, "triggers: 写 SQL, JOIN 查询") {
		t.Fatalf("sql triggers missing:\n%s", out)
	}
}

func TestNewService_DisabledReturnsNoop(t *testing.T) {
	t.Parallel()
	c := NewService(Config{Enabled: false})
	if c.Enabled() {
		t.Fatal("disabled config must yield Enabled()=false")
	}
	if _, ok := c.(NoopClassifier); !ok {
		t.Fatalf("expected NoopClassifier when disabled, got %T", c)
	}
}

func TestNewService_EnabledButNoBinaryReturnsNoop(t *testing.T) {
	t.Parallel()
	c := NewService(Config{Enabled: true, Binary: "definitely-not-on-path-xyz"})
	if c.Enabled() {
		t.Fatal("missing binary must gracefully degrade to Enabled()=false")
	}
}

func TestStripCodeFence_Passthrough(t *testing.T) {
	t.Parallel()
	in := `{"prompt_key":"a","reason":"b"}`
	if got := stripCodeFence(in); got != in {
		t.Fatalf("stripCodeFence(bare) = %q, want unchanged", got)
	}
}
