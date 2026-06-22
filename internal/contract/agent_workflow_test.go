package contract

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestWorkflowPlanJSONRoundTrip(t *testing.T) {
	runID := int64(42)
	now := agentWorkflowTestTime()
	assertJSONRoundTrip(t, WorkflowPlan{
		PlanKey:            "plan-1",
		WorkflowRunID:      &runID,
		DagKey:             "dag-1",
		Goal:               "ship workflow layer",
		NonGoals:           []string{"change dispatcher"},
		Risks:              []string{"schema drift"},
		AcceptanceCriteria: []string{"tools registered"},
		EvalList:           []string{"targeted tests"},
		AllowedWriteScope:  []string{"cmd/mcp-orch/tools"},
		ExpectedArtifacts:  []ExpectedArtifact{{Name: "patch", Kind: ArtifactKindPatch}},
		Status:             WorkflowPlanStatusActive,
		CreatedBy:          "worker-d",
		UpdatedBy:          "worker-d",
		CreatedAt:          now,
		UpdatedAt:          now,
	}, &WorkflowPlan{})
}

func TestAgentTaskJSONRoundTrip(t *testing.T) {
	runID := int64(42)
	now := agentWorkflowTestTime()
	assertJSONRoundTrip(t, AgentTask{
		TaskKey:             "task-1",
		PlanKey:             "plan-1",
		WorkflowRunID:       &runID,
		DagKey:              "dag-1",
		NodeKey:             "node-1",
		Role:                AgentRoleImplementer,
		Title:               "implement store",
		InputContext:        []ContextRef{{Kind: "doc", Ref: "plan.md"}},
		OutputContract:      "small verified diff",
		VerificationCommand: "go test ./cmd/mcp-orch/store/agentworkflow",
		Budget:              AgentTaskBudget{MaxMinutes: 15, MaxTokens: 12000},
		DependsOn:           []string{"task-0"},
		OutputArtifactKeys:  []string{"artifact-1"},
		Status:              AgentTaskStatusRunning,
		AssignedAgent:       "worker-d",
		CreatedBy:           "planner",
		UpdatedBy:           "worker-d",
		CreatedAt:           now,
		UpdatedAt:           now,
	}, &AgentTask{})
}

func TestReviewAndCrossValidationJSONRoundTrip(t *testing.T) {
	now := agentWorkflowTestTime()
	assertJSONRoundTrip(t, ReviewGate{
		GateKey:           "gate-1",
		PlanKey:           "plan-1",
		TaskKey:           "task-1",
		Reviewer:          "reviewer-a",
		TargetArtifactKey: "artifact-1",
		BlockingFindings:  []ReviewFinding{{FindingKey: "f-1", Severity: "high", Summary: "missing test"}},
		NonBlockingFindings: []ReviewFinding{
			{FindingKey: "f-2", Severity: "low", Summary: "wording"},
		},
		ReReviewState: ReviewGateReReviewRequested,
		PassCondition: "targeted tests pass",
		Status:        ReviewGateStatusChangesRequested,
		CreatedAt:     now,
		UpdatedAt:     now,
		ResolvedAt:    &now,
	}, &ReviewGate{})
	assertJSONRoundTrip(t, CrossValidation{
		ValidationKey:        "validation-1",
		PlanKey:              "plan-1",
		TargetArtifactKey:    "artifact-1",
		IndependentReviewers: []string{"reviewer-a", "reviewer-b"},
		Disagreements:        []ReviewDisagreement{{Topic: "scope", Positions: []string{"ok", "too broad"}}},
		Evidence:             []EvidenceRef{{Kind: "test", Ref: "go test", Summary: "passed"}},
		ArbitrationResult:    "keep minimal scope",
		Status:               CrossValidationStatusArbitrated,
		CreatedAt:            now,
		UpdatedAt:            now,
	}, &CrossValidation{})
}

func TestHandoffArtifactAcceptanceJSONRoundTrip(t *testing.T) {
	runID := int64(42)
	now := agentWorkflowTestTime()
	assertJSONRoundTrip(t, HandoffPackage{
		HandoffKey:     "handoff-1",
		PlanKey:        "plan-1",
		CurrentGoal:    "finish tools",
		CompletedWork:  []string{"contract"},
		AttemptedPaths: []string{"sqlc first"},
		FailureEvidence: []EvidenceRef{
			{Kind: "log", Ref: "guard output", Summary: "missing comment"},
		},
		ResidualRisks: []string{"full suite not run"},
		NextActions:   []string{"run targeted tests"},
		CreatedBy:     "worker-d",
		CreatedAt:     now,
	}, &HandoffPackage{})
	assertJSONRoundTrip(t, WorkflowArtifact{
		ArtifactKey:   "artifact-1",
		PlanKey:       "plan-1",
		TaskKey:       "task-1",
		WorkflowRunID: &runID,
		Kind:          ArtifactKindPatch,
		URI:           "shared:workflow/patch.diff",
		Lifecycle:     ArtifactLifecycleCandidate,
		ProducedBy:    "worker-d",
		CreatedAt:     now,
		UpdatedAt:     now,
	}, &WorkflowArtifact{})
	assertJSONRoundTrip(t, AcceptanceRecord{
		AcceptanceKey: "accept-1",
		PlanKey:       "plan-1",
		UserAccepted:  true,
		AutomatedVerification: []VerificationResult{
			{Command: "go test ./...", Status: "passed", Summary: "targeted"},
		},
		ReviewGateKeys: []string{"gate-1"},
		ResidualRisks:  []string{"manual UI not checked"},
		Status:         AcceptanceStatusAcceptedWithRisk,
		CreatedAt:      now,
	}, &AcceptanceRecord{})
}

func assertJSONRoundTrip[T any](t *testing.T, value T, out *T) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(*out, value) {
		t.Fatalf("round trip mismatch\nraw: %s\ngot: %#v\nwant:%#v", raw, *out, value)
	}
}

func agentWorkflowTestTime() time.Time {
	return time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
}
