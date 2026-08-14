package gate

import "fmt"

// FrontendPerformanceInputPaths 返回 performance:verify 的显式仓库输入闭包。
func FrontendPerformanceInputPaths() []string {
	return []string{
		"frontend-app/package.json",
		"frontend-app/package-lock.json",
		"frontend-app/vite.config.js",
		"frontend-app/scripts/chat-history-benchmark.mjs",
		"frontend-app/scripts/evidence-provenance.mjs",
		"frontend-app/scripts/frontend-maintainability-baseline.json",
		"frontend-app/scripts/frontend-performance-cases.json",
		"frontend-app/scripts/runtime/git-environment.mjs",
		"frontend-app/scripts/managed-command.mjs",
		"frontend-app/scripts/performance-baseline-provenance.mjs",
		"frontend-app/scripts/performance-budget-config.mjs",
		"frontend-app/scripts/performance-budget-model.mjs",
		"frontend-app/scripts/performance-budget-runner.mjs",
		"frontend-app/scripts/render-isolation-probe.test.jsx",
		"frontend-app/scripts/resource-budget.mjs",
		"frontend-app/scripts/stop-feedback-benchmark.mjs",
		"frontend-app/src/entities/client/model/contractStoreModel.js",
		"frontend-app/src/entities/client/model/threadLifecycleRuntime.js",
		"frontend-app/src/pages/chat/components/ChatActionFeedback.js",
		"frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.js",
		"frontend-app/src/pages/chat/model/timelineMaterializationModel.js",
	}
}

// buildCanonicalGateWorkloads 选择一个 Gate 在指定 profile 下的 canonical 或受控 suite workload。
// 远程 profile 不允许回到 frontend raw parent；preflight 始终只产生固定 atomic children。
func buildCanonicalGateWorkloads(spec GateSpec, profile Profile, bootstrap WorkloadBootstrap) ([]Workload, error) {
	if spec.ID == GateIDFrontendPreflight {
		return frontendPreflightWorkloads()
	}
	if spec.ID == GateIDFrontendTest && profile != ProfileLocalFast {
		return oneFrontendSuiteWorkload(GateIDFrontendTest, FrontendChangedSuiteCarrierTarget)
	}
	if spec.ID == GateIDFrontendFullTest {
		return oneFrontendSuiteWorkload(GateIDFrontendFullTest, FrontendFullSuiteCarrierTarget)
	}
	workload, err := canonicalWorkload(spec, bootstrap)
	if err != nil {
		return nil, err
	}
	return []Workload{workload}, nil
}

// oneFrontendSuiteWorkload 用旧 materializer 支持的 Vitest 载体构造 changed/full body-only workload。
func oneFrontendSuiteWorkload(gateID GateID, target string) ([]Workload, error) {
	workload, err := expandedTargetWorkload(gateID, workloadTargetVitest, target)
	if err != nil {
		return nil, err
	}
	return []Workload{workload}, nil
}

// frontendPreflightWorkloads 构造固定 allowlist，禁止 parent aggregate 与 children 同时进入 catalog。
func frontendPreflightWorkloads() ([]Workload, error) {
	return frontendGuardWorkloads(GateIDFrontendPreflight)
}

// frontendGuardWorkloads 为 preflight parent 构造固定原子 allowlist。
func frontendGuardWorkloads(parent GateID) ([]Workload, error) {
	targets := FrontendPreflightTargets()
	workloads := make([]Workload, 0, len(targets))
	for _, target := range targets {
		workload, err := expandedTargetWorkload(parent, workloadTargetFrontendGuard, target)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, workload)
	}
	return workloads, nil
}

// frontendPreflightCarrierWorkloads 为 local-fast 构造旧 materializer 可解析的原子 preflight 载体。
func frontendPreflightCarrierWorkloads() ([]Workload, error) {
	targets := FrontendPreflightTargets()
	workloads := make([]Workload, 0, len(targets))
	for _, target := range targets {
		carrier, err := FrontendPreflightCarrierTarget(target)
		if err != nil {
			return nil, err
		}
		workload, err := expandedTargetWorkload(GateIDFrontendTest, workloadTargetVitest, carrier)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, workload)
	}
	return workloads, nil
}

