package router

import (
	"context"
	"testing"
)

func TestRuleRouter_MatchFirstTag(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()

	candidates := []Candidate{
		{PromptKey: "main/sql_expert", AgentKey: "sql_expert", Tags: []string{"SQL", "database"}},
		{PromptKey: "main/frontend_expert", AgentKey: "frontend_expert", Tags: []string{"React", "CSS"}},
	}

	d, err := r.Classify(context.Background(), "please write a SQL query for me", candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Matched || d.AgentKey != "sql_expert" || d.PromptKey != "main/sql_expert" {
		t.Fatalf("want matched=true agent=sql_expert prompt=main/sql_expert, got %+v", d)
	}
	if d.Confidence != 1.0 {
		t.Fatalf("rule-based confidence should be 1.0, got %v", d.Confidence)
	}
}

func TestRuleRouter_CaseInsensitive(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()

	candidates := []Candidate{{PromptKey: "main/sql", AgentKey: "sql_expert", Tags: []string{"sql"}}}

	d, _ := r.Classify(context.Background(), "SELECT * FROM users", candidates)
	// "SELECT" is not a tag, "sql" is — but "sql" is not a substring of
	// "select * from users" (case-insensitive). This test asserts no false
	// positive from a casing shortcut.
	if d.Matched {
		t.Fatalf("should not match: got %+v", d)
	}

	d, _ = r.Classify(context.Background(), "help with SQL", candidates)
	if !d.Matched || d.AgentKey != "sql_expert" {
		t.Fatalf("want matched=true agent=sql_expert, got %+v", d)
	}
}

func TestRuleRouter_PriorityIsCandidateOrder(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()

	// Both candidates contain "test"; first-in-list wins.
	candidates := []Candidate{
		{PromptKey: "main/unit_tester", AgentKey: "unit_tester", Tags: []string{"test"}},
		{PromptKey: "main/qa_engineer", AgentKey: "qa_engineer", Tags: []string{"test"}},
	}

	d, _ := r.Classify(context.Background(), "write some tests please", candidates)
	if !d.Matched || d.AgentKey != "unit_tester" || d.PromptKey != "main/unit_tester" {
		t.Fatalf("first-match-wins: want unit_tester, got %+v", d)
	}
}

func TestRuleRouter_NoMatch(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()

	d, err := r.Classify(context.Background(), "hello world", []Candidate{
		{PromptKey: "p1", AgentKey: "sql", Tags: []string{"database"}},
	})
	if err != nil {
		t.Fatalf("no-match must not error: %v", err)
	}
	if d.Matched {
		t.Fatalf("want Matched=false, got %+v", d)
	}
}

func TestRuleRouter_EmptyInput(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	d, err := r.Classify(context.Background(), "   ", []Candidate{
		{PromptKey: "p1", AgentKey: "x", Tags: []string{"x"}},
	})
	if err != nil {
		t.Fatalf("empty input must not error: %v", err)
	}
	if d.Matched {
		t.Fatalf("empty input must not match: %+v", d)
	}
}

func TestRuleRouter_EmptyTagsSkipped(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	d, _ := r.Classify(context.Background(), "hello", []Candidate{
		{PromptKey: "p1", AgentKey: "x", Tags: []string{"", "   ", "hello"}},
	})
	if !d.Matched || d.AgentKey != "x" {
		t.Fatalf("blank tags should be skipped, valid tag should match: %+v", d)
	}
}

// TestRuleRouter_FallbackWhenNoSpecificMatch asserts that a candidate with
// len(effective tags)==0 is returned as the fallback when the user input
// doesn't match any specialist candidate. Operators use this to declare a
// default persona that ships whenever router can't classify.
func TestRuleRouter_FallbackWhenNoSpecificMatch(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	candidates := []Candidate{
		{PromptKey: "main/sql", AgentKey: "sql", Tags: []string{"database", "sql"}},
		{PromptKey: "main/default", AgentKey: "main", Tags: []string{}}, // fallback
	}
	d, err := r.Classify(context.Background(), "write hello world", candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Matched || d.PromptKey != "main/default" || d.AgentKey != "main" {
		t.Fatalf("want fallback=main/default, got %+v", d)
	}
	if d.Confidence >= 1.0 {
		t.Fatalf("fallback confidence should be lower than a specific match, got %v", d.Confidence)
	}
}

// TestRuleRouter_FallbackSkippedWhenSpecificMatches asserts that a specialist
// tag match beats the fallback even when the fallback appears first in the
// candidate list. This prevents the fallback from stealing traffic whenever
// any specialist would have fired.
func TestRuleRouter_FallbackSkippedWhenSpecificMatches(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	candidates := []Candidate{
		{PromptKey: "main/default", AgentKey: "main", Tags: nil}, // fallback first
		{PromptKey: "main/sql", AgentKey: "sql", Tags: []string{"sql"}},
	}
	d, _ := r.Classify(context.Background(), "help with SQL", candidates)
	if !d.Matched || d.AgentKey != "sql" {
		t.Fatalf("specialist should beat fallback, got %+v", d)
	}
}

// TestRuleRouter_AllBlankTagsTreatedAsFallback asserts that a candidate whose
// tags are only blank strings (e.g. ["", " "]) is treated as a fallback
// candidate, since its effective tag count is zero. This guards against SQL
// rows that survive a half-edit where tags were cleared but not removed.
func TestRuleRouter_AllBlankTagsTreatedAsFallback(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	candidates := []Candidate{
		{PromptKey: "main/blank", AgentKey: "blank", Tags: []string{"", "  "}},
	}
	d, _ := r.Classify(context.Background(), "random input", candidates)
	if !d.Matched || d.PromptKey != "main/blank" {
		t.Fatalf("candidate with only blank tags should be the fallback, got %+v", d)
	}
}

// TestRuleRouter_StructuralScopeTagsInert asserts that scope.* tags do not
// participate in keyword matching and do not disqualify a candidate from the
// fallback pool. This is the fix for the prod symptom where UI-created
// prompts auto-tagged with "scope.cwd:." could never be picked by the
// classifier: the tag didn't match user input, and its presence excluded the
// candidate from fallback, leaving main/default (no tags) always winning.
func TestRuleRouter_StructuralScopeTagsInert(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	candidates := []Candidate{
		// The user-authored persona has only a structural scope tag.
		{PromptKey: "main/xiaobai", AgentKey: "main", Tags: []string{"scope.cwd:."}},
		{PromptKey: "main/default", AgentKey: "main", Tags: []string{}},
	}
	d, _ := r.Classify(context.Background(), "你是谁", candidates)
	if !d.Matched || d.PromptKey != "main/xiaobai" {
		t.Fatalf("structural-only tags should keep candidate in fallback pool, got %+v", d)
	}
}

// TestRuleRouter_StructuralScopeTagDoesNotMatchSubstring asserts the UI's
// scope directive text never accidentally matches via substring — if a user
// typed "scope.cwd:." into the chat, the router must not pick that candidate
// because of the tag, only by its real content tags.
func TestRuleRouter_StructuralScopeTagDoesNotMatchSubstring(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	candidates := []Candidate{
		{PromptKey: "main/scoped", AgentKey: "x", Tags: []string{"scope.cwd:."}},
		{PromptKey: "main/default", AgentKey: "main", Tags: nil},
	}
	d, _ := r.Classify(context.Background(), "please set scope.cwd:. for this run", candidates)
	if !d.Matched || d.PromptKey != "main/scoped" {
		// Even when user mentions the literal "scope.cwd:.", the scope tag
		// must stay inert and main/scoped only wins via the fallback pool.
		t.Fatalf("structural tag must not tag-match user input; fallback should pick main/scoped, got %+v", d)
	}
	if d.Reason == "rule: tag=scope.cwd:." {
		t.Fatalf("decision reason should not attribute match to a structural tag: %+v", d)
	}
}
