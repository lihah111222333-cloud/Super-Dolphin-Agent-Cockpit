package remoteruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

// Command 构造一个 operation 的完整 SSH argv，但不启动远端进程。
func (transport *Transport) Command(operation Operation, target TargetFacts, nodeID string, request []byte) (CommandSpec, error) {
	if transport == nil || transport.runner == nil {
		return CommandSpec{}, ErrInvalidTransportDependencies
	}
	node, ok := transport.nodes[nodeID]
	if !ok {
		return CommandSpec{}, fmt.Errorf("%w: node identity", ErrInvalidTransportRequest)
	}
	if err := validateOperation(operation); err != nil {
		return CommandSpec{}, err
	}
	if err := validateRequest(request); err != nil {
		return CommandSpec{}, err
	}
	if err := target.Validate(); err != nil {
		return CommandSpec{}, err
	}
	args, err := transport.sshArgs(operation, node, target, request)
	if err != nil {
		return CommandSpec{}, err
	}
	return CommandSpec{Executable: transport.config.SSHExecutable, Args: args}, nil
}

// Probe 在 probe timeout 内执行一个 node probe，并只接受唯一 canonical result。
func (transport *Transport) Probe(ctx context.Context, target TargetFacts, nodeID string, request []byte) (NodeResult, error) {
	return transport.execute(ctx, OperationProbe, target, nodeID, request)
}

// Run 在 run timeout 内执行 runner，并只接受唯一 canonical result。
func (transport *Transport) Run(ctx context.Context, target TargetFacts, nodeID string, request []byte) (NodeResult, error) {
	return transport.execute(ctx, OperationRun, target, nodeID, request)
}

