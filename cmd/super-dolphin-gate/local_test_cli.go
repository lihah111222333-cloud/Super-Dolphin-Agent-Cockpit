package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	projectmaptrusted "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/projectmaptrusted"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// validateWorkloadAuthorityTarget 统一校验 CLI authority 选择。
func validateWorkloadAuthorityTarget(target string) error {
	switch strings.TrimSpace(target) {
	case "local", "remote", "auto", "hybrid":
		return nil
	default:
		return protocolError("invalid workload target %q", target)
	}
}

// mergeWorkloadSelections 合并 flag 与 manifest，拒绝空值和重复项。
func mergeWorkloadSelections(flags remoteStringListFlag, manifestPath string) ([]string, error) {
	selected := append([]string(nil), flags...)
	if strings.TrimSpace(manifestPath) != "" {
		manifest, err := loadLocalWorkloadManifest(manifestPath)
		if err != nil {
			return nil, err
		}
		selected = append(selected, manifest...)
	}
	seen := make(map[string]struct{}, len(selected))
	for index, value := range selected {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("workload selection %d is empty", index)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("duplicate workload ID %q", value)
		}
		seen[value] = struct{}{}
		selected[index] = value
	}
	return selected, nil
}

type localWorkloadManifest []string

// Validate 拒绝 manifest 内的空 workload ID。
func (manifest localWorkloadManifest) Validate() error {
	for index, value := range manifest {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("workload manifest entry %d is empty", index)
		}
	}
	return nil
}

// loadLocalWorkloadManifest 只接受绝对路径的严格 JSON 数组。
func loadLocalWorkloadManifest(path string) ([]string, error) {
	if !filepath.IsAbs(path) {
		return nil, protocolError("--gate-workload-manifest must be an absolute path")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read workload manifest: %w", err)
	}
	var values localWorkloadManifest
	if err := gatecontract.DecodeStrictJSON(data, &values); err != nil {
		return nil, fmt.Errorf("decode workload manifest: %w", err)
	}
	if values == nil {
		return nil, errors.New("workload manifest must contain a JSON array")
	}
	if err := values.Validate(); err != nil {
		return nil, err
	}
	return []string(values), nil
}

// validateLocalWorkloadSelection 使用 canonical catalog 校验 CLI 选择。
func validateLocalWorkloadSelection(ids []string, catalog gatecontract.WorkloadCatalog) error {
	if len(ids) == 0 {
		return errors.New("local workload selection is empty")
	}
	known := make(map[string]struct{}, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		if strings.TrimSpace(workload.ID) == "" {
			return errors.New("canonical workload catalog contains an empty ID")
		}
		if _, duplicate := known[workload.ID]; duplicate {
			return fmt.Errorf("canonical workload catalog contains duplicate ID %q", workload.ID)
		}
		known[workload.ID] = struct{}{}
	}
	selected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return errors.New("local workload ID is empty")
		}
		if _, duplicate := selected[id]; duplicate {
			return fmt.Errorf("duplicate workload ID %q", id)
		}
		if _, ok := known[id]; !ok {
			return fmt.Errorf("unknown workload ID %q", id)
		}
		selected[id] = struct{}{}
	}
	return nil
}

// materializeLocalExactTree 只从固定 Git tree 创建隔离工作区。
func materializeLocalExactTree(repository, tree string, trustedGit gatecontract.TrustedGitBinary) (projectmaptrusted.ExactTree, error) {
	if strings.TrimSpace(repository) == "" || strings.TrimSpace(tree) == "" {
		return projectmaptrusted.ExactTree{}, errors.New("local exact-tree materializer requires repository and tree")
	}
	exact, err := projectmaptrusted.MaterializeExactGitTree(repository, tree, "super-dolphin-local-workload-", trustedGit)
	if err != nil {
		return projectmaptrusted.ExactTree{}, err
	}
	if exact.Cleanup == nil || strings.TrimSpace(exact.SourceRoot) == "" || exact.SourceTreeSHA != tree {
		return projectmaptrusted.ExactTree{}, errors.New("local exact-tree materialization returned incomplete cleanup proof")
	}
	return exact, nil
}

type localTestCLIPlan struct {
	Store   *gatecontract.DurationLedgerStore
	Input   gatecontract.LocalWorkloadSchedulerInput
	Catalog gatecontract.WorkloadCatalog
}

// localRemoteSubsetOutcome 保留 remote subset 的既有权威输出材料；local 证据绝不写入其中。
type localRemoteSubsetOutcome struct {
	Called bool
	Input  remoteci.RunInput
	Result remoteci.RunResult
}

