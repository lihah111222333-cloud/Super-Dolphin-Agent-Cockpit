package gate

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	expandedGoPackageBootstrapEstimateMS     int64 = 4_000
	expandedGoRacePackageBootstrapEstimateMS int64 = 8_000
	// expandedNilnessTotalBootstrapEstimateMS 保守估计拆包后整个 analyzer gate 的冷启动总量。
	// 每个包只分得该预算的一部分，避免首代账本将每个 nilness 包误判为独立长任务。
	expandedNilnessTotalBootstrapEstimateMS int64 = 180_000
	// expandedPlaywrightBootstrapEstimateMS 让每个 describe target 在首个 authoritative
	// timing sample 到达前保持在 medium resource tier。
	expandedPlaywrightBootstrapEstimateMS int64 = 20_000
	// AtomicArchtestPackageTarget、AtomicCodexAppPackageTarget、AtomicAgentRuntimePackageTarget、
	// AtomicAgentTerminalPackageTarget、AtomicAppPackageTarget、AtomicUpdaterPackageTarget、
	// AtomicTaskDAGPackageTarget、AtomicSQLitePackageTarget、AtomicGatePackageTarget 与
	// AtomicRemoteCIPackageTarget 必须拆成顶层测试，
	// 避免大型包形成超时整包 workload。
	// 这些目标均保留同一 release/race gate 的完整覆盖，并由 compile group 共享一次测试二进制编译。
	AtomicArchtestPackageTarget      = "./internal/archtest"
	AtomicCodexAppPackageTarget      = "./internal/provider/codexapp"
	AtomicAgentRuntimePackageTarget  = "./cmd/agent-runtime"
	AtomicAgentTerminalPackageTarget = "./cmd/agent-terminal"
	AtomicAppPackageTarget           = "./internal/app"
	AtomicUpdaterPackageTarget       = "./cmd/super-dolphin-updater"
	AtomicTaskDAGPackageTarget       = "./cmd/mcp-orch/store/taskdag"
	AtomicSQLitePackageTarget        = "./internal/platform/db/sqlite"
	AtomicGatePackageTarget          = "./internal/devtools/gate"
	AtomicRemoteCIPackageTarget      = "./internal/devtools/remoteci"
)

// AtomicGoPackageTargets 返回需要按顶层 Go 测试拆分的包目标副本。
// 调用方不得通过返回值改变 catalog 与 remote inventory 的唯一清单。
func AtomicGoPackageTargets() []string {
	atomicGoPackageTargets := []string{
		AtomicArchtestPackageTarget,
		AtomicCodexAppPackageTarget,
		AtomicAgentRuntimePackageTarget,
		AtomicAgentTerminalPackageTarget,
		AtomicAppPackageTarget,
		AtomicUpdaterPackageTarget,
		AtomicTaskDAGPackageTarget,
		AtomicSQLitePackageTarget,
		AtomicGatePackageTarget,
		AtomicRemoteCIPackageTarget,
	}
	return slices.Clone(atomicGoPackageTargets)
}

// raceExcludedStaticGoTestTargets 返回只需在 normal gate 执行的静态代码规模守卫目标。
// 该清单属于 workload catalog contract；race catalog 必须过滤它，而不是回退为整包 race。
func raceExcludedStaticGoTestTargets() []GoTestTarget {
	return []GoTestTarget{{Package: AtomicArchtestPackageTarget, Name: "TestCodeSizeGuard"}}
}

// isKnownWorkloadKind 报告 workload 的执行类别是否属于当前协议。
func isKnownWorkloadKind(kind WorkloadKind) bool {
	switch kind {
	case WorkloadKindGoTest, WorkloadKindNodeTest, WorkloadKindGuard:
		return true
	default:
		return false
	}
}

// WorkloadBootstrap 是 duration ledger 尚无可比成功样本时使用的显式种子。
type WorkloadBootstrap struct {
	Kind       WorkloadKind
	EstimateMS int64
	Shardable  bool
}

// WorkloadBootstrapPolicy 按稳定 GateID 保存 workload 的冷启动分类和估时。
type WorkloadBootstrapPolicy map[GateID]WorkloadBootstrap

