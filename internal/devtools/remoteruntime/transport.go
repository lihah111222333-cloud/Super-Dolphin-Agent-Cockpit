package remoteruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

// Operation 是 runtime transport 的单次 bounded 操作。
type Operation string

const (
	// OperationProbe 调用 node probe executable。
	OperationProbe Operation = "probe"
	// OperationRun 调用 node runner executable，并显式绑定 worker。
	OperationRun Operation = "run"
	// OperationWorker 调用 node worker 的一次工作请求。
	OperationWorker Operation = "worker"
)

var (
	// ErrInvalidTransportDependencies 表示缺少显式 command runner 或 config。
	ErrInvalidTransportDependencies = errors.New("runtime transport dependencies are invalid")
	// ErrInvalidTransportRequest 表示 node、operation 或 request 违反输入边界。
	ErrInvalidTransportRequest = errors.New("runtime transport request is invalid")
	// ErrCommandFailed 表示 SSH command 未以零退出或无法启动。
	ErrCommandFailed = errors.New("runtime transport command failed")
	// ErrCommandTimedOut 表示 bounded context 到期或被取消。
	ErrCommandTimedOut = errors.New("runtime transport command timed out")
	// ErrNodeResultInvalid 表示 stdout 不是唯一 canonical node result。
	ErrNodeResultInvalid = errors.New("runtime transport node result is invalid")
)

// CommandSpec 是 command runner 的不可变、已派生 argv 视图。
type CommandSpec struct {
	// Executable 是 config 派生的 SSH executable。
	Executable string
	// Args 是 binding、target facts 和 operation 派生的完整 argv。
	Args []string
	// Stdout 是 bounded canonical result channel。
	Stdout io.Writer
	// Stderr 是 bounded、不会投影到 result 的远端日志 channel。
	Stderr io.Writer
}

// CommandResult 是 command runner 返回的稳定进程状态。
type CommandResult struct {
	// ExitCode 是 command runner 观察到的进程退出码。
	ExitCode int
}

// CommandRunner 是可注入的跨平台 command 执行端口。
type CommandRunner interface {
	// Run 在调用方提供的 bounded context 中执行完整 command spec。
	Run(context.Context, CommandSpec) (CommandResult, error)
}

// CommandRunnerFunc 将函数适配为 CommandRunner，便于 Linux/Windows 测试注入。
type CommandRunnerFunc func(context.Context, CommandSpec) (CommandResult, error)

// Run 调用注入的 command 函数。
func (function CommandRunnerFunc) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	if function == nil {
		return CommandResult{}, ErrInvalidTransportDependencies
	}
	return function(ctx, spec)
}

// ExecCommandRunner 使用 remoteportable.ConfigureBoundedCommand 执行真实跨平台进程。
type ExecCommandRunner struct{}

// Run 创建 bounded exec.Cmd，分别收集 stdout/stderr，并不把 stderr 投影到结果。
func (ExecCommandRunner) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	if ctx == nil || spec.Executable == "" || spec.Stdout == nil || spec.Stderr == nil {
		return CommandResult{}, ErrInvalidTransportDependencies
	}
	command := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	if err := remoteportable.ConfigureBoundedCommand(command); err != nil {
		return CommandResult{}, errors.Join(ErrCommandFailed, err)
	}
	command.Stdout = spec.Stdout
	command.Stderr = spec.Stderr
	err := command.Run()
	result := CommandResult{ExitCode: 0}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if err == nil {
		return result, nil
	}
	if result.ExitCode == 0 {
		result.ExitCode = -1
	}
	return result, err
}

// NodeResult 是 transport 解析出的唯一 canonical stdout object。
type NodeResult struct {
	// NodeID 是 stdout result 中经过 binding 校验的节点身份。
	NodeID string
	// Identity 是 stdout result 中经过 binding 校验的执行身份。
	Identity string
	// Raw 是唯一 canonical JSON object 的独立副本。
	Raw []byte
	// CleanupVerified 仅表示 run/worker 的 SSH process 零退出且 strict result 解码成功；probe 永远为 false。
	CleanupVerified bool
}

