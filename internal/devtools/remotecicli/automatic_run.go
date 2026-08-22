package remotecicli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocitransport"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

const (
	automaticRunOutputMaxBytes      = 16 << 20
	automaticRunResultSchemaVersion = "automatic-heteroci-run-result/v1"
)

// AutomaticRunResult 是 local-first workflow 的非权威 terminal result。
type AutomaticRunResult struct {
	SchemaVersion string                   `json:"schema_version"`
	Authority     string                   `json:"authority"`
	Status        string                   `json:"status"`
	RepositoryID  string                   `json:"repository_id"`
	SourceCommit  string                   `json:"source_commit"`
	SourceTree    string                   `json:"source_tree"`
	RunID         string                   `json:"run_id"`
	Fence         heteroci.OwnerFence      `json:"fence"`
	Plan          heteroci.PackingPlan     `json:"plan"`
	Decision      heteroci.RoutingDecision `json:"decision"`
	Leases        []heteroci.RoutingLease  `json:"leases"`
	Units         []AutomaticRunUnitResult `json:"units"`
}

// AutomaticRunUnitResult 是单个 workload 的 durable local terminal projection。
type AutomaticRunUnitResult struct {
	UnitID  string                     `json:"unit_id"`
	BatchID heterociworkflow.BatchID   `json:"batch_id"`
	State   heteroci.RoutingLeaseState `json:"state"`
}

type automaticExecutorBinder struct {
	executors map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor
}

// Bind 一次返回自动 route 计划要求的 exact local node executor map。
func (binder automaticExecutorBinder) Bind(plan heterociworkflow.ExecutorBindingPlan) (map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, error) {
	if err := plan.Validate(); err != nil {
		return nil, errors.New("automatic executor binding plan is invalid")
	}
	bound := make(map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, len(plan.Bindings))
	for _, binding := range plan.Bindings {
		executor, ok := binder.executors[binding.Key]
		if !ok {
			return nil, errors.New("automatic executor binding is unavailable")
		}
		if executor == nil {
			return nil, errors.New("automatic executor binding is nil")
		}
		bound[binding.Key] = executor
	}
	return bound, nil
}

// dispatchAutomaticRun 执行同一 automatic config 生成的 durable local-first workflow。
func dispatchAutomaticRun(args []string, stdout, stderr io.Writer) error {
	configPath, repositoryPath, runID, err := parseAutomaticRunArgs(args)
	if err != nil {
		return err
	}
	result, err := runAutomaticExecution(context.Background(), configPath, repositoryPath, runID, stderr)
	if err != nil {
		return err
	}
	if err := writeAutomaticRunResult(stdout, result); err != nil {
		return err
	}
	return nil
}

// automatic scheduler composition seam 尚未开放；后续 scheduler wiring 必须替换此签名断言而非公开手动 dispatch。
var _ func([]string, io.Writer, io.Writer) error = dispatchAutomaticRun

