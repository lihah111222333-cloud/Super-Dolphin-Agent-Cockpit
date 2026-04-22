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
	c := NewService(Config{Disabled: true})
	if c.Enabled() {
		t.Fatal("Disabled=true must yield Enabled()=false")
	}
	if _, ok := c.(NoopClassifier); !ok {
		t.Fatalf("expected NoopClassifier when Disabled=true, got %T", c)
	}
}

func TestNewService_MissingBinaryDegradesToNoop(t *testing.T) {
	t.Parallel()
	// Auto-detect path: no Disabled flag, but binary name that will never
	// be on PATH. Must degrade to NoopClassifier with Enabled()=false rather
	// than panicking or returning a classifier that errors at runtime.
	c := NewService(Config{Binary: "definitely-not-on-path-xyz"})
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

func TestPruneCandidates_NoopWhenBelowMax(t *testing.T) {
	t.Parallel()
	cands := []Candidate{{PromptKey: "a"}, {PromptKey: "b"}}
	got := PruneCandidates(cands, "anything", 5)
	if len(got) != 2 || got[0].PromptKey != "a" || got[1].PromptKey != "b" {
		t.Fatalf("expected unchanged list, got %+v", got)
	}
}

func TestPruneCandidates_NoopWhenMaxZero(t *testing.T) {
	t.Parallel()
	cands := []Candidate{{PromptKey: "a"}, {PromptKey: "b"}, {PromptKey: "c"}}
	got := PruneCandidates(cands, "hi", 0)
	if len(got) != 3 {
		t.Fatalf("max=0 must be noop, got %d", len(got))
	}
}

func TestPruneCandidates_RanksByTagOverlap(t *testing.T) {
	t.Parallel()
	cands := []Candidate{
		{PromptKey: "main/default", Tags: []string{}},
		{PromptKey: "main/writing", Tags: []string{"写邮件", "润色一下"}},
		{PromptKey: "main/sql", Tags: []string{"写 SQL", "JOIN 查询", "schema 设计"}},
		{PromptKey: "main/debug", Tags: []string{"报错", "stack trace"}},
	}
	got := PruneCandidates(cands, "帮我写个 JOIN 查询", 2)
	if len(got) != 2 {
		t.Fatalf("max=2 must keep exactly 2, got %d", len(got))
	}
	if got[0].PromptKey != "main/sql" {
		t.Fatalf("top-ranked should be main/sql (tag match), got %q", got[0].PromptKey)
	}
}

func TestPruneCandidates_TopUpEvenWhenAllZeroScore(t *testing.T) {
	t.Parallel()
	cands := []Candidate{
		{PromptKey: "a", Tags: []string{"foo"}},
		{PromptKey: "b", Tags: []string{"bar"}},
		{PromptKey: "c", Tags: []string{"baz"}},
	}
	got := PruneCandidates(cands, "hello world", 2)
	if len(got) != 2 {
		t.Fatalf("must top-up to max even on zero scores: got %d", len(got))
	}
	// Ties break by original order so the classifier sees a stable subset.
	if got[0].PromptKey != "a" || got[1].PromptKey != "b" {
		t.Fatalf("expected a,b (stable order), got %v,%v", got[0].PromptKey, got[1].PromptKey)
	}
}

func TestPruneCandidates_CaseInsensitive(t *testing.T) {
	t.Parallel()
	cands := []Candidate{
		{PromptKey: "main/review", Tags: []string{"Code Review", "review this"}},
		{PromptKey: "main/default"},
		{PromptKey: "main/sql", Tags: []string{"写 SQL"}},
	}
	got := PruneCandidates(cands, "help me CODE REVIEW this function", 1)
	if len(got) != 1 || got[0].PromptKey != "main/review" {
		t.Fatalf("expected case-insensitive match on code review, got %+v", got)
	}
}

func TestTagOverlapScore_EmptyInputs(t *testing.T) {
	t.Parallel()
	if s := tagOverlapScore(nil, "foo"); s != 0 {
		t.Fatalf("nil tags should score 0, got %d", s)
	}
	if s := tagOverlapScore([]string{"x"}, ""); s != 0 {
		t.Fatalf("empty input should score 0, got %d", s)
	}
}