// frontendTestTargetsForGate 为 changed/full frontend gate 选择真实 Vitest 清单或受控空清单 suite。
func frontendTestTargetsForGate(id GateID, inventory WorkloadInventory) (string, []string) {
	switch id {
	case GateIDFrontendTest:
		if len(inventory.FrontendChangedTests) == 0 {
			return workloadTargetVitest, []string{FrontendChangedSuiteCarrierTarget}
		}
		return workloadTargetVitest, inventory.FrontendChangedTests
	case GateIDFrontendFullTest:
		if len(inventory.FrontendFullTests) == 0 {
			return workloadTargetVitest, []string{FrontendFullSuiteCarrierTarget}
		}
		return workloadTargetVitest, inventory.FrontendFullTests
	default:
		return "", nil
	}
}

// expandedTargetBootstrap 选择 atomic target 的执行类型和显式冷启动估时。
func expandedTargetBootstrap(gateID GateID, targetKind, target string) (WorkloadKind, int64, error) {
	switch targetKind {
	case workloadTargetVitest:
		if preflightTarget, ok := ParseFrontendPreflightCarrierTarget(target); ok {
			estimate, err := frontendPreflightBootstrapEstimateMS(preflightTarget)
			return WorkloadKindGuard, estimate, err
		}
		if isFrontendSuiteCarrierTarget(gateID, target) {
			return WorkloadKindNodeTest, 90_000, nil
		}
		return WorkloadKindNodeTest, 5_000, nil
	case workloadTargetFrontendGuard:
		estimate, err := frontendPreflightBootstrapEstimateMS(target)
		if err != nil {
			return "", 0, err
		}
		return WorkloadKindGuard, estimate, nil
	case workloadTargetPlaywright:
		return WorkloadKindNodeTest, expandedPlaywrightBootstrapEstimateMS, nil
	default:
		if gateID == GateIDBackendTestGuardWithRace {
			return WorkloadKindGoTest, expandedGoRacePackageBootstrapEstimateMS, nil
		}
		return WorkloadKindGoTest, expandedGoPackageBootstrapEstimateMS, nil
	}
}

func isFrontendSuiteCarrierTarget(gateID GateID, target string) bool {
	return gateID == GateIDFrontendTest && target == FrontendChangedSuiteCarrierTarget ||
		gateID == GateIDFrontendFullTest && target == FrontendFullSuiteCarrierTarget
}

func frontendPreflightBootstrapEstimateMS(target string) (int64, error) {
	switch target {
	case FrontendPreflightTargetCriticalGuards:
		return 60_000, nil
	case FrontendPreflightTargetTurnContractVerify, FrontendPreflightTargetTurnContractFieldGuard:
		return 30_000, nil
	case FrontendPreflightTargetCriticalTypecheck, FrontendPreflightTargetContractsVitest,
		FrontendPreflightTargetRPCAudit, FrontendPreflightTargetDependencyContract:
		return 15_000, nil
	default:
		return 0, fmt.Errorf("frontend preflight target %q has no bootstrap estimate", target)
	}
}

func validateAuthoritativeWorkloadMixes(plan GatePlan, catalog WorkloadCatalog) error {
	seenTargeted := make(map[GateID]bool, len(plan.Gates))
	seenCanonical := make(map[GateID]bool, len(plan.Gates))
	for _, workload := range catalog.Workloads {
		parent, _, _, targeted, err := parseTargetWorkloadID(workload.ID)
		if err != nil {
			return err
		}
		if plan.Profile != ProfileLocalFast && (parent == GateIDFrontendTest || parent == GateIDFrontendFullTest) && !targeted {
			return fmt.Errorf("non-local profile %q cannot execute raw frontend test workload %q", plan.Profile, workload.ID)
		}
		if targeted {
			seenTargeted[parent] = true
		} else {
			seenCanonical[parent] = true
		}
		if seenTargeted[parent] && seenCanonical[parent] {
			return fmt.Errorf("frontend workload parent %q mixes raw and targeted identities", parent)
		}
	}
	return nil
}