// writeAutomaticRunResult 在写 stdout 前强制 terminal result 总字节上限。
func writeAutomaticRunResult(stdout io.Writer, result AutomaticRunResult) error {
	if err := validateAutomaticRunResult(result); err != nil {
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return newCLIFailure("encode automatic run result", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > automaticRunOutputMaxBytes {
		return newCLIFailure("automatic run result exceeds output limit", nil)
	}
	if _, err := stdout.Write(encoded); err != nil {
		return newCLIFailure("write automatic run result", err)
	}
	return nil
}

// validateAutomaticRunResult 拒绝非权威 terminal 结果的 schema、authority 或 status 漂移。
func validateAutomaticRunResult(result AutomaticRunResult) error {
	if result.SchemaVersion != automaticRunResultSchemaVersion {
		return newCLIFailure("automatic run result schema is invalid", nil)
	}
	if result.Authority != heteroci.AuthorityLocalNonAuthoritative {
		return newCLIFailure("automatic run result authority is invalid", nil)
	}
	if result.Status != heterociworkflow.ResultStatusNotVerified {
		return newCLIFailure("automatic run result status is invalid", nil)
	}
	return nil
}

// parseAutomaticRunArgs 只接受 config、repository 和 run identity。
func parseAutomaticRunArgs(args []string) (string, string, string, error) {
	if len(args) != 6 || args[0] != "--config" || args[2] != "--repository" || args[4] != "--run-id" {
		return "", "", "", newCLIFailure("automatic run usage: run --config <absolute-file> --repository <absolute-root> --run-id <identity>", nil)
	}
	if err := validateAbsolutePathComponents(args[1], false); err != nil {
		return "", "", "", newCLIFailure("automatic run config path is invalid", err)
	}
	if err := validateAbsolutePathComponents(args[3], true); err != nil {
		return "", "", "", newCLIFailure("automatic run repository path is invalid", err)
	}
	if !validAutomaticIdentity(args[5]) {
		return "", "", "", newCLIFailure("automatic run identity is invalid", nil)
	}
	return args[1], args[3], args[5], nil
}

// runAutomaticExecution 组装 durable owner、receipt、probe、binder 并执行一次 workflow。
func runAutomaticExecution(ctx context.Context, configPath, repositoryPath, runID string, stderr io.Writer) (result AutomaticRunResult, returnErr error) {
	inputs, err := prepareAutomaticRouteInputs(ctx, configPath, repositoryPath)
	if err != nil {
		return result, err
	}
	defer inputs.cancel()
	clock := func() time.Time { return time.Now().UTC() }
	ownerDB, err := platformsqlite.Open(inputs.ctx, platformsqlite.OpenOptions{Path: inputs.config.OwnerDBPath})
	if err != nil {
		return result, newCLIFailure("open automatic owner DB", err)
	}
	defer func() { returnErr = errors.Join(returnErr, ownerDB.Close()) }()
	owner, err := heterociowner.NewOwnerWithClock(inputs.ctx, ownerDB, inputs.ownerScope, clock)
	if err != nil {
		return result, newCLIFailure("initialize automatic owner", err)
	}
	receipts, err := heterociexec.OpenSQLiteReceiptStore(inputs.ctx, inputs.config.ReceiptDBPath)
	if err != nil {
		return result, newCLIFailure("open automatic receipt store", err)
	}
	defer func() { returnErr = errors.Join(returnErr, receipts.Close()) }()
	binder, err := buildAutomaticExecutorBinder(inputs, receipts, stderr)
	if err != nil {
		return result, err
	}
	transport, err := buildAutomaticProbeTransport(inputs)
	if err != nil {
		return result, err
	}
	units, err := remoteportable.ProjectPortableWorkloadBootstrapExecutionUnits(inputs.repository.Workloads)
	if err != nil {
		return result, newCLIFailure("project automatic workloads", err)
	}
	runResult, err := heterociworkflow.Execute(inputs.ctx, heterociworkflow.Dependencies{
		Owner: owner, ProbeTransport: transport, Executors: binder,
		LoadManifest: heteroci.LoadManifestFile, AcquireSnapshot: heteroci.AcquireSnapshot,
		Now: clock, CleanupTimeout: time.Duration(inputs.config.CleanupTimeoutMS) * time.Millisecond,
	}, heterociworkflow.RunRequest{
		ManifestPath:  inputs.config.TopologyManifestPath,
		RepositoryID:  inputs.repository.RepositoryID,
		SourceTreeSHA: inputs.repository.TreeSHA,
		RunID:         runID,
		Units:         units,
		HeartbeatTTL:  time.Duration(inputs.config.HeartbeatTTLMS) * time.Millisecond,
	})
	if err != nil {
		return result, newCLIFailure("execute automatic local workflow", err)
	}
	return projectAutomaticRunResult(inputs.repository, runID, runResult), nil
}

func buildAutomaticProbeTransport(inputs automaticRouteInputs) (heteroci.ProbeTransport, error) {
	config := inputs.config
	requester, err := heterocitransport.NewSSHRequester(heterocitransport.SSHRequesterConfig{
		SSHExecutable: config.SSHExecutable, Manifest: inputs.manifest, NodeCommands: inputs.commands,
		Timeout: time.Duration(config.ProbeTimeoutMS) * time.Millisecond, WaitDelay: time.Duration(config.ProbeWaitDelayMS) * time.Millisecond,
	})
	if err != nil {
		return nil, newCLIFailure("construct automatic SSH requester", err)
	}
	transport, err := heterocitransport.New(requester)
	if err != nil {
		return nil, newCLIFailure("construct automatic probe transport", err)
	}
	return transport, nil
}

func buildAutomaticExecutorBinder(inputs automaticRouteInputs, receipts heterociexec.ReceiptStore, logs io.Writer) (automaticExecutorBinder, error) {
	moduleIdentity, err := deriveDetectedRepositoryModuleIdentity(inputs.ctx, inputs.repository)
	if err != nil {
		return automaticExecutorBinder{}, newCLIFailure("derive module cache identity", err)
	}
	bindings := make(map[string]AutomaticProbeNode, len(inputs.config.Nodes))
	for _, binding := range inputs.config.Nodes {
		bindings[binding.NodeID] = binding
	}
	executors := make(map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, len(inputs.manifest.Nodes))
	for _, node := range inputs.manifest.Nodes {
		executor, err := buildAutomaticNodeExecutor(inputs, node, bindings[node.NodeID], moduleIdentity, receipts, logs)
		if err != nil {
			return automaticExecutorBinder{}, err
		}
		executors[heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: node.NodeID}] = executor
	}
	return automaticExecutorBinder{executors: executors}, nil
}

func buildAutomaticNodeExecutor(inputs automaticRouteInputs, node heteroci.NodeConfig, binding AutomaticProbeNode, moduleIdentity string, receipts heterociexec.ReceiptStore, logs io.Writer) (heterociworkflow.BatchExecutor, error) {
	runnerConfig, err := automaticNodeRunnerConfig(inputs, node, binding, moduleIdentity, logs)
	if err != nil {
		return nil, err
	}
	runner, err := NewAutomaticSSHRunner(runnerConfig)
	if err != nil {
		return nil, newCLIFailure("construct automatic node runner", err)
	}
	factory := automaticNodeCommandFactory(inputs.repository.Workloads, node.Platform, binding)
	executor, err := heterociexec.NewCommandBatchExecutor(factory, runner, receipts)
	if err != nil {
		return nil, newCLIFailure("construct automatic batch executor", err)
	}
	return executor, nil
}

// automaticNodeCommandFactory 把 batch unit 映射为唯一 node batch request。
func automaticNodeCommandFactory(workloads []remoteportable.PortableManifestWorkload, platform heteroci.Platform, binding AutomaticProbeNode) heterociexec.CommandFactory {
	byID := make(map[string]remoteportable.PortableManifestWorkload, len(workloads))
	for _, workload := range workloads {
		byID[workload.UnitID] = workload
	}
	return func(batch heterociworkflow.ExecutionBatch) (heterociexec.Command, error) {
		request := heterocinode.BatchRequest{SchemaVersion: heterocinode.BatchRequestSchemaVersion, GoExecutable: binding.GoExecutable}
		for _, unit := range batch.Units {
			workload, ok := byID[unit.UnitID]
			if !ok {
				return heterociexec.Command{}, errors.New("automatic batch workload is missing")
			}
			request.Workloads = append(request.Workloads, automaticBatchWorkload(workload))
		}
		encoded, err := heterocinode.EncodeBatchRequest(request)
		if err != nil {
			return heterociexec.Command{}, err
		}
		worker := binding.WorkerExecutable
		if platform == heteroci.PlatformLinuxAMD64 {
			worker = "/opt/super-dolphin/bin/super-dolphin-heteroci-node"
		}
		return heterociexec.EncodeCommand(platform, []string{worker, "batch", "--request-base64", base64.StdEncoding.EncodeToString(encoded)})
	}
}

func automaticBatchWorkload(workload remoteportable.PortableManifestWorkload) heterocinode.BatchWorkload {
	return heterocinode.BatchWorkload{
		UnitID: workload.UnitID, Kind: workload.Kind, ModuleRoot: workload.ModuleRoot, Package: workload.Package, Name: workload.Name,
		ExecutionPolicy: heterocinode.BatchExecutionPolicy(workload.ExecutionPolicy),
	}
}

// automaticNodeRunnerConfig 从 detected repository 与 node binding 派生平台 runner。
func automaticNodeRunnerConfig(inputs automaticRouteInputs, node heteroci.NodeConfig, binding AutomaticProbeNode, moduleIdentity string, logs io.Writer) (AutomaticSSHRunnerConfig, error) {
	host, ok := strings.CutPrefix(node.Endpoint, "ssh://")
	if !ok || !validAutomaticIdentity(host) {
		return AutomaticSSHRunnerConfig{}, errors.New("automatic node SSH endpoint is invalid")
	}
	repositoryPath := joinAutomaticRemotePath(binding.RepositoryBasePath, inputs.repository.RepositoryID)
	base := AutomaticSSHRunnerConfig{
		SSHExecutable: inputs.config.SSHExecutable, Host: host, Platform: node.Platform,
		Timeout:   time.Duration(inputs.config.RunTimeoutMS) * time.Millisecond,
		WaitDelay: time.Duration(inputs.config.ProbeWaitDelayMS) * time.Millisecond, LogWriter: logs,
	}
	workloadIdentity := inputs.repository.ManifestBlobSHA256
	if node.Platform == heteroci.PlatformLinuxAMD64 {
		base.Linux = &AutomaticLinuxRunnerConfig{
			RunAsUser: binding.RunAsUser, DockerGroup: binding.DockerGroup, RunnerExecutable: binding.RunnerExecutable, RepoPath: repositoryPath, SharedRoot: binding.SharedRoot, CacheRoot: binding.CacheRoot,
			RepositoryID: inputs.repository.RepositoryID, NodeID: node.NodeID, ObjectFormat: inputs.repository.ObjectFormat,
			SourceCommit: inputs.repository.CommitSHA, SourceTree: inputs.repository.TreeSHA, ToolchainIdentity: binding.ToolchainIdentity,
			ModuleIdentity: moduleIdentity, WorkloadIdentity: workloadIdentity, DockerImage: binding.DockerImage,
			WorkerBinary: binding.WorkerExecutable, WorkerIdentity: binding.WorkerIdentity,
		}
		return base, nil
	}
	if node.Platform == heteroci.PlatformWindowsAMD64 {
		base.Windows = &AutomaticWindowsRunnerConfig{
			RunnerExecutable: binding.RunnerExecutable, RepoPath: repositoryPath, ObjectStoreRoot: joinAutomaticRemotePath(repositoryPath, ".git"),
			WorktreeRoot: repositoryPath, CacheRoot: binding.CacheRoot, RepositoryID: inputs.repository.RepositoryID, NodeID: node.NodeID,
			SourceCommit: inputs.repository.CommitSHA, SourceTree: inputs.repository.TreeSHA, ToolchainIdentity: binding.ToolchainIdentity,
			ModuleIdentity: moduleIdentity, WorkloadIdentity: workloadIdentity,
		}
		return base, nil
	}
	return AutomaticSSHRunnerConfig{}, errors.New("automatic node platform is unsupported")
}

func joinAutomaticRemotePath(base, element string) string {
	if strings.Contains(base, ":/") {
		return strings.TrimRight(base, "/") + "/" + element
	}
	return path.Join(base, element)
}

func projectAutomaticRunResult(repository DetectedRepository, runID string, result heterociworkflow.RunResult) AutomaticRunResult {
	units := make([]AutomaticRunUnitResult, len(result.Units))
	for index, unit := range result.Units {
		units[index] = AutomaticRunUnitResult{UnitID: unit.UnitID, BatchID: unit.BatchID, State: unit.State}
	}
	return AutomaticRunResult{
		SchemaVersion: automaticRunResultSchemaVersion, Authority: result.Authority, Status: result.Status,
		RepositoryID: repository.RepositoryID, SourceCommit: repository.CommitSHA, SourceTree: repository.TreeSHA, RunID: runID,
		Fence: result.Fence, Plan: result.Plan, Decision: result.Decision, Leases: result.Leases, Units: units,
	}
}