type localTestCLIAdapter struct {
	Prepare             func(context.Context, remoteRunOptions) (localTestCLIPlan, error)
	RequireRemoteToken  func([]string, []string, io.Writer) (string, error)
	ExecuteRemoteSubset func(context.Context, remoteRunOptions, []gatecontract.GateID, string) (localRemoteSubsetOutcome, error)
}

// localGateWorkloadIDs 转换已解析 selector，空选择立即阻断。
func localGateWorkloadIDs(values []string) ([]gatecontract.GateID, error) {
	if len(values) == 0 {
		return nil, protocolError("test target requires --gate-workload or --gate-workload-manifest")
	}
	ids := make([]gatecontract.GateID, len(values))
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, protocolError("gate workload ID is empty")
		}
		ids[index] = gatecontract.GateID(value)
	}
	return ids, nil
}

// runLocalTestInvocation 保持 local PASS first，local failure 不得回退远程。
func runLocalTestInvocation(ctx context.Context, args []string, options remoteRunOptions, stdout io.Writer, adapter localTestCLIAdapter) error {
	if ctx == nil {
		return errors.New("local test CLI context is required")
	}
	plan, err := prepareLocalTestCLIPlan(ctx, options, adapter)
	if err != nil {
		return err
	}
	if err := validateLocalTestCLIPlan(options, &plan); err != nil {
		return err
	}
	prepared, err := gatecontract.PrepareLocalWorkloadSchedule(ctx, plan.Store, plan.Input)
	if err != nil {
		return err
	}
	prepared, err = gatecontract.RunLocalWorkloadMisses(ctx, plan.Store, plan.Input, prepared)
	if err != nil {
		return emitFrozenLocalTestInvocationOutcome(stdout, options.Target, prepared, localTestInvocationOutcome{LocalErr: err})
	}
	if len(prepared.Remote) == 0 {
		return emitFrozenLocalTestInvocationOutcome(stdout, options.Target, prepared, localTestInvocationOutcome{})
	}
	remote, remoteErr := runLocalRemoteSubset(ctx, args, options, stdout, adapter, &plan.Input, &prepared)
	return emitLocalTestInvocationOutcome(stdout, options.Target, prepared, remote, remoteErr)
}

// prepareLocalTestCLIPlan 校验 adapter 的不可变计划交接。
func prepareLocalTestCLIPlan(ctx context.Context, options remoteRunOptions, adapter localTestCLIAdapter) (localTestCLIPlan, error) {
	if adapter.Prepare == nil {
		return localTestCLIPlan{}, errors.New("local test CLI producer is required")
	}
	plan, err := adapter.Prepare(ctx, options)
	if err != nil {
		return localTestCLIPlan{}, err
	}
	if plan.Store == nil {
		return localTestCLIPlan{}, errors.New("local test CLI producer returned a nil ledger store")
	}
	return plan, nil
}

// validateLocalTestCLIPlan 在调度前复核 selection 与 authority target。
func validateLocalTestCLIPlan(options remoteRunOptions, plan *localTestCLIPlan) error {
	if plan == nil {
		return errors.New("local test CLI plan is required")
	}
	if err := validateLocalWorkloadSelection(options.GateWorkloadIDs, plan.Catalog); err != nil {
		return protocolError("validate --gate-workload selection: %v", err)
	}
	return validateLocalTestSchedulerTarget(options, &plan.Input)
}

// runLocalRemoteSubset 仅在 frozen remote 分区非空后握手 token，并保留 remote 输出材料。
func runLocalRemoteSubset(ctx context.Context, args []string, options remoteRunOptions, stdout io.Writer, adapter localTestCLIAdapter, input *gatecontract.LocalWorkloadSchedulerInput, prepared *gatecontract.LocalWorkloadScheduleResult) (localRemoteSubsetOutcome, error) {
	if input == nil || prepared == nil {
		return localRemoteSubsetOutcome{}, errors.New("remote subset scheduler state is required")
	}
	if strings.TrimSpace(options.ConfigPath) == "" {
		return localRemoteSubsetOutcome{}, protocolError("remote subset requires --config")
	}
	if adapter.RequireRemoteToken == nil || adapter.ExecuteRemoteSubset == nil {
		return localRemoteSubsetOutcome{}, errors.New("remote subset adapter is required")
	}
	digest, err := adapter.RequireRemoteToken([]string{"test"}, args, stdout)
	if err != nil {
		return localRemoteSubsetOutcome{}, err
	}
	var outcome localRemoteSubsetOutcome
	input.RemoteExecute = func(remoteCtx context.Context, ids []gatecontract.GateID) error {
		result, executeErr := adapter.ExecuteRemoteSubset(remoteCtx, options, ids, digest)
		result.Called = true
		outcome = result
		return executeErr
	}
	runErr := gatecontract.RunSelectedRemoteWorkloads(ctx, *input, prepared)
	return outcome, runErr
}

