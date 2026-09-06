package main

import "testing"

// TestOrchUpgradePlanningGuardIsReadOnlyAndWired 锁定规划校验进入 guard，刷新只走显式入口。
func TestOrchUpgradePlanningGuardIsReadOnlyAndWired(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	guard := makefileTargetBlock(t, makefile, "guard")
	assertScriptContains(t, guard, "$(TEST_WITH_GUARD) --guard-only")
	assertScriptContains(t, guard, "$(MAKE) orch-upgrade-check")
	check := makefileTargetBlock(t, makefile, "orch-upgrade-check")
	assertScriptContains(t, check, "python3 -m unittest scripts.orch_upgrade_gate_test")
	assertScriptContains(t, check, "python3 -m unittest scripts.orch_acceptance_inventory_test")
	assertScriptContains(t, check, "python3 -m unittest scripts.orch_s0_executable_receipt_test")
	assertScriptContains(t, check, "python3 scripts/orch_acceptance_inventory.py --root \"$(CURDIR)\"")
	assertScriptContains(t, check, "python3 scripts/orch_upgrade_gate.py --root \"$(CURDIR)\" --check")
	assertScriptDoesNotContain(t, check, "--write")
	render := makefileTargetBlock(t, makefile, "orch-upgrade-render")
	assertScriptContains(t, render, "python3 scripts/orch_upgrade_gate.py --root \"$(CURDIR)\" --write")
}

// Blocker-specific planning evidence anchors (non-authorizing).
const (
	B01 = "B01"
	B02 = "B02"
	B03 = "B03"
	B04 = "B04"
	B05 = "B05"
	B06 = "B06"
	B07 = "B07"
	B08 = "B08"
	B09 = "B09"
	B10 = "B10"
	B11 = "B11"
	B12 = "B12"
	B13 = "B13"
	B14 = "B14"
	B15 = "B15"
	B16 = "B16"
	B17 = "B17"
	B18 = "B18"
)