// Bytes 返回 node result 的独立 canonical JSON 副本。
func (result NodeResult) Bytes() []byte {
	return slices.Clone(result.Raw)
}

// Transport 将严格 RuntimeConfig 绑定到一个显式 command runner。
type Transport struct {
	config RuntimeConfig
	runner CommandRunner
	nodes  map[string]NodeBinding
}

// New 构造 provider-neutral runtime transport；nil runner 会立即失败，不提供默认替代。
func New(config RuntimeConfig, runner CommandRunner) (*Transport, error) {
	if runner == nil || isNilInterface(runner) {
		return nil, ErrInvalidTransportDependencies
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("runtime transport config: %w", err)
	}
	nodes := make(map[string]NodeBinding, len(config.Nodes))
	for _, node := range config.Nodes {
		nodes[node.NodeID] = node
	}
	return &Transport{config: config, runner: runner, nodes: nodes}, nil
}

// NewExecTransport 构造真实 exec transport；调用方仍须显式传入完整 config。
func NewExecTransport(config RuntimeConfig) (*Transport, error) {
	return New(config, ExecCommandRunner{})
}

// Config 返回 transport 绑定的不可变 config 副本。
func (transport *Transport) Config() RuntimeConfig {
	if transport == nil {
		return RuntimeConfig{}
	}
	config := transport.config
	config.Nodes = slices.Clone(config.Nodes)
	return config
}

// ProbeCommand 构造不启动远端进程的 probe SSH argv；fresh capacity probe 不需要 execution facts。
func (transport *Transport) ProbeCommand(target TargetFacts, nodeID string, request []byte) (CommandSpec, error) {
	if transport == nil || transport.runner == nil {
		return CommandSpec{}, ErrInvalidTransportDependencies
	}
	node, ok := transport.nodes[nodeID]
	if !ok {
		return CommandSpec{}, fmt.Errorf("%w: node identity", ErrInvalidTransportRequest)
	}
	if err := validateRequest(request); err != nil {
		return CommandSpec{}, err
	}
	if err := target.Validate(); err != nil {
		return CommandSpec{}, err
	}
	args, err := transport.probeSSHArgs(node, request)
	if err != nil {
		return CommandSpec{}, err
	}
	return CommandSpec{Executable: transport.config.SSHExecutable, Args: args}, nil
}

// RunCommand 构造 runner SSH argv；每次 batch 必须显式提供 execution facts。
func (transport *Transport) RunCommand(target TargetFacts, execution ExecutionFacts, nodeID string, request []byte) (CommandSpec, error) {
	return transport.batchCommand(OperationRun, target, execution, nodeID, request)
}

// WorkerCommand 构造 worker SSH argv；每次 batch 必须显式提供 execution facts。
func (transport *Transport) WorkerCommand(target TargetFacts, execution ExecutionFacts, nodeID string, request []byte) (CommandSpec, error) {
	return transport.batchCommand(OperationWorker, target, execution, nodeID, request)
}

// batchCommand 构造 run/worker 的完整 SSH argv；每次 batch 必须显式提供 execution facts。
func (transport *Transport) batchCommand(operation Operation, target TargetFacts, execution ExecutionFacts, nodeID string, request []byte) (CommandSpec, error) {
	if transport == nil || transport.runner == nil {
		return CommandSpec{}, ErrInvalidTransportDependencies
	}
	node, ok := transport.nodes[nodeID]
	if !ok {
		return CommandSpec{}, fmt.Errorf("%w: node identity", ErrInvalidTransportRequest)
	}
	if err := validateBatchOperation(operation); err != nil {
		return CommandSpec{}, err
	}
	if err := validateRequest(request); err != nil {
		return CommandSpec{}, err
	}
	if err := target.Validate(); err != nil {
		return CommandSpec{}, err
	}
	if err := execution.ValidateForNode(node); err != nil {
		return CommandSpec{}, err
	}
	args, err := transport.batchSSHArgs(operation, node, target, execution, request)
	if err != nil {
		return CommandSpec{}, err
	}
	return CommandSpec{Executable: transport.config.SSHExecutable, Args: args}, nil
}

