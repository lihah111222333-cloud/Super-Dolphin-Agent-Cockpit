package contracttest

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestAcceptanceConstantsProjectCaseKeys(t *testing.T) {
	cases := map[string]struct {
		got  AcceptanceCriterion
		want CaseKey
	}{
		"event translation":           {got: AcceptanceEventTranslation, want: CaseEventMatrix},
		"prompt snapshot parity":      {got: AcceptancePromptSnapshotParity, want: CasePromptParity},
		"prompt materialized carrier": {got: AcceptancePromptMaterialized, want: CasePromptMaterializedCarrier},
		"approval":                    {got: AcceptanceApproval, want: CaseApproval},
		"interrupt":                   {got: AcceptanceInterrupt, want: CaseInterrupt},
		"force complete":              {got: AcceptanceForceComplete, want: CaseForceComplete},
		"resume":                      {got: AcceptanceResume, want: CaseResume},
		"toolbridge":                  {got: AcceptanceToolbridge, want: CaseToolbridge},
		"runtime report":              {got: AcceptanceRuntimeReport, want: CaseRuntimeReport},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("acceptance criterion = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestRequiredAcceptanceCriteriaProjectsDeclaredCases(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	got := RequiredAcceptanceCriteria(spec)
	want := []AcceptanceCriterion{
		AcceptanceEventTranslation,
		AcceptancePromptSnapshotParity,
		AcceptanceApproval,
		AcceptanceInterrupt,
		AcceptanceForceComplete,
		AcceptanceResume,
		AcceptanceToolbridge,
		AcceptanceRuntimeReport,
	}
	assertAcceptanceCriteria(t, got, want)

	materialized := CompleteFixtureSpec("fixture")
	materialized.RequiredCases[CasePromptMaterializedCarrier] = materialized.RequiredCases[CasePromptParity]
	delete(materialized.RequiredCases, CasePromptParity)
	got = RequiredAcceptanceCriteria(materialized)
	want[1] = AcceptancePromptMaterialized
	assertAcceptanceCriteria(t, got, want)
}

func TestValidateAcceptanceSpecRejectsMissingCriteria(t *testing.T) {
	cases := []struct {
		name string
		edit func(Spec)
		want string
	}{
		{name: "event translation", edit: deleteRequiredCase(CaseEventMatrix), want: string(CaseEventMatrix)},
		{name: "approval", edit: deleteRequiredCase(CaseApproval), want: string(CaseApproval)},
		{name: "interrupt", edit: deleteRequiredCase(CaseInterrupt), want: string(CaseInterrupt)},
		{name: "force complete", edit: deleteRequiredCase(CaseForceComplete), want: string(CaseForceComplete)},
		{name: "resume", edit: deleteRequiredCase(CaseResume), want: string(CaseResume)},
		{name: "toolbridge", edit: deleteRequiredCase(CaseToolbridge), want: string(CaseToolbridge)},
		{name: "runtime report", edit: deleteRequiredCase(CaseRuntimeReport), want: string(CaseRuntimeReport)},
		{
			name: "prompt alternative",
			edit: func(spec Spec) {
				delete(spec.RequiredCases, CasePromptParity)
				delete(spec.RequiredCases, CasePromptMaterializedCarrier)
			},
			want: string(CasePromptParity) + " or " + string(CasePromptMaterializedCarrier),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := CompleteFixtureSpec("fixture")
			tc.edit(spec)
			err := ValidateAcceptanceSpec(spec)
			if err == nil {
				t.Fatal("ValidateAcceptanceSpec() error = nil, want missing criterion")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateAcceptanceSpec() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunSpecForTestValidatesAcceptanceBeforeProviderBehavior(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	delete(spec.RequiredCases, CaseRuntimeReport)
	started := false
	start := spec.Start
	spec.Start = func(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		started = true
		return start(ctx, req)
	}

	err := RunSpecForTest(t, spec)
	if err == nil {
		t.Fatal("RunSpecForTest() error = nil, want acceptance failure")
	}
	if started {
		t.Fatal("RunSpecForTest started provider behavior before validating acceptance")
	}
}

func deleteRequiredCase(key CaseKey) func(Spec) {
	return func(spec Spec) {
		delete(spec.RequiredCases, key)
	}
}

func assertAcceptanceCriteria(t *testing.T, got, want []AcceptanceCriterion) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("criteria len = %d, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("criteria[%d] = %q, want %q; got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}
