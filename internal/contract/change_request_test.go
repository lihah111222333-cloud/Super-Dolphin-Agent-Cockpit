package contract

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestChangeRequestJSONRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 22, 10, 30, 0, 0, time.UTC)
	want := ChangeRequest{
		ID:            "cr_123",
		WorkflowRunID: "run_456",
		Branch:        "codex/workflow-runtime-template-mr",
		Commits: []ChangeRequestCommit{
			{SHA: "abc123", Title: "feat: 增加模板产品化", Author: "codex", CreatedAt: now},
		},
		Checks: []ChangeRequestCheck{
			{Name: "frontend lint", Status: ChangeRequestCheckStatusPassed, URL: "https://ci.example/checks/1", UpdatedAt: now},
		},
		ReviewGate: ChangeRequestReviewGate{
			Status:      ChangeRequestReviewGateBlocked,
			Reviewer:    "reviewer",
			BlockingIDs: []string{"thread-1"},
		},
		External: ChangeRequestExternalRef{
			Provider: "gitlab",
			Kind:     "merge_request",
			URL:      "https://gitlab.example/project/-/merge_requests/7",
			ID:       "7",
		},
		Status:    ChangeRequestStatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal(ChangeRequest) error = %v", err)
	}
	var got ChangeRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(ChangeRequest) error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}
