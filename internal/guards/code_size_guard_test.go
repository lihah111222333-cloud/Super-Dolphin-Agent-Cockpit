package guards_test

import (
	"encoding/json"
	"os"
	"testing"
)

// TestCodeSizeBudget verifies that no package exceeds its line budget.
// This is the V3 equivalent of go-agent-v2's code_size_guard_test.go.
//
// Layer: L3_ci_gate
func TestCodeSizeBudget(t *testing.T) {
	// TODO: implement when first real code is migrated
	// 1. Read refactor_baseline.json
	// 2. Count lines per package
	// 3. Assert each package <= frozen_max

	data, err := os.ReadFile("refactor_baseline.json")
	if err != nil {
		t.Skipf("baseline not found: %v", err)
	}

	var baseline struct {
		TotalBudget int `json:"total_budget"`
	}
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}

	if baseline.TotalBudget <= 0 {
		t.Fatal("total_budget must be positive")
	}
	t.Logf("V3 total budget: %d lines", baseline.TotalBudget)
}