// validateWorkloadPlanIdentity 校验持久化计划的 schema、gate 摘要与账本 generation。
func validateWorkloadPlanIdentity(plan WorkloadExecutionPlan) error {
	if plan.SchemaVersion != workloadExecutionPlanSchemaVersion {
		return fmt.Errorf("workload execution plan schema must equal %d", workloadExecutionPlanSchemaVersion)
	}
	if !isPrefixedSHA256Digest(plan.GatePlanDigest) || plan.LedgerGeneration == 0 {
		return errors.New("workload execution plan gate or ledger identity is invalid")
	}
	return nil
}

// validateWorkloadPlanOwnerDuration 校验 owner-only 关键路径不挤占完整 SLA。
func validateWorkloadPlanOwnerDuration(plan WorkloadExecutionPlan) error {
	if plan.OwnerEstimatedDurationMS < 0 || plan.OwnerEstimatedDurationMS >= plan.Context.TargetDurationMS {
		return errors.New("workload execution plan owner duration is invalid")
	}
	return nil
}

// DefaultWorkloadBootstrapPolicy 返回覆盖当前完整 GateRegistry 的隔离策略。
func DefaultWorkloadBootstrapPolicy() WorkloadBootstrapPolicy {
	return WorkloadBootstrapPolicy{
		GateIDAIMaintenanceSelfTest: {WorkloadKindGuard, 60000, true}, GateIDFrontendLint: {WorkloadKindGuard, 60000, true}, GateIDFrontendPreflight: {WorkloadKindGuard, 60000, true}, GateIDFrontendTest: {WorkloadKindNodeTest, 90000, true}, GateIDFrontendFullTest: {WorkloadKindNodeTest, 90000, true}, GateIDFrontendBuild: {WorkloadKindGuard, 60000, true}, GateIDFrontendEmbedVerify: {WorkloadKindGuard, 60000, true}, GateIDBackendTestWithGuard: {WorkloadKindGoTest, 90000, true}, GateIDBackendTestGuardWithRace: {WorkloadKindGoTest, 90000, true}, GateIDBackendNilness: {WorkloadKindGuard, 60000, true}, GateIDSQLCVerify: {WorkloadKindGuard, 60000, true}, GateIDCodemapCheck: {WorkloadKindGuard, 60000, true}, GateIDProjectMapCheck: {WorkloadKindGuard, 60000, true}, GateIDCapabilityContractCheck: {WorkloadKindGuard, 60000, true}, GateIDWhitespaceCheck: {WorkloadKindGuard, 10000, true}, GateIDReleaseLayeredCheck: {WorkloadKindGuard, 30000, false},
		GateIDFrontendE2E: {WorkloadKindNodeTest, 90000, true},
	}
}

// BuildWorkloadCatalog 将当前规范 GatePlan 转换为可记录、可分片的 workload 真值源。
func BuildWorkloadCatalog(plan GatePlan, policy WorkloadBootstrapPolicy) (WorkloadCatalog, error) {
	if err := plan.Validate(); err != nil {
		return WorkloadCatalog{}, fmt.Errorf("validate gate plan: %w", err)
	}
	if err := ValidateWorkloadBootstrapPolicy(policy); err != nil {
		return WorkloadCatalog{}, err
	}
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: make([]Workload, 0, len(plan.Gates))}
	for _, spec := range plan.Gates {
		workload, err := canonicalWorkload(spec, policy[spec.ID])
		if err != nil {
			return WorkloadCatalog{}, err
		}
		catalog.Workloads = append(catalog.Workloads, workload)
	}
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return WorkloadCatalog{}, err
	}
	return catalog, nil
}

// BuildExpandedWorkloadCatalog 将 Go 包和 Vitest 文件展开为可跨容器调度的原子 workload。
func BuildExpandedWorkloadCatalog(plan GatePlan, policy WorkloadBootstrapPolicy, inventory WorkloadInventory) (WorkloadCatalog, error) {
	return buildExpandedWorkloadCatalog(plan, policy, inventory, false)
}

// BuildCalibrationWorkloadCatalog 展开首代校准目录，并为每个 Go 包生成 race workload。
func BuildCalibrationWorkloadCatalog(plan GatePlan, policy WorkloadBootstrapPolicy, inventory WorkloadInventory) (WorkloadCatalog, error) {
	return buildExpandedWorkloadCatalog(plan, policy, inventory, true)
}

