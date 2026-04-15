package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestNumericLengthAnchorsProviderRequiresAntUserType(t *testing.T) {
	provider := NumericLengthAnchorsProvider{}
	if text, err := provider.Resolve(context.Background(), SectionContext{}); err != nil || text != nil {
		t.Fatalf("Resolve() = %v, %v, want nil without ant gate", text, err)
	}
	t.Setenv("USER_TYPE", "ant")
	text, err := provider.Resolve(context.Background(), SectionContext{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, "25 words") || !strings.Contains(*text, "100 words") {
		t.Fatalf("Resolve() = %v, want numeric anchors prompt", text)
	}
}

func TestTokenBudgetProviderRespectsFeatureGate(t *testing.T) {
	provider := TokenBudgetProvider{}
	if text, err := provider.Resolve(context.Background(), SectionContext{}); err != nil || text != nil {
		t.Fatalf("Resolve() = %v, %v, want nil without TOKEN_BUDGET gate", text, err)
	}
	t.Setenv("TOKEN_BUDGET", "1")
	text, err := provider.Resolve(context.Background(), SectionContext{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, "minimum work budget") {
		t.Fatalf("Resolve() = %v, want token budget prompt", text)
	}
}

func TestBriefProviderRequiresFeatureAndBriefMode(t *testing.T) {
	provider := BriefProvider{}
	ctx := SectionContext{BuildCtx: BuildCtx{Summary: "brief"}}
	if text, err := provider.Resolve(context.Background(), ctx); err != nil || text != nil {
		t.Fatalf("Resolve() = %v, %v, want nil without KAIROS feature", text, err)
	}
	t.Setenv("KAIROS_BRIEF", "1")
	text, err := provider.Resolve(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, "# Brief Mode") {
		t.Fatalf("Resolve() = %v, want brief mode prompt", text)
	}
	text, err = provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{SessionFlags: map[string]bool{"kairos_brief": true}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %v, want nil when brief mode is disabled", text)
	}
}

func TestAssembleTurnBriefGateTracksSummary(t *testing.T) {
	t.Setenv("KAIROS_BRIEF", "1")
	svc := NewService(&Config{}, nil)
	turnWithBrief, err := svc.AssembleTurn(context.Background(), TurnInput{Summary: "brief"})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if _, ok := resolvedSectionContent(turnWithBrief.ResolvedSections, DynamicSectionBrief); !ok {
		t.Fatalf("ResolvedSections = %#v, want brief section when summary=brief", turnWithBrief.ResolvedSections)
	}
	turnWithoutBrief, err := svc.AssembleTurn(context.Background(), TurnInput{})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if _, ok := resolvedSectionContent(turnWithoutBrief.ResolvedSections, DynamicSectionBrief); ok {
		t.Fatalf("ResolvedSections = %#v, want no brief section without brief mode", turnWithoutBrief.ResolvedSections)
	}
}

func TestTokenBudgetBackstopContinuesBelowSoftFloor(t *testing.T) {
	tracker := &TokenBudgetTracker{}
	decision := evaluateTokenBudgetBackstop(tracker, 10000, 8000, false)
	if !decision.Continue || decision.MinimumTarget != 9000 {
		t.Fatalf("decision = %#v, want continuation below 90%% soft floor", decision)
	}
	if tracker.ContinuationCount != 1 {
		t.Fatalf("ContinuationCount = %d, want 1", tracker.ContinuationCount)
	}
}

func TestTokenBudgetBackstopStopsAfterThreeLowYieldContinuations(t *testing.T) {
	tracker := &TokenBudgetTracker{}
	for _, total := range []int{6000, 6200, 6400} {
		decision := evaluateTokenBudgetBackstop(tracker, 10000, total, false)
		if !decision.Continue {
			t.Fatalf("decision = %#v, want continuation before low-yield streak saturates", decision)
		}
	}
	decision := evaluateTokenBudgetBackstop(tracker, 10000, 6600, false)
	if decision.Continue || !decision.DiminishingReturns {
		t.Fatalf("decision = %#v, want stop after three consecutive low-yield continuations", decision)
	}
}