// Probe 在 probe timeout 内执行一个 node probe，并只接受唯一 canonical result；fresh capacity 不要求 execution facts。
func (transport *Transport) Probe(ctx context.Context, target TargetFacts, nodeID string, request []byte) (NodeResult, error) {
	node, err := transport.prepareProbeWithContext(ctx, target, nodeID, request)
	if err != nil {
		return NodeResult{}, err
	}
	args, err := transport.probeSSHArgs(node, request)
	if err != nil {
		return NodeResult{}, err
	}
	return transport.runCommand(ctx, OperationProbe, node, args)
}

// Run 在 run timeout 内执行 runner，并只接受唯一 canonical result。
func (transport *Transport) Run(ctx context.Context, target TargetFacts, execution ExecutionFacts, nodeID string, request []byte) (NodeResult, error) {
	return transport.execute(ctx, OperationRun, target, execution, nodeID, request)
}

// Worker 在 worker timeout 内执行一个显式 worker 请求。
func (transport *Transport) Worker(ctx context.Context, target TargetFacts, execution ExecutionFacts, nodeID string, request []byte) (NodeResult, error) {
	return transport.execute(ctx, OperationWorker, target, execution, nodeID, request)
}

// ProbeBytes 返回 Probe 的 raw canonical JSON，便于独立 binary 直接接入协议 decoder。
func (transport *Transport) ProbeBytes(ctx context.Context, target TargetFacts, nodeID string, request []byte) ([]byte, error) {
	result, err := transport.Probe(ctx, target, nodeID, request)
	if err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

// RunBytes 返回 Run 的 raw canonical JSON，便于独立 binary 直接接入协议 decoder。
func (transport *Transport) RunBytes(ctx context.Context, target TargetFacts, execution ExecutionFacts, nodeID string, request []byte) ([]byte, error) {
	result, err := transport.Run(ctx, target, execution, nodeID, request)
	if err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func (transport *Transport) execute(ctx context.Context, operation Operation, target TargetFacts, execution ExecutionFacts, nodeID string, request []byte) (NodeResult, error) {
	node, err := transport.prepareExecution(ctx, operation, target, execution, nodeID, request)
	if err != nil {
		return NodeResult{}, err
	}
	args, err := transport.batchSSHArgs(operation, node, target, execution, request)
	if err != nil {
		return NodeResult{}, err
	}
	return transport.runCommand(ctx, operation, node, args)
}

func (transport *Transport) prepareProbeWithContext(ctx context.Context, target TargetFacts, nodeID string, request []byte) (NodeBinding, error) {
	if transport == nil || transport.runner == nil || ctx == nil {
		return NodeBinding{}, ErrInvalidTransportDependencies
	}
	node, ok := transport.nodes[nodeID]
	if !ok {
		return NodeBinding{}, fmt.Errorf("%w: node identity", ErrInvalidTransportRequest)
	}
	if err := target.Validate(); err != nil {
		return NodeBinding{}, err
	}
	if err := validateRequest(request); err != nil {
		return NodeBinding{}, err
	}
	return node, nil
}

func (transport *Transport) prepareExecution(ctx context.Context, operation Operation, target TargetFacts, execution ExecutionFacts, nodeID string, request []byte) (NodeBinding, error) {
	if transport == nil || transport.runner == nil || ctx == nil {
		return NodeBinding{}, ErrInvalidTransportDependencies
	}
	node, ok := transport.nodes[nodeID]
	if !ok {
		return NodeBinding{}, fmt.Errorf("%w: node identity", ErrInvalidTransportRequest)
	}
	if err := target.Validate(); err != nil {
		return NodeBinding{}, err
	}
	if err := validateBatchOperation(operation); err != nil {
		return NodeBinding{}, err
	}
	if err := execution.ValidateForNode(node); err != nil {
		return NodeBinding{}, err
	}
	if err := validateRequest(request); err != nil {
		return NodeBinding{}, err
	}
	return node, nil
}

func (transport *Transport) runCommand(ctx context.Context, operation Operation, node NodeBinding, args []string) (NodeResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, transport.operationTimeout(operation))
	defer cancel()
	stdout := &boundedWriter{limit: MaxNodeResultBytes}
	stderr := &boundedWriter{limit: MaxNodeStderrBytes}
	result, runErr := transport.runner.Run(runCtx, CommandSpec{Executable: transport.config.SSHExecutable, Args: args, Stdout: stdout, Stderr: stderr})
	if runCtx.Err() != nil {
		return NodeResult{}, errors.Join(ErrCommandTimedOut, runCtx.Err())
	}
	if runErr != nil || result.ExitCode != 0 {
		return NodeResult{}, ErrCommandFailed
	}
	decoded, decodeErr := DecodeNodeResult(stdout.Bytes(), node, operation)
	if decodeErr != nil {
		return NodeResult{}, decodeErr
	}
	if operation == OperationRun || operation == OperationWorker {
		decoded.CleanupVerified = true
	}
	return decoded, nil
}

func (transport *Transport) operationTimeout(operation Operation) time.Duration {
	switch operation {
	case OperationProbe:
		return time.Duration(transport.config.ProbeTimeoutMS) * time.Millisecond
	case OperationRun:
		return time.Duration(transport.config.RunTimeoutMS) * time.Millisecond
	case OperationWorker:
		return time.Duration(transport.config.WorkerTimeoutMS) * time.Millisecond
	default:
		return 0
	}
}

func (transport *Transport) probeSSHArgs(node NodeBinding, request []byte) ([]string, error) {
	token := base64.StdEncoding.EncodeToString(request)
	remoteArgs := []string{node.ProbeExecutable, "probe", "--config", node.ProbeConfigPath, "--probe-request-base64", token}
	return transport.finishSSHArgs(OperationProbe, node, remoteArgs)
}

func (transport *Transport) batchSSHArgs(operation Operation, node NodeBinding, target TargetFacts, execution ExecutionFacts, request []byte) ([]string, error) {
	token := base64.StdEncoding.EncodeToString(request)
	workerArgs := []string{node.WorkerExecutable, "batch", "--request-base64", token}
	remoteArgs, err := operationRemoteArgs(operation, node, target, execution, workerArgs, transport.operationTimeout(operation))
	if err != nil {
		return nil, err
	}
	return transport.finishSSHArgs(operation, node, remoteArgs)
}

func (transport *Transport) finishSSHArgs(operation Operation, node NodeBinding, remoteArgs []string) ([]string, error) {
	for _, argument := range remoteArgs {
		if argument == "" || strings.IndexFunc(argument, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return nil, ErrInvalidTransportRequest
		}
	}
	remoteCommand, err := quoteRemoteCommand(node.Platform, remoteArgs)
	if err != nil {
		return nil, err
	}
	connectSeconds := (transport.operationTimeout(operation) + time.Second - 1) / time.Second
	return []string{
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.FormatInt(int64(connectSeconds), 10),
		"--", endpointHost(node.Endpoint), remoteCommand,
	}, nil
}

func operationRemoteArgs(operation Operation, node NodeBinding, target TargetFacts, execution ExecutionFacts, workerArgs []string, timeout time.Duration) ([]string, error) {
	switch operation {
	case OperationRun:
		if node.Platform == UbuntuPlatform {
			return linuxRunnerArgs(node, target, execution, workerArgs, timeout)
		}
		return windowsRunnerArgs(node, target, execution, workerArgs, timeout)
	case OperationWorker:
		return workerArgs, nil
	default:
		return nil, ErrInvalidTransportRequest
	}
}

func linuxRunnerArgs(node NodeBinding, target TargetFacts, execution ExecutionFacts, workerArgs []string, timeout time.Duration) ([]string, error) {
	if node.Linux == nil {
		return nil, ErrInvalidTransportRequest
	}
	args := []string{node.RunnerExecutable, "--repo", node.RepositoryRoot, "--mode", "docker", "--runtime", "ubuntu", "--docker-image", node.Linux.DockerImage,
		"--worker-binary", node.WorkerExecutable, "--worker-identity", node.WorkerIdentity, "--shared-root", node.ObjectStoreRoot, "--cache-root", node.CachePublicationRoot,
		"--repository-id", target.RepositoryID, "--node-id", node.NodeID, "--object-format", target.ObjectFormat, "--source-commit", target.CommitSHA, "--source-tree", target.TreeSHA,
		"--toolchain-identity", node.ToolchainIdentity, "--goos", execution.GOOS, "--goarch", execution.GOARCH, "--cgo-enabled", execution.CGOEnabled,
		"--module-identity", execution.ModuleIdentity, "--workload-identity", execution.WorkloadIdentity, "--timeout-seconds", timeoutSeconds(timeout), "--"}
	return append(args, workerArgs...), nil
}

func windowsRunnerArgs(node NodeBinding, target TargetFacts, execution ExecutionFacts, workerArgs []string, timeout time.Duration) ([]string, error) {
	command := strings.Join(workerArgs, " ")
	args := []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", node.RunnerExecutable,
		"-RepoPath", node.RepositoryRoot, "-CommandBase64", utf16Base64(command), "-NodeId", node.NodeID, "-RepositoryId", target.RepositoryID,
		"-RepositoryRoot", node.RepositoryRoot, "-ObjectStoreRoot", node.ObjectStoreRoot, "-WorktreeRoot", node.WorktreeRoot, "-CacheRoot", node.CachePublicationRoot,
		"-ToolchainIdentity", node.ToolchainIdentity, "-GOOS", execution.GOOS, "-GOARCH", execution.GOARCH, "-CGOEnabled", execution.CGOEnabled,
		"-ModuleIdentity", execution.ModuleIdentity, "-WorkloadIdentity", execution.WorkloadIdentity, "-SourceCommit", target.CommitSHA, "-SourceTree", target.TreeSHA,
		"-TimeoutSeconds", timeoutSeconds(timeout)}
	return args, nil
}