// BuildSelectedTestWorkloadCatalog 构建非权威的指定测试目录；它永远不能替代完整 GatePlan。
func BuildSelectedTestWorkloadCatalog(plan GatePlan, inventory WorkloadInventory) (WorkloadCatalog, error) {
	if err := plan.Validate(); err != nil {
		return WorkloadCatalog{}, err
	}
	normalized, err := normalizeWorkloadInventory(inventory)
	if err != nil {
		return WorkloadCatalog{}, err
	}
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: false}
	if err := appendSelectedFrontendWorkloads(&catalog, normalized.FrontendFullTests); err != nil {
		return WorkloadCatalog{}, err
	}
	if err := appendSelectedGoPackageWorkloads(&catalog, normalized.GoPackages); err != nil {
		return WorkloadCatalog{}, err
	}
	if err := appendSelectedGoTestWorkloads(&catalog, normalized.GoTests); err != nil {
		return WorkloadCatalog{}, err
	}
	if err := appendSelectedGoBenchmarkWorkloads(&catalog, normalized.GoBenchmarks); err != nil {
		return WorkloadCatalog{}, err
	}
	if err := validateSelectedWorkloadCatalog(plan, catalog); err != nil {
		return WorkloadCatalog{}, err
	}
	return catalog, nil
}

// appendSelectedFrontendWorkloads 将已规范化的 Vitest 文件追加为原子 workload。
func appendSelectedFrontendWorkloads(catalog *WorkloadCatalog, targets []string) error {
	for _, target := range targets {
		workload, err := selectedTargetWorkload(
			GateIDFrontendTest,
			workloadTargetVitest,
			target,
			WorkloadKindNodeTest,
			5_000,
		)
		if err != nil {
			return err
		}
		catalog.Workloads = append(catalog.Workloads, workload)
	}
	return nil
}

// appendSelectedGoPackageWorkloads 将已规范化的 Go 包追加为整包 workload。
func appendSelectedGoPackageWorkloads(catalog *WorkloadCatalog, targets []string) error {
	for _, target := range targets {
		workload, err := selectedTargetWorkload(
			GateIDBackendTestWithGuard,
			workloadTargetGoPackage,
			target,
			WorkloadKindGoTest,
			15_000,
		)
		if err != nil {
			return err
		}
		catalog.Workloads = append(catalog.Workloads, workload)
	}
	return nil
}

// appendSelectedGoTestWorkloads 将已规范化的顶层 Go 测试追加为精确 workload。
func appendSelectedGoTestWorkloads(catalog *WorkloadCatalog, targets []GoTestTarget) error {
	for _, target := range targets {
		workload, err := NewGoTestWorkload(
			GateIDBackendTestWithGuard,
			target.Package,
			target.Name,
			1_000,
		)
		if err != nil {
			return err
		}
		catalog.Workloads = append(catalog.Workloads, workload)
	}
	return nil
}

// appendSelectedGoBenchmarkWorkloads 将已规范化的 benchmark 追加为远程 workload。
func appendSelectedGoBenchmarkWorkloads(catalog *WorkloadCatalog, targets []GoTestTarget) error {
	for _, target := range targets {
		workload, err := NewGoBenchmarkWorkload(
			GateIDBackendTestWithGuard,
			target.Package,
			target.Name,
			15_000,
		)
		if err != nil {
			return err
		}
		catalog.Workloads = append(catalog.Workloads, workload)
	}
	return nil
}

// selectedTargetWorkload 构造带稳定 digest 的一个原子 worker 测试。
func selectedTargetWorkload(gateID GateID, targetKind, target string, kind WorkloadKind, estimate int64) (Workload, error) {
	id, err := targetWorkloadID(gateID, targetKind, target)
	if err != nil {
		return Workload{}, err
	}
	digest, err := workloadProgramDigest(id)
	if err != nil {
		return Workload{}, err
	}
	return Workload{ID: id, Kind: kind, CommandDigest: digest, BootstrapEstimateMS: estimate, Shardable: true}, nil
}

