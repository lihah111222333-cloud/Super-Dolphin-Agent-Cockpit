package gatehook

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecisionForStatusFixtures(t *testing.T) {
	type decisionFixture struct {
		Name           string    `json:"name"`
		Status         JobStatus `json:"status"`
		CurrentTreeSHA string    `json:"current_tree_sha"`
		WantBlock      string    `json:"want_block"`
		WantContinue   bool      `json:"want_continue"`
	}
	var fixtures []decisionFixture
	if err := json.Unmarshal([]byte(loadFixture(t, "status/decisions.json")), &fixtures); err != nil {
		t.Fatalf("decode decision fixtures: %v", err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			decision, err := DecisionForStatus(fixture.Status, fixture.CurrentTreeSHA)
			if err != nil {
				t.Fatalf("DecisionForStatus: %v", err)
			}
			if decision.Continue != fixture.WantContinue {
				t.Fatalf("continue = %v, want %v", decision.Continue, fixture.WantContinue)
			}
			if fixture.WantBlock != "" &&
				(decision.Decision != "block" || !strings.Contains(decision.Reason, fixture.WantBlock)) {
				t.Fatalf("block decision = %#v, want substring %q", decision, fixture.WantBlock)
			}
		})
	}
}

func TestDecisionForStatusRejectsPassedWithoutReceipt(t *testing.T) {
	_, err := DecisionForStatus(JobStatus{
		JobID: "job-passed", State: JobStatePassed, SourceTreeSHA: "tree-a",
	}, "tree-a")
	if err == nil || !strings.Contains(err.Error(), "receipt_id") {
		t.Fatalf("error = %v, want receipt_id", err)
	}
}
