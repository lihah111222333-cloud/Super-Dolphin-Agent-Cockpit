package main

import (
	"strings"
	"testing"
)

func TestCodeSizeGuardUsesSingleUnifiedFreezeFile(t *testing.T) {
	guard := readScript(t, "code_size_guard.go")
	assertScriptContains(t, guard, "freezePath := filepath.Join(repoRoot, \"internal/archtest/freeze_baseline.json\")")
	assertScriptContains(t, guard, "runFreeze(opts, freezePath, cfg.acceptance)")
	assertScriptContains(t, guard, "runCheck(opts, freezePath)")
	assertScriptContains(t, guard, "runUnifiedFreezePhase(freezePath, opts)")
	assertScriptContains(t, guard, "internal/archtest/freeze_baseline.json")

	for _, oldPath := range []string{
		"internal/archtest/baseline.json",
		"internal/archtest/baseline_test.json",
		"internal/archtest/priority_ssa_baseline.json",
	} {
		if strings.Contains(guard, oldPath) {
			t.Fatalf("code_size_guard.go still references old freeze file %s", oldPath)
		}
	}
}

func TestCodeSizeGuardWiresPrioritySSABaselineIntoFreezeCheckAndStrict(t *testing.T) {
	guard := readScript(t, "code_size_guard.go")
	for _, want := range []string{
		"runFreeze(opts, freezePath, cfg.acceptance)",
		"runCheck(opts, freezePath)",
		"runStrict(opts)",
		"archtest.CollectPrioritySSAViolations(opts)",
		"archtest.FreezeGuardState(opts, acceptance)",
		"runPrioritySSAFreezePhase(&freeze, opts)",
		"reportPrioritySSAViolationsAndExit(\"priority SSA 新增违规\", result.New)",
		"PrioritySSABaselineFromCurrent(result)",
	} {
		assertScriptContains(t, guard, want)
	}
}
