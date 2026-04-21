// This test locks in the routing intent of the Orchestrator seed prompt
// (migration 0036_seed_orchestrator_prompt.sql). The tag set below mirrors
// what the migration inserts into prompt_templates.tags; if you change one,
// change the other \u2014 or this test fails loudly so intent stays traceable.
package router

import (
	"context"
	"strings"
	"testing"
)

func orchestratorCandidate() Candidate {
	return Candidate{
		PromptKey: "main/orchestrator",
		AgentKey:  "orchestrator",
		Title:     "Orchestrator",
		Tags: []string{
			"orchestrator",
			"orchestrate",
			"coordinate",
			"delegate",
			"multi-agent",
			"multi agent",
			"sub-agent",
			"sub agent",
			"team",
			"plan and delegate",
			"decompose",
			"break down",
		},
	}
}

func TestOrchestratorSeed_RoutesMultiAgentRequests(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	candidates := []Candidate{
		{PromptKey: "main/sql_expert", AgentKey: "sql_expert", Tags: []string{"sql", "database"}},
		orchestratorCandidate(),
	}

	cases := []string{
		"I need you to coordinate several experts on this",
		"decompose this into smaller tasks and delegate",
		"please orchestrate the refactor across multiple modules",
		"spawn a team of sub-agents to audit the repo",
	}
	for _, input := range cases {
		d, err := r.Classify(context.Background(), input, candidates)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", input, err)
		}
		if !d.Matched || d.AgentKey != "orchestrator" {
			t.Fatalf("%q: want orchestrator match, got %+v", input, d)
		}
	}
}

func TestOrchestratorSeed_DoesNotHijackSpecialistQueries(t *testing.T) {
	t.Parallel()
	r := NewRuleRouter()
	candidates := []Candidate{
		{PromptKey: "main/sql_expert", AgentKey: "sql_expert", Tags: []string{"sql", "database"}},
		orchestratorCandidate(),
	}

	// "write a sql query" should go to sql_expert, not get pulled into the
	// orchestrator just because its tag list is longer.
	d, _ := r.Classify(context.Background(), "write a sql query for me", candidates)
	if !d.Matched || d.AgentKey != "sql_expert" {
		t.Fatalf("expected sql_expert, got %+v", d)
	}
}

func TestOrchestratorMigration_TagsStayInSync(t *testing.T) {
	t.Parallel()
	// Sanity check: read the migration file and verify every tag listed above
	// appears in the seed row's tags jsonb literal. Guards against a silent
	// drift where someone edits the SQL but forgets to update this test (or
	// vice versa) \u2014 the "locked intent" holds.
	const relPath = "../../migrations/0036_seed_orchestrator_prompt.sql"
	body := mustReadFile(t, relPath)
	for _, tag := range orchestratorCandidate().Tags {
		if !strings.Contains(body, "\""+tag+"\"") {
			t.Fatalf("migration 0036 is missing tag %q \u2014 orchestrator routing will drift", tag)
		}
	}
}