func timeoutSeconds(timeout time.Duration) string {
	return strconv.FormatInt(int64((timeout+time.Second-1)/time.Second), 10)
}

func utf16Base64(value string) string {
	encoded := make([]byte, 0, len(value)*2)
	for _, character := range value {
		encoded = append(encoded, byte(character), byte(character>>8))
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func endpointHost(endpoint string) string {
	host, _ := strings.CutPrefix(endpoint, "ssh://")
	return host
}

func quoteRemoteCommand(platform Platform, args []string) (string, error) {
	quoted := make([]string, len(args))
	for index, argument := range args {
		if platform == WindowsPlatform {
			if !windowsArgumentSafe(argument) {
				return "", ErrInvalidTransportRequest
			}
			quoted[index] = argument
			continue
		}
		quoted[index] = "'" + strings.ReplaceAll(argument, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " "), nil
}

func windowsArgumentSafe(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("_./\\:=+-", character)) {
			return false
		}
	}
	return true
}

func validateBatchOperation(operation Operation) error {
	switch operation {
	case OperationRun, OperationWorker:
		return nil
	default:
		return fmt.Errorf("%w: operation", ErrInvalidTransportRequest)
	}
}

func validateRequest(request []byte) error {
	if len(request) == 0 || len(request) > MaxNodeResultBytes {
		return fmt.Errorf("%w: request size", ErrInvalidTransportRequest)
	}
	return nil
}

type boundedWriter struct {
	mu    sync.Mutex
	data  bytes.Buffer
	limit int
}

// Write 在超过 channel 上限时拒绝继续接收远端输出。
func (writer *boundedWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.data.Len()+len(data) > writer.limit {
		return 0, ErrNodeResultInvalid
	}
	return writer.data.Write(data)
}

// Bytes 返回 bounded writer 的独立字节副本。
func (writer *boundedWriter) Bytes() []byte {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return slices.Clone(writer.data.Bytes())
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
