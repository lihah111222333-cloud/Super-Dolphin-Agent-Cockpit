package remoteci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

// remoteWorkloadFingerprintsWithSnapshot 只计算 correctness-bound production
// input digest，并保留同一 exact-tree snapshot。compile-group closure/profile
// 必须在 PASS 分类完成后由 MISS 专用入口按需计算。
func remoteWorkloadFingerprintsWithSnapshot(
	ctx context.Context,
	repositoryRoot string,
	tree string,
	workloads []gate.Workload,
) (map[string]string, map[string]gate.CompileGroupInput, *remoteGitTreeSnapshot, error) {
	return remoteWorkloadFingerprintsWithSnapshotWithCandidateObjectAuthority(ctx, repositoryRoot, tree, gate.CandidateObjectAuthority{}, workloads)
}

// remoteWorkloadFingerprintsWithSnapshotWithCandidateObjectAuthority retains
// the sealed private ODB proof while reading a staged exact tree for local PASS
// lookup. The authority is never identity material.
func remoteWorkloadFingerprintsWithSnapshotWithCandidateObjectAuthority(ctx context.Context, repositoryRoot, tree string, authority gate.CandidateObjectAuthority, workloads []gate.Workload) (map[string]string, map[string]gate.CompileGroupInput, *remoteGitTreeSnapshot, error) {
	snapshot, err := loadRemoteGitTreeSnapshotWithCandidateObjectAuthority(ctx, repositoryRoot, tree, authority)
	if err != nil {
		return nil, nil, nil, err
	}
	inputDigests, err := snapshot.remoteWorkloadInputDigests(ctx, workloads)
	if err != nil {
		return nil, nil, nil, err
	}
	return inputDigests, nil, snapshot, nil
}

func (snapshot *remoteGitTreeSnapshot) remoteWorkloadInputDigests(
	ctx context.Context,
	workloads []gate.Workload,
) (map[string]string, error) {
	return snapshot.concurrentRemoteWorkloadInputDigests(ctx, workloads)
}

// compileGroupInputs 计算指定 workload 集合的 compile-group closure/profile。
// 调用方必须已经完成 PASS lookup，并传入严格 MISS（含 package-affinity
// demotion 后的集合）；该函数不接受全 catalog 的隐式补全。
func (snapshot *remoteGitTreeSnapshot) compileGroupInputs(
	ctx context.Context,
	workloads []gate.Workload,
) (map[string]gate.CompileGroupInput, error) {
	compileInputs := make(map[string]gate.CompileGroupInput)
	profileDigests := make(map[bool]string, 2)
	for _, workload := range workloads {
		compileInput, ok, err := snapshot.compileGroupInputForWorkload(ctx, workload, profileDigests)
		if err != nil {
			return nil, fmt.Errorf("fingerprint compile input for workload %q: %w", workload.ID, err)
		}
		if ok {
			compileInputs[workload.ID] = compileInput
		}
	}
	return compileInputs, nil
}

// remoteCompileGroupInputsForMisses 将严格 MISS workload 投影为 compile-group 输入。
// It projects only strict MISS workload IDs into
// compile-group inputs. Unknown IDs and duplicate catalog entries fail closed.
func remoteCompileGroupInputsForMisses(
	ctx context.Context,
	snapshot *remoteGitTreeSnapshot,
	catalog gate.WorkloadCatalog,
	misses []gate.GateID,
) (map[string]gate.CompileGroupInput, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("remote compile-group fingerprint snapshot is required")
	}
	if len(misses) == 0 {
		return nil, nil
	}
	missSet, err := validateRemoteMissIDs(misses)
	if err != nil {
		return nil, err
	}
	missWorkloads, err := projectRemoteMissWorkloads(catalog, missSet)
	if err != nil {
		return nil, err
	}
	inputs, err := snapshot.compileGroupInputs(ctx, missWorkloads)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteCompileGroupInputsForWorkloads(missWorkloads, inputs); err != nil {
		return nil, err
	}
	return inputs, nil
}

func validateRemoteMissIDs(misses []gate.GateID) (map[string]struct{}, error) {
	missSet := make(map[string]struct{}, len(misses))
	for _, id := range misses {
		workloadID := string(id)
		if workloadID == "" {
			return nil, fmt.Errorf("remote workload MISS ID is empty")
		}
		if _, duplicate := missSet[workloadID]; duplicate {
			return nil, fmt.Errorf("remote workload MISS ID %q is duplicated", workloadID)
		}
		missSet[workloadID] = struct{}{}
	}
	return missSet, nil
}