// validateSelectedWorkloadCatalog 校验非权威目录只含当前计划下的原子 worker 测试。
func validateSelectedWorkloadCatalog(plan GatePlan, catalog WorkloadCatalog) error {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return err
	}
	if catalog.Authoritative {
		return errors.New("selected workload catalog must be non-authoritative")
	}
	required := make(map[GateID]struct{}, len(plan.Gates))
	for _, spec := range plan.Gates {
		required[spec.ID] = struct{}{}
	}
	for index, workload := range catalog.Workloads {
		if err := validateSelectedWorkload(index, workload, required, plan.Profile); err != nil {
			return err
		}
	}
	return nil
}

// validateSelectedWorkload 验证一项选择性 workload 的归属、分片和命令身份。
func validateSelectedWorkload(index int, workload Workload, required map[GateID]struct{}, profile Profile) error {
	parent, _, _, targeted, err := parseTargetWorkloadID(workload.ID)
	if err != nil {
		return err
	}
	if !targeted || !workload.Shardable {
		return fmt.Errorf("selected workload catalog entry %d is not an atomic worker test", index)
	}
	if _, ok := required[parent]; !ok {
		return fmt.Errorf("selected workload gate %q is not required by profile %q", parent, profile)
	}
	digest, err := workloadProgramDigest(workload.ID)
	if err != nil {
		return err
	}
	if workload.CommandDigest != digest {
		return fmt.Errorf("selected workload catalog entry %d command drifted", index)
	}
	return nil
}

// canonicalWorkload 从一个 GateSpec 生成其未展开的确定性 workload。
func canonicalWorkload(spec GateSpec, bootstrap WorkloadBootstrap) (Workload, error) {
	if isExpansionOnlyGate(spec.ID) {
		return Workload{ID: string(spec.ID), Kind: bootstrap.Kind, CommandDigest: expansionOnlyWorkloadDigest(spec.ID), BootstrapEstimateMS: bootstrap.EstimateMS}, nil
	}
	digest, err := WorkloadExecutionDigest(string(spec.ID))
	if err != nil {
		return Workload{}, err
	}
	return Workload{ID: string(spec.ID), Kind: bootstrap.Kind, CommandDigest: digest, BootstrapEstimateMS: bootstrap.EstimateMS, Shardable: bootstrap.Shardable}, nil
}

// expandedTargetsForGateMode 返回 gate 在普通或首代校准模式下的原子目标。
func expandedTargetsForGateMode(id GateID, inventory WorkloadInventory, allRacePackages bool) (string, []string) {
	switch id {
	case GateIDBackendTestWithGuard:
		return workloadTargetGoPackage, inventory.GoPackages
	case GateIDBackendTestGuardWithRace:
		if allRacePackages {
			return workloadTargetGoPackage, inventory.GoPackages
		}
		return workloadTargetGoPackage, raceWorkloadPackages(inventory.GoPackages)
	case GateIDBackendNilness:
		return workloadTargetGoPackage, inventory.GoPackages
	case GateIDFrontendTest:
		return workloadTargetVitest, inventory.FrontendChangedTests
	case GateIDFrontendFullTest:
		return workloadTargetVitest, inventory.FrontendFullTests
	case GateIDFrontendE2E:
		return workloadTargetPlaywright, []string{
			playwrightBusinessReadSurfacesTarget,
			playwrightBusinessChatBridgeTarget,
			playwrightDesktopShellTarget,
			playwrightDesktopBusinessPagesTarget,
			playwrightDesktopReadSettingsTarget,
		}
	default:
		return "", nil
	}
}

// normalizeWorkloadInventory 对各类目标排序并拒绝非法值或重复值。
func normalizeWorkloadInventory(inventory WorkloadInventory) (WorkloadInventory, error) {
	var err error
	inventory.GoPackages, err = normalizeWorkloadTargets(inventory.GoPackages, validateGoPackageTarget)
	if err != nil {
		return WorkloadInventory{}, err
	}
	inventory.NestedGoModules, err = normalizeWorkloadTargets(inventory.NestedGoModules, validateNestedGoModulePath)
	if err != nil {
		return WorkloadInventory{}, err
	}
	inventory.GoTests, err = normalizeGoTestTargets(inventory.GoTests)
	if err != nil {
		return WorkloadInventory{}, err
	}
	inventory.GoRaceTests, err = normalizeGoTestTargets(inventory.GoRaceTests)
	if err != nil {
		return WorkloadInventory{}, err
	}
	inventory.GoBenchmarks, err = normalizeGoBenchmarkTargets(inventory.GoBenchmarks)
	if err != nil {
		return WorkloadInventory{}, err
	}
	inventory.FrontendChangedTests, err = normalizeWorkloadTargets(inventory.FrontendChangedTests, validateVitestTarget)
	if err != nil {
		return WorkloadInventory{}, err
	}
	inventory.FrontendFullTests, err = normalizeWorkloadTargets(inventory.FrontendFullTests, validateVitestTarget)
	if err != nil {
		return WorkloadInventory{}, err
	}
	return inventory, nil
}

