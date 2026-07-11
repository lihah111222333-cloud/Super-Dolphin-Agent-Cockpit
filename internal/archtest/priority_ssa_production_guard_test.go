package archtest_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestPrioritySSAGuardsUseUnifiedFreezeBaseline(t *testing.T) {
	root := repoRoot(t)
	freezePath := filepath.Join(root, "internal", "archtest", "freeze_baseline.json")
	info, err := archtest.LoadGuardFreeze(freezePath)
	if err != nil {
		t.Fatalf("load unified freeze: %v", err)
	}
	for key, violation := range info.Data.PrioritySSA {
		if violation.Rule == "" || violation.File == "" || violation.Detail == "" {
			t.Fatalf("priority SSA freeze entry %q lost rule/file/detail metadata: %#v", key, violation)
		}
		if key != violation.Key() {
			t.Fatalf("priority SSA freeze key mismatch: got %q want %q", key, violation.Key())
		}
	}

	result, err := archtest.CheckPrioritySSAWithBaseline(codeSizeGuardOptions(root), info.Data.PrioritySSA)
	if err != nil {
		t.Fatalf("check priority SSA freeze: %v", err)
	}
	if len(result.New) > 0 {
		t.Fatalf("priority SSA new violations not in unified freeze (%d):\n%s\nRun: go run ./scripts/code_size_guard.go --freeze",
			len(result.New), strings.Join(archtest.PrioritySSAViolationStrings(result.New), "\n"))
	}
}