func projectRemoteMissWorkloads(
	catalog gate.WorkloadCatalog,
	missSet map[string]struct{},
) ([]gate.Workload, error) {
	missWorkloads := make([]gate.Workload, 0, len(missSet))
	seen := make(map[string]struct{}, len(missSet))
	for _, workload := range catalog.Workloads {
		if _, ok := missSet[workload.ID]; !ok {
			continue
		}
		missWorkloads = append(missWorkloads, workload)
		seen[workload.ID] = struct{}{}
	}
	for workloadID := range missSet {
		if _, ok := seen[workloadID]; !ok {
			return nil, fmt.Errorf("remote workload MISS %q is not present in catalog", workloadID)
		}
	}
	return missWorkloads, nil
}

func validateRemoteCompileGroupInputsForWorkloads(
	workloads []gate.Workload,
	inputs map[string]gate.CompileGroupInput,
) error {
	expected := make(map[string]struct{}, len(workloads))
	for _, workload := range workloads {
		selector, err := validateRemoteCompileGroupInput(workload, inputs)
		if err != nil {
			return err
		}
		if selector {
			expected[workload.ID] = struct{}{}
		}
	}
	return rejectUnexpectedRemoteCompileGroupInputs(expected, inputs)
}

type remoteWorkloadInputDigestResult struct {
	digest string
	err    error
}

// concurrentRemoteWorkloadInputDigests 并行计算 exact Go selector；其他 workload
// 保持串行，避免前端 blob 读取等非共享路径制造无界子进程。结果仍按 catalog 顺序
// 检查并返回最早错误，因此并发不改变 fail-fast 的可观察语义。
func (snapshot *remoteGitTreeSnapshot) concurrentRemoteWorkloadInputDigests(
	ctx context.Context,
	workloads []gate.Workload,
) (map[string]string, error) {
	results := make([]remoteWorkloadInputDigestResult, len(workloads))
	parallel := make([]int, 0, len(workloads))
	for index, workload := range workloads {
		if remoteExactGoSelectorWorkload(workload) {
			parallel = append(parallel, index)
			continue
		}
		results[index].digest, results[index].err = snapshot.workloadInputDigest(ctx, workload)
	}
	snapshot.runRemoteWorkloadInputDigestWorkers(ctx, workloads, parallel, results)
	digests := make(map[string]string, len(workloads))
	for index, workload := range workloads {
		if results[index].err != nil {
			return nil, fmt.Errorf("fingerprint workload %q: %w", workload.ID, results[index].err)
		}
		digests[workload.ID] = results[index].digest
	}
	return digests, nil
}

type remoteWorkloadInputDigestWorkerKey struct {
	packageTarget string
	profile       string
}

// runRemoteWorkloadInputDigestWorkers 按包和构建 profile 分组 exact selector；
// 组间并行、组内串行，避免同包 selector 同时复制大型编译闭包。分组数由冻结
// catalog 唯一决定，不引入产品并发阈值，也不影响远程 ECI fanout 语义。
func (snapshot *remoteGitTreeSnapshot) runRemoteWorkloadInputDigestWorkers(
	ctx context.Context,
	workloads []gate.Workload,
	indexes []int,
	results []remoteWorkloadInputDigestResult,
) {
	groups := groupRemoteWorkloadInputDigestIndexes(workloads, indexes, results)
	var group errgroup.Group
	for _, indexes := range groups {
		group.Go(func() error {
			for _, index := range indexes {
				results[index].digest, results[index].err = snapshot.workloadInputDigest(ctx, workloads[index])
			}
			return nil
		})
	}
	_ = group.Wait()
}

// groupRemoteWorkloadInputDigestIndexes 保留输入顺序，并把同一编译根的 selector
// 放入同一个 worker；解析错误写入原结果槽，继续由调用方按 catalog 顺序报告。
func groupRemoteWorkloadInputDigestIndexes(
	workloads []gate.Workload,
	indexes []int,
	results []remoteWorkloadInputDigestResult,
) [][]int {
	positions := make(map[remoteWorkloadInputDigestWorkerKey]int)
	groups := make([][]int, 0)
	for _, index := range indexes {
		parsed, profile, supported, err := remoteGoWorkloadInputTarget(workloads[index])
		if err != nil || !supported {
			results[index].err = err
			if err == nil {
				results[index].err = fmt.Errorf("workload %q is not an exact Go selector", workloads[index].ID)
			}
			continue
		}
		key := remoteWorkloadInputDigestWorkerKey{packageTarget: parsed.Package, profile: profile.cacheKey()}
		position, ok := positions[key]
		if !ok {
			position = len(groups)
			positions[key] = position
			groups = append(groups, nil)
		}
		groups[position] = append(groups[position], index)
	}
	return groups
}