func normalizeGoTestTargets(values []GoTestTarget) ([]GoTestTarget, error) {
	return normalizeNamedGoTargets(values, "test", encodeGoTestTarget)
}

func normalizeGoBenchmarkTargets(values []GoTestTarget) ([]GoTestTarget, error) {
	return normalizeNamedGoTargets(values, "benchmark", encodeGoBenchmarkTarget)
}

// normalizeNamedGoTargets 规范化、排序并拒绝重复的命名 Go 目标。
func normalizeNamedGoTargets(
	values []GoTestTarget,
	label string,
	encode func(GoTestTarget) (string, error),
) ([]GoTestTarget, error) {
	cloned := slices.Clone(values)
	sort.Slice(cloned, func(left, right int) bool {
		if cloned[left].Package != cloned[right].Package {
			return cloned[left].Package < cloned[right].Package
		}
		return cloned[left].Name < cloned[right].Name
	})
	for index, target := range cloned {
		if _, err := encode(target); err != nil {
			return nil, fmt.Errorf("Go %s workload target %q#%q: %w", label, target.Package, target.Name, err)
		}
		if index > 0 && target == cloned[index-1] {
			return nil, fmt.Errorf("Go %s workload target %q#%q is duplicated", label, target.Package, target.Name)
		}
	}
	return cloned, nil
}

// normalizeWorkloadTargets 排序后校验同类目标的规范性和唯一性。
func normalizeWorkloadTargets(values []string, validate func(string) error) ([]string, error) {
	cloned := append([]string(nil), values...)
	sort.Strings(cloned)
	for index, value := range cloned {
		if err := validate(value); err != nil {
			return nil, fmt.Errorf("workload inventory target %q: %w", value, err)
		}
		if index > 0 && value == cloned[index-1] {
			return nil, fmt.Errorf("workload inventory target %q is duplicated", value)
		}
	}
	return cloned, nil
}

// raceWorkloadPackages 从完整 Go 包清单筛出既有 race 覆盖范围。
func raceWorkloadPackages(packages []string) []string {
	var selected []string
	for _, packageName := range packages {
		relative := strings.TrimPrefix(packageName, "./") + "/"
		for _, prefix := range RaceSensitivePathPrefixes() {
			if strings.HasPrefix(relative, prefix) {
				selected = append(selected, packageName)
				break
			}
		}
	}
	return selected
}

// ValidateWorkloadBootstrapPolicy 拒绝缺项、陈旧项和无效估时，强制策略随 GateRegistry 演进。
func ValidateWorkloadBootstrapPolicy(policy WorkloadBootstrapPolicy) error {
	if len(policy) == 0 {
		return errors.New("workload bootstrap policy must not be empty")
	}
	registry := GateRegistry()
	expected := make(map[GateID]struct{}, len(registry))
	for _, spec := range registry {
		expected[spec.ID] = struct{}{}
		bootstrap, ok := policy[spec.ID]
		if !ok {
			return fmt.Errorf("workload bootstrap policy is missing gate %q", spec.ID)
		}
		if err := validateWorkloadBootstrap(spec.ID, bootstrap); err != nil {
			return err
		}
	}
	for id := range policy {
		if _, ok := expected[id]; !ok {
			return fmt.Errorf("workload bootstrap policy contains stale gate %q", id)
		}
	}
	return nil
}

