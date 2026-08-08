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

// remoteWorkloadFingerprintsWithSnapshot 保留首次 Prepare 的 exact-tree
// snapshot；closure 只在确认 MISS 后按需捕获，all-hit 不承担迁移材料成本。
func remoteWorkloadFingerprintsWithSnapshot(
	ctx context.Context,
	repositoryRoot string,
	tree string,
	workloads []gate.Workload,
) (map[string]string, map[string]gate.CompileGroupInput, *remoteGitTreeSnapshot, error) {
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, tree)
	if err != nil {
		return nil, nil, nil, err
	}
	inputDigests, compileInputs, err := snapshot.remoteWorkloadFingerprints(ctx, workloads)
	if err != nil {
		return nil, nil, nil, err
	}
	return inputDigests, compileInputs, snapshot, nil
}

func (snapshot *remoteGitTreeSnapshot) remoteWorkloadFingerprints(
	ctx context.Context,
	workloads []gate.Workload,
) (map[string]string, map[string]gate.CompileGroupInput, error) {
	inputDigests, err := snapshot.concurrentRemoteWorkloadInputDigests(ctx, workloads)
	if err != nil {
		return nil, nil, err
	}
	compileInputs := make(map[string]gate.CompileGroupInput)
	profileDigests := make(map[bool]string, 2)
	for _, workload := range workloads {
		compileInput, ok, err := snapshot.compileGroupInputForWorkload(ctx, workload, profileDigests)
		if err != nil {
			return nil, nil, fmt.Errorf("fingerprint compile input for workload %q: %w", workload.ID, err)
		}
		if ok {
			compileInputs[workload.ID] = compileInput
		}
	}
	return inputDigests, compileInputs, nil
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

// runRemoteWorkloadInputDigestWorkers 并行执行全部 exact selector；任务数量由冻结
// catalog 唯一决定，不引入产品并发阈值，也不影响远程 ECI fanout 语义。
func (snapshot *remoteGitTreeSnapshot) runRemoteWorkloadInputDigestWorkers(
	ctx context.Context,
	workloads []gate.Workload,
	indexes []int,
	results []remoteWorkloadInputDigestResult,
) {
	var group errgroup.Group
	for _, index := range indexes {
		group.Go(func() error {
			results[index].digest, results[index].err = snapshot.workloadInputDigest(ctx, workloads[index])
			return nil
		})
	}
	_ = group.Wait()
}

// remoteExactGoSelectorWorkload 只选择共享 snapshot cache 已具备并发保护的
// exact Go test/benchmark，其他 target 保持原执行顺序。
func remoteExactGoSelectorWorkload(workload gate.Workload) bool {
	_, kind, _, targeted, err := gate.ParseWorkloadID(workload.ID)
	return err == nil && targeted && (kind == gate.WorkloadTargetGoTest || kind == gate.WorkloadTargetGoBenchmark)
}

func validateRemoteCompileGroupInputs(catalog gate.WorkloadCatalog, inputs map[string]gate.CompileGroupInput) error {
	expected := make(map[string]struct{})
	for _, workload := range catalog.Workloads {
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
		WorkerContractDigest string `json:"worker_contract_digest"`
	}{
		SchemaVersion:        gate.CompileGroupSchemaVersion,
		Platform:             cicontract.TargetPlatform,
		Toolchain:            cicontract.GoToolchainVersion,
		Race:                 profile.race,
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
