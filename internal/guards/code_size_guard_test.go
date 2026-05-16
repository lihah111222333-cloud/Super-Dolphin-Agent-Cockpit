package guards_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type codeSizeBudgetBaseline struct {
	TotalBudget int                              `json:"total_budget"`
	Packages    map[string]codeSizePackageBudget `json:"packages"`
}

type codeSizePackageBudget struct {
	Lines     int `json:"lines"`
	FrozenMax int `json:"frozen_max"`
}

// TestCodeSizeBudgetBaselineIsActionable verifies that the migration baseline
// cannot silently degrade into an empty or non-enforcing budget file.
//
// Layer: L3_ci_gate
func TestCodeSizeBudgetBaselineIsActionable(t *testing.T) {
	data, err := os.ReadFile("refactor_baseline.json")
	if err != nil {
		t.Fatalf("read refactor_baseline.json: %v", err)
	}

	var baseline codeSizeBudgetBaseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}

	assertCodeSizeBudgetBaseline(t, baseline)
}

func assertCodeSizeBudgetBaseline(t *testing.T, baseline codeSizeBudgetBaseline) {
	t.Helper()
	if baseline.TotalBudget <= 0 {
		t.Fatal("total_budget must be positive")
	}
	if len(baseline.Packages) == 0 {
		t.Fatal("packages must not be empty")
	}

	var totalFrozenMax int
	for pkg, budget := range baseline.Packages {
		assertCodeSizePackageBudget(t, pkg, budget)
		totalFrozenMax += budget.FrozenMax
	}
	if totalFrozenMax < baseline.TotalBudget {
		t.Fatalf("sum frozen_max = %d, want >= total_budget %d", totalFrozenMax, baseline.TotalBudget)
	}
}

func assertCodeSizePackageBudget(t *testing.T, pkg string, budget codeSizePackageBudget) {
	t.Helper()
	if strings.TrimSpace(pkg) == "" {
		t.Fatal("package path must not be blank")
	}
	if budget.Lines < 0 {
		t.Fatalf("%s lines = %d, want >= 0", pkg, budget.Lines)
	}
	if budget.FrozenMax <= 0 {
		t.Fatalf("%s frozen_max = %d, want > 0", pkg, budget.FrozenMax)
	}
	if budget.Lines > budget.FrozenMax {
		t.Fatalf("%s lines = %d, want <= frozen_max %d", pkg, budget.Lines, budget.FrozenMax)
	}
}