// validateWorkloadBootstrap 校验单个 Gate 的分类、估时和 worker 所有权边界。
func validateWorkloadBootstrap(id GateID, bootstrap WorkloadBootstrap) error {
	if !isKnownWorkloadKind(bootstrap.Kind) {
		return fmt.Errorf("workload bootstrap policy gate %q has unsupported kind %q", id, bootstrap.Kind)
	}
	if bootstrap.EstimateMS <= 0 {
		return fmt.Errorf("workload bootstrap policy gate %q estimate must be positive", id)
	}
	if id == GateIDReleaseLayeredCheck && bootstrap.Shardable {
		return errors.New("release layered attestation must remain owner-only and non-shardable")
	}
	if id != GateIDReleaseLayeredCheck && !bootstrap.Shardable {
		return fmt.Errorf("workload bootstrap policy gate %q must be shardable", id)
	}
	return nil
}

// buildExpandedWorkloadCatalog 按目标清单展开目录，并保留首代全包 race 选择。
func buildExpandedWorkloadCatalog(plan GatePlan, policy WorkloadBootstrapPolicy, inventory WorkloadInventory, allRacePackages bool) (WorkloadCatalog, error) {
	if err := plan.Validate(); err != nil {
		return WorkloadCatalog{}, fmt.Errorf("validate gate plan: %w", err)
	}
	if err := ValidateWorkloadBootstrapPolicy(policy); err != nil {
		return WorkloadCatalog{}, err
	}
	normalized, err := normalizeWorkloadInventory(inventory)
	if err != nil {
		return WorkloadCatalog{}, err
	}
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true}
	for _, spec := range plan.Gates {
		workloads, err := expandedGateWorkloads(spec, policy[spec.ID], normalized, allRacePackages)
		if err != nil {
			return WorkloadCatalog{}, err
		}
		catalog.Workloads = append(catalog.Workloads, workloads...)
	}
	if err := validateWorkloadCatalogForGatePlan(plan, catalog); err != nil {
		return WorkloadCatalog{}, err
	}
	return catalog, nil
}

// expandedGateWorkloads 生成一个 gate 的 canonical 或原子 worker workload 序列。
func expandedGateWorkloads(spec GateSpec, bootstrap WorkloadBootstrap, inventory WorkloadInventory, allRacePackages bool) ([]Workload, error) {
	targetKind, targets, atomicGoTests, err := expandedGateTargets(spec.ID, inventory, allRacePackages)
	if err != nil {
		return nil, err
	}
	if err := validateExpandedGateTargetInventory(spec.ID, targets); err != nil {
		return nil, err
	}
	guardSpecs, err := splitGoGuardWorkloadSpecs(spec.ID, targetKind, len(targets)+len(atomicGoTests), inventory.NestedGoModules)
	if err != nil {
		return nil, err
	}
	if noExpandedGateTargets(targets, guardSpecs, atomicGoTests) {
		workload, err := canonicalWorkload(spec, bootstrap)
		return []Workload{workload}, err
	}
	return appendExpandedGateWorkloads(spec.ID, targetKind, targets, guardSpecs, atomicGoTests)
}

// validateExpandedGateTargetInventory 阻断 nilness 在缺失 Go 包清单时静默退化。
func validateExpandedGateTargetInventory(gateID GateID, targets []string) error {
	if gateID == GateIDBackendNilness && len(targets) == 0 {
		return errors.New("backend:nilness requires a non-empty Go package inventory")
	}
	return nil
}

// appendExpandedGateWorkloads 按守卫、普通目标和原子测试的 canonical 顺序追加 workload。
func appendExpandedGateWorkloads(gateID GateID, targetKind string, targets []string, guardSpecs []splitGoGuardWorkloadSpec, atomicGoTests []string) ([]Workload, error) {
	workloads := make([]Workload, 0, len(targets)+len(guardSpecs)+len(atomicGoTests))
	nilnessEstimateMS := int64(0)
	if gateID == GateIDBackendNilness {
		nilnessEstimateMS = nilnessPackageBootstrapEstimateMS(len(targets))
	}
	for _, guardSpec := range guardSpecs {
		guard, err := selectedTargetWorkload(gateID, workloadTargetGoGuard, guardSpec.target, WorkloadKindGuard, guardSpec.estimateMS)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, guard)
	}
	for _, target := range targets {
		workload, err := expandedTargetWorkloadWithEstimate(gateID, targetKind, target, nilnessEstimateMS)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, workload)
	}
	for _, target := range atomicGoTests {
		workload, err := expandedTargetWorkload(gateID, workloadTargetGoTest, target)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, workload)
	}
	return workloads, nil
}