type localTestAuthorityOutput struct {
	Authority        string                                  `json:"authority"`
	Target           string                                  `json:"target"`
	Status           string                                  `json:"status"`
	LocalOutcome     string                                  `json:"local_outcome"`
	Stats            gatecontract.LocalWorkloadScheduleStats `json:"stats"`
	LocalEvidenceIDs []string                                `json:"local_evidence_ids"`
	Error            string                                  `json:"error,omitempty"`
}

// localTestInvocationOutcome 冻结 local 结论，并把之后 remote 阶段的材料和错误隔离。
type localTestInvocationOutcome struct {
	LocalErr  error
	Remote    localRemoteSubsetOutcome
	RemoteErr error
}

// emitLocalTestInvocationOutcome 先报告本地非权威事实，再原样交给远程 emitter 输出其独立回执。
func emitLocalTestInvocationOutcome(stdout io.Writer, target string, prepared gatecontract.LocalWorkloadScheduleResult, remote localRemoteSubsetOutcome, remoteErr error) error {
	return emitFrozenLocalTestInvocationOutcome(stdout, target, prepared, localTestInvocationOutcome{Remote: remote, RemoteErr: remoteErr})
}

// emitFrozenLocalTestInvocationOutcome 只让 frozen local 错误影响 local JSON；remote 错误只走 remote emitter 或命令返回。
func emitFrozenLocalTestInvocationOutcome(stdout io.Writer, target string, prepared gatecontract.LocalWorkloadScheduleResult, outcome localTestInvocationOutcome) error {
	localEmitErr := emitLocalTestAuthorityOutput(stdout, target, prepared, outcome.LocalErr)
	if outcome.LocalErr != nil {
		return errors.Join(outcome.LocalErr, localEmitErr)
	}
	if !outcome.Remote.Called {
		return errors.Join(outcome.RemoteErr, localEmitErr)
	}
	return errors.Join(localEmitErr, emitRemoteRunResult(stdout, outcome.Remote.Input.LedgerStore, outcome.Remote.Result, outcome.RemoteErr))
}

// emitLocalTestAuthorityOutput 只编码本地开发证据，明确其从不构成 remote 或 release authority。
func emitLocalTestAuthorityOutput(stdout io.Writer, target string, prepared gatecontract.LocalWorkloadScheduleResult, runErr error) error {
	output := localTestAuthorityOutput{
		Authority:        "LOCAL_NON_AUTHORITATIVE",
		Target:           target,
		Status:           localTestOutputStatus(runErr),
		LocalOutcome:     localTestOutputOutcome(prepared),
		Stats:            prepared.Stats,
		LocalEvidenceIDs: localTestEvidenceIDs(prepared),
	}
	if runErr != nil {
		output.Error = runErr.Error()
	}
	if err := json.NewEncoder(stdout).Encode(output); err != nil {
		return infrastructureError("encode local non-authoritative result: %v", err)
	}
	return nil
}

// localTestOutputStatus 将失败保留为失败，禁止被已经完成的 local PASS 覆盖。
func localTestOutputStatus(runErr error) string {
	if runErr != nil {
		return "FAILED"
	}
	return "PASS"
}

// localTestOutputOutcome 让全命中结果显式声明为 local hit。
func localTestOutputOutcome(prepared gatecontract.LocalWorkloadScheduleResult) string {
	if prepared.Stats.LocalHits != 0 && prepared.Stats.LocalMisses == 0 && len(prepared.Remote) == 0 {
		return "LOCAL_HIT"
	}
	if prepared.Stats.LocalExecuted != 0 {
		return "LOCAL_EXECUTED"
	}
	if len(prepared.Remote) != 0 {
		return "REMOTE_SUBSET"
	}
	return "LOCAL_NOOP"
}

// localTestEvidenceIDs 仅投影 local evidence 的 workload ID，按稳定顺序输出且不接触 remote receipt。
func localTestEvidenceIDs(prepared gatecontract.LocalWorkloadScheduleResult) []string {
	seen := make(map[string]struct{}, len(prepared.Hits)+len(prepared.Evidence))
	for _, evidence := range append(prepared.Hits, prepared.Evidence...) {
		seen[string(evidence.Identity.WorkloadID)] = struct{}{}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// validateLocalTestSchedulerTarget 将 CLI target 交给 gate scheduler。
func validateLocalTestSchedulerTarget(options remoteRunOptions, input *gatecontract.LocalWorkloadSchedulerInput) error {
	if input == nil {
		return errors.New("local test scheduler input is required")
	}
	input.Target = gatecontract.LocalWorkloadScheduleTarget(options.Target)
	return nil
}
