package contracttest

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateAcceptanceSpecRejectsMissingCriterion(t *testing.T) {
	tests := []struct {
		name string
		omit []AcceptanceCriterion
		want string
	}{
		{name: "event matrix", omit: []AcceptanceCriterion{AcceptanceEventMatrix}, want: string(AcceptanceEventMatrix)},
		{name: "prompt alternative", omit: []AcceptanceCriterion{AcceptancePromptSnapshotParity, AcceptancePromptMaterialized}, want: "requires prompt_snapshot_parity or prompt_materialized_carrier"},
		{name: "approval", omit: []AcceptanceCriterion{AcceptanceApproval}, want: string(AcceptanceApproval)},
		{name: "interrupt", omit: []AcceptanceCriterion{AcceptanceInterrupt}, want: string(AcceptanceInterrupt)},
		{name: "force complete", omit: []AcceptanceCriterion{AcceptanceForceComplete}, want: string(AcceptanceForceComplete)},
		{name: "resume", omit: []AcceptanceCriterion{AcceptanceResume}, want: string(AcceptanceResume)},
		{name: "toolbridge", omit: []AcceptanceCriterion{AcceptanceToolbridge}, want: string(AcceptanceToolbridge)},
		{name: "dynamic tool responder", omit: []AcceptanceCriterion{AcceptanceDynamicToolResponder}, want: string(AcceptanceDynamicToolResponder)},
		{name: "runtime report", omit: []AcceptanceCriterion{AcceptanceRuntimeReport}, want: string(AcceptanceRuntimeReport)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := CompleteFixtureSpec("fixture")
			for _, criterion := range tt.omit {
				delete(spec.RequiredCases, CaseKey(criterion))
			}

			err := ValidateAcceptanceSpec(spec)
			if err == nil {
				t.Fatal("ValidateAcceptanceSpec() error = nil, want missing acceptance criterion")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateAcceptanceSpec() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateAcceptanceSpecRejectsMissingEventTranslation(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.EventCases = nil

	err := ValidateAcceptanceSpec(spec)
	if err == nil {
		t.Fatal("ValidateAcceptanceSpec() error = nil, want missing event translation")
	}
	if !strings.Contains(err.Error(), "event_translation") {
		t.Fatalf("ValidateAcceptanceSpec() error = %v, want event_translation", err)
	}
}

func TestRequiredAcceptanceCriteriaProjectsDeclaredRequiredCases(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	got := RequiredAcceptanceCriteria(spec)
	want := []AcceptanceCriterion{
		AcceptanceEventTranslation,
		AcceptanceApproval,
		AcceptanceInterrupt,
		AcceptanceForceComplete,
		AcceptanceResume,
		AcceptanceToolbridge,
		AcceptanceDynamicToolResponder,
		AcceptanceRuntimeReport,
		AcceptancePromptSnapshotParity,
	}

	if !slices.Equal(got, want) {
		t.Fatalf("RequiredAcceptanceCriteria() = %v, want %v", got, want)
	}
}

func TestValidateAcceptanceSpecAcceptsPromptMaterializedAlternative(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CasePromptMaterializedCarrier] = spec.RequiredCases[CasePromptParity]
	delete(spec.RequiredCases, CasePromptParity)

	if err := ValidateAcceptanceSpec(spec); err != nil {
		t.Fatalf("ValidateAcceptanceSpec() error = %v, want materialized prompt carrier accepted", err)
	}
}