// Worker 在 worker timeout 内执行一个显式 worker 请求。
func (transport *Transport) Worker(ctx context.Context, target TargetFacts, nodeID string, request []byte) (NodeResult, error) {
	return transport.execute(ctx, OperationWorker, target, nodeID, request)
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
func (transport *Transport) RunBytes(ctx context.Context, target TargetFacts, nodeID string, request []byte) ([]byte, error) {
	result, err := transport.Run(ctx, target, nodeID, request)
	if err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

// DecodeNodeResult 严格解析一个 node result object，并校验 node_id 与 optional identity。
func DecodeNodeResult(data []byte, node NodeBinding, operation Operation) (NodeResult, error) {
	if err := validateOperation(operation); err != nil {
		return NodeResult{}, err
	}
	canonical, err := canonicalNodeObject(data)
	if err != nil {
		return NodeResult{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return NodeResult{}, errors.Join(ErrNodeResultInvalid, err)
	}
	nodeID, err := requiredStringField(fields, "node_id")
	if err != nil || nodeID != node.NodeID {
		return NodeResult{}, fmt.Errorf("%w: node_id", ErrNodeResultInvalid)
	}
	if _, err := requiredStringField(fields, "status"); err != nil {
		return NodeResult{}, fmt.Errorf("%w: status", ErrNodeResultInvalid)
	}
	identityKey, identity, err := resultIdentity(fields, operation)
	if err != nil {
		return NodeResult{}, err
	}
	if !resultIdentityMatches(identityKey, identity, node, operation) {
		return NodeResult{}, fmt.Errorf("%w: identity", ErrNodeResultInvalid)
	}
	return NodeResult{NodeID: nodeID, Identity: identity, Raw: canonical}, nil
}

func (transport *Transport) execute(ctx context.Context, operation Operation, target TargetFacts, nodeID string, request []byte) (NodeResult, error) {
	node, err := transport.prepareExecution(ctx, operation, target, nodeID, request)
	if err != nil {
		return NodeResult{}, err
	}
	args, err := transport.sshArgs(operation, node, target, request)
	if err != nil {
		return NodeResult{}, err
	}
	return transport.runCommand(ctx, operation, node, args)
}

func (transport *Transport) prepareExecution(ctx context.Context, operation Operation, target TargetFacts, nodeID string, request []byte) (NodeBinding, error) {
	if transport == nil || transport.runner == nil || ctx == nil {
		return NodeBinding{}, ErrInvalidTransportDependencies
	}
	node, ok := transport.nodes[nodeID]
	if !ok {
		return NodeBinding{}, fmt.Errorf("%w: node identity", ErrInvalidTransportRequest)
	}
	if err := validateOperation(operation); err != nil {
		return NodeBinding{}, err
	}
	if err := target.Validate(); err != nil {
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
	return DecodeNodeResult(stdout.Bytes(), node, operation)
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

func (transport *Transport) sshArgs(operation Operation, node NodeBinding, target TargetFacts, request []byte) ([]string, error) {
	token := base64.StdEncoding.EncodeToString(request)
	workerArgs := []string{node.WorkerExecutable, "batch", "--request-base64", token}
	remoteArgs, err := operationRemoteArgs(operation, node, target, workerArgs, token, transport.operationTimeout(operation))
	if err != nil {
		return nil, err
	}
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

func operationRemoteArgs(operation Operation, node NodeBinding, target TargetFacts, workerArgs []string, token string, timeout time.Duration) ([]string, error) {
	switch operation {
	case OperationProbe:
		return []string{node.ProbeExecutable, "probe", "--config", node.ProbeConfigPath, "--probe-request-base64", token}, nil
	case OperationRun:
		if node.Platform == UbuntuPlatform {
			return linuxRunnerArgs(node, target, workerArgs, timeout)
		}
		return windowsRunnerArgs(node, target, workerArgs, timeout)
	case OperationWorker:
		return workerArgs, nil
	default:
		return nil, ErrInvalidTransportRequest
	}
}

func linuxRunnerArgs(node NodeBinding, target TargetFacts, workerArgs []string, timeout time.Duration) ([]string, error) {
	if node.Linux == nil {
		return nil, ErrInvalidTransportRequest
	}
	args := []string{node.RunnerExecutable, "--repo", node.RepositoryRoot, "--mode", "docker", "--runtime", "ubuntu", "--docker-image", node.Linux.DockerImage,
		"--worker-binary", node.WorkerExecutable, "--worker-identity", node.WorkerIdentity, "--shared-root", node.ObjectStoreRoot, "--cache-root", node.CachePublicationRoot,
		"--repository-id", target.RepositoryID, "--node-id", node.NodeID, "--object-format", target.ObjectFormat, "--source-commit", target.CommitSHA, "--source-tree", target.TreeSHA,
		"--toolchain-identity", node.ToolchainIdentity, "--goos", target.GOOS, "--goarch", target.GOARCH, "--cgo-enabled", target.CGOEnabled,
		"--module-identity", target.ModuleIdentity, "--workload-identity", target.WorkloadIdentity, "--timeout-seconds", timeoutSeconds(timeout), "--"}
	return append(args, workerArgs...), nil
}

func windowsRunnerArgs(node NodeBinding, target TargetFacts, workerArgs []string, timeout time.Duration) ([]string, error) {
	command := strings.Join(workerArgs, " ")
	args := []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", node.RunnerExecutable,
		"-RepoPath", node.RepositoryRoot, "-CommandBase64", utf16Base64(command), "-NodeId", node.NodeID, "-RepositoryId", target.RepositoryID,
		"-RepositoryRoot", node.RepositoryRoot, "-ObjectStoreRoot", node.ObjectStoreRoot, "-WorktreeRoot", node.WorktreeRoot, "-CacheRoot", node.CachePublicationRoot,
		"-ToolchainIdentity", node.ToolchainIdentity, "-GOOS", target.GOOS, "-GOARCH", target.GOARCH, "-CGOEnabled", target.CGOEnabled,
		"-ModuleIdentity", target.ModuleIdentity, "-WorkloadIdentity", target.WorkloadIdentity, "-SourceCommit", target.CommitSHA, "-SourceTree", target.TreeSHA,
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

func validateOperation(operation Operation) error {
	switch operation {
	case OperationProbe, OperationRun, OperationWorker:
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

func requiredStringField(fields map[string]json.RawMessage, name string) (string, error) {
	raw, ok := fields[name]
	if !ok {
		return "", ErrNodeResultInvalid
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" || strings.TrimSpace(value) != value {
		return "", ErrNodeResultInvalid
	}
	return value, nil
}

func resultIdentity(fields map[string]json.RawMessage, operation Operation) (string, string, error) {
	keys := []string{"node_identity", "identity"}
	switch operation {
	case OperationProbe:
		keys = append(keys, "probe_identity")
	case OperationRun:
		keys = append(keys, "runner_identity", "worker_identity")
	case OperationWorker:
		keys = append(keys, "worker_identity")
	}
	for _, key := range keys {
		if raw, ok := fields[key]; ok {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil || value == "" {
				return "", "", fmt.Errorf("%w: identity", ErrNodeResultInvalid)
			}
			return key, value, nil
		}
	}
	return "", "", fmt.Errorf("%w: identity", ErrNodeResultInvalid)
}

func resultIdentityMatches(key, value string, node NodeBinding, operation Operation) bool {
	switch key {
	case "probe_identity":
		return operation == OperationProbe && value == node.ProbeIdentity
	case "runner_identity":
		return operation == OperationRun && value == node.RunnerIdentity
	case "worker_identity":
		return (operation == OperationRun || operation == OperationWorker) && value == node.WorkerIdentity
	case "node_identity", "identity":
		return genericIdentityMatches(value, node, operation)
	default:
		return false
	}
}

func genericIdentityMatches(value string, node NodeBinding, operation Operation) bool {
	switch operation {
	case OperationProbe:
		return value == node.ProbeIdentity
	case OperationRun:
		return value == node.RunnerIdentity || value == node.WorkerIdentity
	case OperationWorker:
		return value == node.WorkerIdentity
	default:
		return false
	}
}

func canonicalNodeObject(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxNodeResultBytes {
		return nil, ErrNodeResultInvalid
	}
	if err := walkStrictJSON(data); err != nil {
		return nil, errors.Join(ErrNodeResultInvalid, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.Join(ErrNodeResultInvalid, err)
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, ErrNodeResultInvalid
	}
	canonical, err := json.Marshal(object)
	if err != nil || !bytes.Equal(canonical, data) {
		return nil, ErrNodeResultInvalid
	}
	return slices.Clone(data), nil
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