// remoteExactGoSelectorWorkload 只选择共享 snapshot cache 已具备并发保护的
// exact Go test/benchmark，其他 target 保持原执行顺序。
func remoteExactGoSelectorWorkload(workload gate.Workload) bool {
	_, kind, _, targeted, err := gate.ParseWorkloadID(workload.ID)
	return err == nil && targeted && (kind == gate.WorkloadTargetGoTest || kind == gate.WorkloadTargetGoBenchmark)
}

// validateRemoteCompileGroupInput 校验一个 catalog workload 的 Prepare 输入。
func validateRemoteCompileGroupInput(workload gate.Workload, inputs map[string]gate.CompileGroupInput) (bool, error) {
	parent, targetKind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return false, fmt.Errorf("parse workload %q for compile input: %w", workload.ID, err)
	}
	if !targeted || !remoteCompileGroupTargetKind(targetKind) {
		return false, nil
	}
	input, ok := inputs[workload.ID]
	if !ok {
		return false, fmt.Errorf("remote workload %q compile input is required", workload.ID)
	}
	if err := input.Validate(); err != nil {
		return false, fmt.Errorf("remote workload %q compile input: %w", workload.ID, err)
	}
	profile := remoteGoBuildProfile{race: parent == gate.GateIDBackendTestGuardWithRace}
	packageTarget, semanticKey, err := compileGroupTarget(targetKind, target, profile)
	if err != nil {
		return false, fmt.Errorf("remote workload %q compile target: %w", workload.ID, err)
	}
	if input.PackageTarget != packageTarget {
		return false, fmt.Errorf("remote workload %q compile package target drifted", workload.ID)
	}
	if input.SemanticKey != semanticKey {
		return false, fmt.Errorf("remote workload %q compile semantic target drifted", workload.ID)
	}
	return true, nil
}

// remoteCompileGroupTargetKind 限定 worker 支持的 exact Go selector 类型。
func remoteCompileGroupTargetKind(kind gate.WorkloadTargetKind) bool {
	return kind == gate.WorkloadTargetGoTest || kind == gate.WorkloadTargetGoBenchmark
}

// rejectUnexpectedRemoteCompileGroupInputs 禁止向非 selector workload 注入编译输入。
func rejectUnexpectedRemoteCompileGroupInputs(expected map[string]struct{}, inputs map[string]gate.CompileGroupInput) error {
	for workloadID := range inputs {
		if _, ok := expected[workloadID]; !ok {
			return fmt.Errorf("compile input supplied for non-Go workload %q", workloadID)
		}
	}
	return nil
}

// compileGroupInputForWorkload 为一个 selector workload 计算共享编译输入。
func (snapshot *remoteGitTreeSnapshot) compileGroupInputForWorkload(
	ctx context.Context,
	workload gate.Workload,
	profileDigests map[bool]string,
) (gate.CompileGroupInput, bool, error) {
	parent, targetKind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return gate.CompileGroupInput{}, false, err
	}
	// whole-package target 保持现有执行路径；只有 exact Go test/benchmark selector
	// 才允许生成 compile-group 输入。
	if !targeted || (targetKind != gate.WorkloadTargetGoTest && targetKind != gate.WorkloadTargetGoBenchmark) {
		return gate.CompileGroupInput{}, false, nil
	}
	profile := remoteGoBuildProfile{race: parent == gate.GateIDBackendTestGuardWithRace}
	packageTarget, semanticKey, err := compileGroupTarget(targetKind, target, profile)
	if err != nil {
		return gate.CompileGroupInput{}, false, err
	}
	sharedInputDigest, err := snapshot.goPackageInputDigest(ctx, packageTarget, profile)
	if err != nil {
		return gate.CompileGroupInput{}, false, err
	}
	profileDigest, ok := profileDigests[profile.race]
	if !ok {
		profileDigest, err = snapshot.compileGroupProfileDigest(ctx, profile)
		if err != nil {
			return gate.CompileGroupInput{}, false, err
		}
		profileDigests[profile.race] = profileDigest
	}
	input := gate.CompileGroupInput{
		PackageTarget:     packageTarget,
		SemanticKey:       semanticKey,
		SharedInputDigest: sharedInputDigest,
		ProfileDigest:     profileDigest,
	}
	if err := input.Validate(); err != nil {
		return gate.CompileGroupInput{}, false, err
	}
	return input, true, nil
}