// expandedGateTargets 返回 gate 的普通目标与需要独立执行的顶层 Go 测试。
func expandedGateTargets(gateID GateID, inventory WorkloadInventory, allRacePackages bool) (string, []string, []string, error) {
	targetKind, targets := expandedTargetsForGateMode(gateID, inventory, allRacePackages)
	if targetKind != workloadTargetGoPackage {
		return targetKind, targets, nil, nil
	}
	packages, tests, err := splitAtomicGoTestTargets(gateID, targets, inventory)
	return targetKind, packages, tests, err
}

// noExpandedGateTargets 报告 gate 是否没有任何可展开目标。
func noExpandedGateTargets(targets []string, guards []splitGoGuardWorkloadSpec, tests []string) bool {
	return len(targets) == 0 && len(guards) == 0 && len(tests) == 0
}

// splitAtomicGoTestTargets 将已知超时包替换为精确顶层测试；缺少清单时保留整包覆盖。
func splitAtomicGoTestTargets(gateID GateID, packages []string, inventory WorkloadInventory) ([]string, []string, error) {
	if gateID == GateIDBackendNilness {
		return packages, nil, nil
	}
	tests := inventory.GoTests
	if gateID == GateIDBackendTestGuardWithRace {
		var err error
		packages, tests, err = prepareRaceGoTestInputs(packages, inventory)
		if err != nil {
			return nil, nil, err
		}
	}
	atomicPackages := atomicGoTestPackages(packages, tests)
	if len(atomicPackages) == 0 {
		return packages, nil, nil
	}
	encoded, err := encodeAtomicGoTestTargets(tests, atomicPackages)
	if err != nil {
		return nil, nil, err
	}
	if len(encoded) == 0 {
		return packages, nil, nil
	}
	return filterAtomicGoPackages(packages, atomicPackages), encoded, nil
}

func atomicGoTestPackages(packages []string, tests []GoTestTarget) []string {
	atomicTargets := AtomicGoPackageTargets()
	selected := make([]string, 0, len(atomicTargets))
	for _, packageTarget := range atomicTargets {
		if !slices.Contains(packages, packageTarget) || !hasAtomicGoTestPackage(tests, packageTarget) {
			continue
		}
		selected = append(selected, packageTarget)
	}
	return selected
}

func hasAtomicGoTestPackage(tests []GoTestTarget, packageTarget string) bool {
	for _, test := range tests {
		if test.Package == packageTarget {
			return true
		}
	}
	return false
}

type splitGoGuardWorkloadSpec struct {
	target     string
	estimateMS int64
}

// splitGoGuardWorkloadSpecs 将串行守卫按固定原子命令和 tree 中发现的嵌套模块拆分。
func splitGoGuardWorkloadSpecs(
	gateID GateID,
	targetKind string,
	targetCount int,
	nestedModules []string,
) ([]splitGoGuardWorkloadSpec, error) {
	if gateID == GateIDAIMaintenanceSelfTest {
		return []splitGoGuardWorkloadSpec{
			{target: GoGuardTargetAIMaintenanceUnit, estimateMS: 60000},
			{target: GoGuardTargetAIMaintenanceGate, estimateMS: 60000},
		}, nil
	}
	if gateID != GateIDBackendTestWithGuard || targetKind != workloadTargetGoPackage || targetCount == 0 {
		return nil, nil
	}
	specs := []splitGoGuardWorkloadSpec{
		{target: GoGuardTargetSourceRawGoTest, estimateMS: 60000},
		{target: GoGuardTargetCopylocksProvider, estimateMS: 20000},
		{target: GoGuardTargetCopylocksPlatform, estimateMS: 20000},
		{target: GoGuardTargetCopylocksThread, estimateMS: 10000},
	}
	for _, module := range nestedModules {
		target, err := NestedGoModuleGuardTarget(module)
		if err != nil {
			return nil, err
		}
		specs = append(specs, splitGoGuardWorkloadSpec{target: target, estimateMS: 30000})
	}
	return specs, nil
}

// expandedTargetWorkload 构造一个默认估时的 Go 或 Vitest 原子测试 workload。
func expandedTargetWorkload(gateID GateID, targetKind, target string) (Workload, error) {
	return expandedTargetWorkloadWithEstimate(gateID, targetKind, target, 0)
}

