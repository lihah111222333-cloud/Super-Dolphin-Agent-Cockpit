package gate

import "testing"

func TestCompileGroupSerialPackingEligibleUsesExplicitFailClosedPolicy(t *testing.T) {
	t.Parallel()
	eligiblePackages := []string{
		AtomicAgentRuntimePackageTarget,
		AtomicAppPackageTarget,
		AtomicUpdaterPackageTarget,
		AtomicTaskDAGPackageTarget,
		AtomicSQLitePackageTarget,
		AtomicGatePackageTarget,
		AtomicRemoteCIPackageTarget,
	}
	for _, packageTarget := range eligiblePackages {
		t.Run(packageTarget, func(t *testing.T) {
			t.Parallel()
			group := CompileGroup{PackageTarget: packageTarget, SemanticKey: CompileGroupSemanticGoTestNormal}
			if !CompileGroupSerialPackingEligible(group) {
				t.Fatalf("expected package %q to be serial-packing eligible", packageTarget)
			}
		})
	}

	rejected := []CompileGroup{
		{PackageTarget: AtomicArchtestPackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal},
		{PackageTarget: AtomicSuperDolphinGatePackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal},
		{PackageTarget: AtomicCodexAppPackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal},
		{PackageTarget: AtomicMcpLSPPackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal},
		{PackageTarget: AtomicAgentTerminalPackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal},
		{PackageTarget: AtomicGatePackageTarget, SemanticKey: CompileGroupSemanticGoTestRace},
		{PackageTarget: AtomicGatePackageTarget, SemanticKey: CompileGroupSemanticGoBenchmark},
		{PackageTarget: AtomicGatePackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal, BatchPlan: []CompileGroupBatch{{Exclusive: true}}},
		{PackageTarget: "./new/atomic/package", SemanticKey: CompileGroupSemanticGoTestNormal},
	}
	for _, group := range rejected {
		if CompileGroupSerialPackingEligible(group) {
			t.Fatalf("unexpected serial-packing eligibility for package=%q semantic=%q", group.PackageTarget, group.SemanticKey)
		}
	}
}