// compileGroupTarget 将 Go target 解析为 package 与 profile 语义键。
func compileGroupTarget(targetKind gate.WorkloadTargetKind, target string, profile remoteGoBuildProfile) (string, string, error) {
	semanticKey, err := gate.CompileGroupSemanticKey(targetKind, profile.race)
	if err != nil {
		return "", "", err
	}
	switch targetKind {
	case gate.WorkloadTargetGoTest:
		parsed, err := gate.ParseGoTestTarget(target)
		if err != nil {
			return "", "", err
		}
		return parsed.Package, semanticKey, nil
	case gate.WorkloadTargetGoBenchmark:
		parsed, err := gate.ParseGoBenchmarkTarget(target)
		if err != nil {
			return "", "", err
		}
		return parsed.Package, semanticKey, nil
	default:
		return "", "", fmt.Errorf("workload target kind %q cannot form compile group", targetKind)
	}
}

func (snapshot *remoteGitTreeSnapshot) compileGroupProfileDigest(ctx context.Context, profile remoteGoBuildProfile) (string, error) {
	workerDigest, err := snapshot.workerExecutionDigest(ctx)
	if err != nil {
		return "", err
	}
	material := struct {
		SchemaVersion        uint32 `json:"schema_version"`
		Platform             string `json:"platform"`
		Toolchain            string `json:"toolchain"`
		Race                 bool   `json:"race"`
		GoFlags              string `json:"go_flags"`
		WorkerContractDigest string `json:"worker_contract_digest"`
	}{
		SchemaVersion:        gate.CompileGroupSchemaVersion,
		Platform:             cicontract.TargetPlatform,
		Toolchain:            cicontract.GoToolchainVersion,
		Race:                 profile.race,
		GoFlags:              gate.CanonicalGoFlags(profile.race),
		WorkerContractDigest: workerDigest,
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal compile group profile identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func cloneRemoteCompileGroupInputs(inputs map[string]gate.CompileGroupInput) map[string]gate.CompileGroupInput {
	if len(inputs) == 0 {
		return nil
	}
	clone := make(map[string]gate.CompileGroupInput, len(inputs))
	maps.Copy(clone, inputs)
	return clone
}

// remoteCompileGroupInputsByGateID 将 RunInput 传输 map 转换为 gate planner 的
// 强类型身份 map，并在边界拒绝格式错误或重复的 workload 身份。
func remoteCompileGroupInputsByGateID(inputs map[string]gate.CompileGroupInput) (map[gate.GateID]gate.CompileGroupInput, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	converted := make(map[gate.GateID]gate.CompileGroupInput, len(inputs))
	for workloadID, input := range inputs {
		if workloadID == "" {
			return nil, fmt.Errorf("compile input workload ID is empty")
		}
		if _, _, _, _, err := gate.ParseWorkloadID(workloadID); err != nil {
			return nil, fmt.Errorf("compile input workload ID %q is invalid: %w", workloadID, err)
		}
		if err := input.Validate(); err != nil {
			return nil, fmt.Errorf("compile input workload %q: %w", workloadID, err)
		}
		id := gate.GateID(workloadID)
		if _, duplicate := converted[id]; duplicate {
			return nil, fmt.Errorf("compile input workload ID %q is duplicated", workloadID)
		}
		converted[id] = input
	}
	return converted, nil
}

// remoteCompileGroupInputsForExecution 只投影 exact miss selector 的 compile
// input；worker 支持的每个 miss 必须在 planning 前拥有 Prepare 冻结的输入，缺失
// 时立即失败，不得回退到逐 selector 的 go test 路径。
func remoteCompileGroupInputsForExecution(
	executionIDs []gate.GateID,
	inputs map[string]gate.CompileGroupInput,
) (map[gate.GateID]gate.CompileGroupInput, bool, error) {
	allInputs, err := remoteCompileGroupInputsByGateID(inputs)
	if err != nil {
		return nil, false, err
	}
	converted := make(map[gate.GateID]gate.CompileGroupInput)
	compileAware := false
	for _, id := range executionIDs {
		if !gate.CompileGroupWorkloadSupported(id) {
			continue
		}
		compileAware = true
		input, ok := allInputs[id]
		if !ok {
			return nil, false, fmt.Errorf("compile input is missing for miss workload %q", id)
		}
		converted[id] = input
	}
	return converted, compileAware, nil
}