// expandedTargetWorkloadWithEstimate 构造可覆盖 bootstrap 估时的原子 workload。
func expandedTargetWorkloadWithEstimate(gateID GateID, targetKind, target string, estimateOverride int64) (Workload, error) {
	kind, estimate := WorkloadKindGoTest, expandedGoPackageBootstrapEstimateMS
	if targetKind == workloadTargetVitest {
		kind, estimate = WorkloadKindNodeTest, 5000
	} else if targetKind == workloadTargetPlaywright {
		kind, estimate = WorkloadKindNodeTest, expandedPlaywrightBootstrapEstimateMS
	} else if gateID == GateIDBackendTestGuardWithRace {
		estimate = expandedGoRacePackageBootstrapEstimateMS
	}
	if estimateOverride > 0 {
		estimate = estimateOverride
	}
	return selectedTargetWorkload(gateID, targetKind, target, kind, estimate)
}

func nilnessPackageBootstrapEstimateMS(packageCount int) int64 {
	if packageCount <= 0 {
		return 1
	}
	estimate := expandedNilnessTotalBootstrapEstimateMS / int64(packageCount)
	if estimate < 1 {
		return 1
	}
	return estimate
}

// validateWorkloadCatalogForGatePlan 校验目录按 gate 规范顺序精确覆盖 GatePlan。
func validateWorkloadCatalogForGatePlan(plan GatePlan, catalog WorkloadCatalog) error {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return err
	}
	if !catalog.Authoritative {
		return validateSelectedWorkloadCatalog(plan, catalog)
	}
	return validateAuthoritativeWorkloadCatalog(plan, catalog)
}

// validateAuthoritativeWorkloadCatalog 校验权威目录按 GatePlan 顺序完整覆盖。
func validateAuthoritativeWorkloadCatalog(plan GatePlan, catalog WorkloadCatalog) error {
	required := make(map[GateID]int, len(plan.Gates))
	for index, spec := range plan.Gates {
		required[spec.ID] = index
	}
	seen := make(map[GateID]int, len(plan.Gates))
	last := -1
	for index, workload := range catalog.Workloads {
		parent, gateIndex, err := validateAuthoritativeWorkload(index, workload, required, last)
		if err != nil {
			return err
		}
		last = gateIndex
		seen[parent]++
	}
	for _, spec := range plan.Gates {
		if seen[spec.ID] == 0 {
			return fmt.Errorf("workload catalog omits gate %q", spec.ID)
		}
	}
	return nil
}

// validateAuthoritativeWorkload 校验单项目录的顺序、命令摘要和分片归属。
func validateAuthoritativeWorkload(index int, workload Workload, required map[GateID]int, last int) (GateID, int, error) {
	parent, err := workloadParentGateID(workload.ID)
	if err != nil {
		return "", 0, err
	}
	gateIndex, ok := required[parent]
	if !ok || gateIndex < last {
		return "", 0, fmt.Errorf("workload catalog entry %d is outside canonical gate order", index)
	}
	_, _, _, targeted, err := parseTargetWorkloadID(workload.ID)
	if err != nil {
		return "", 0, err
	}
	if err := validateAuthoritativeWorkloadIdentity(index, workload, parent, targeted); err != nil {
		return "", 0, err
	}
	return parent, gateIndex, nil
}

// validateAuthoritativeWorkloadIdentity 校验展开描述、命令摘要和分片所有权。
func validateAuthoritativeWorkloadIdentity(index int, workload Workload, parent GateID, targeted bool) error {
	if isExpansionOnlyGate(parent) && !targeted {
		if workload.Shardable || workload.CommandDigest != expansionOnlyWorkloadDigest(parent) {
			return fmt.Errorf("workload catalog expansion descriptor %q is executable or drifted", parent)
		}
		return nil
	}
	digest, err := workloadProgramDigest(workload.ID)
	if err != nil {
		return err
	}
	if workload.CommandDigest != digest {
		return fmt.Errorf("workload catalog entry %d command drifted from gate %q", index, parent)
	}
	if workload.Shardable == (parent == GateIDReleaseLayeredCheck) {
		return fmt.Errorf("workload catalog gate %q has invalid shard ownership", parent)
	}
	return nil
}
