package remoteci

import (
	"slices"
	"testing"

	gate "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCanonicalSuperDolphinGateHelperIsExcludedFromInventory(t *testing.T) {
	names := []string{
		"TestRemoteHookConcurrentProcessHelper",
		"TestRemoteHookConcurrentProcessesKeepInheritedTokenAndDeliveryIsolated",
	}
	targets := inventoryGoTestTargets(gate.AtomicSuperDolphinGatePackageTarget, names)
	if slices.ContainsFunc(targets, func(target gate.GoTestTarget) bool {
		return target.Name == "TestRemoteHookConcurrentProcessHelper"
	}) {
		t.Fatal("super-dolphin-gate subprocess helper entered remote inventory")
	}
	if len(targets) != 1 || targets[0].Name != names[1] {
		t.Fatalf("inventory targets = %#v, want regular selector only", targets)
	}
}
