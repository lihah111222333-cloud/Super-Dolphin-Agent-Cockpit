package remotecicli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteruntime"
)

const (
	exactBatchCommandSchema  = "remotecicli-batch-command/v1"
	maxExactBatchCommandArgs = 23
)

var (
	// ErrRuntimeAdapter 表示 adapter 输入、binding 或 strict wire 形状不合法。
	ErrRuntimeAdapter = errors.New("remote CI runtime adapter is invalid")
	// ErrRuntimeAdapterCleanup 表示节点没有提供 cleanup proof，不能接受 batch result。
	ErrRuntimeAdapterCleanup = errors.New("remote CI runtime adapter cleanup is unverified")
)

// RuntimeProbeClient 是 runtime probe adapter 的 provider-neutral 注入端口。
type RuntimeProbeClient interface {
	// Config 返回已验证的单 Ubuntu/Wine runtime 配置。
	Config() remoteruntime.RuntimeConfig
	// Probe 在显式 target/node binding 下执行一次 probe。
	Probe(context.Context, remoteruntime.TargetFacts, string, []byte) (remoteruntime.NodeResult, error)
}

// RuntimeBatchClient 是 runtime batch runner 的 provider-neutral 注入端口。
type RuntimeBatchClient interface {
	// Config 返回已验证的单 Ubuntu/Wine runtime 配置。
	Config() remoteruntime.RuntimeConfig
	// Run 在显式 target、execution facts 和 node binding 下执行一次 batch。
	Run(context.Context, remoteruntime.TargetFacts, remoteruntime.ExecutionFacts, string, []byte) (remoteruntime.NodeResult, error)
}

// RuntimeProbeTransport 将显式 runtime Transport 适配为 heteroci.ProbeTransport。
// endpoint 到 node_id 的映射在构造时冻结；Probe 不接受或推断 ExecutionFacts。
type RuntimeProbeTransport struct {
	runtime    RuntimeProbeClient
	target     remoteruntime.TargetFacts
	endpoints  map[string]string
	identities map[string]string
}

var _ heteroci.ProbeTransport = (*RuntimeProbeTransport)(nil)

// NewRuntimeProbeTransport 校验 topology/runtime 的 endpoint、node、platform 和 probe identity exact 相等。
func NewRuntimeProbeTransport(runtime RuntimeProbeClient, target remoteruntime.TargetFacts, manifest heteroci.Manifest) (*RuntimeProbeTransport, error) {
	if runtime == nil || target.Validate() != nil {
		return nil, fmt.Errorf("%w: probe transport dependencies", ErrRuntimeAdapter)
	}
	if err := manifest.ValidateSingleUbuntuWineExecutionPool(); err != nil {
		return nil, fmt.Errorf("%w: probe topology: %w", ErrRuntimeAdapter, err)
	}
	config := runtime.Config()
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("%w: probe runtime config: %w", ErrRuntimeAdapter, err)
	}
	endpoints, identities, err := exactRuntimeNodeEndpointMap(config, manifest)
	if err != nil {
		return nil, err
	}
	return &RuntimeProbeTransport{runtime: runtime, target: target, endpoints: endpoints, identities: identities}, nil
}

// Probe 仅按 exact endpoint 找到 node_id，再把 opaque request 交给 runtime Transport。
func (transport *RuntimeProbeTransport) Probe(ctx context.Context, endpoint string, request []byte) (heteroci.ProbeResponseBody, error) {
	if transport == nil || transport.runtime == nil || ctx == nil || len(request) == 0 {
		return nil, fmt.Errorf("%w: probe request", ErrRuntimeAdapter)
	}
	nodeID, ok := transport.endpoints[endpoint]
	if !ok {
		return nil, fmt.Errorf("%w: probe endpoint is not bound", ErrRuntimeAdapter)
	}
	result, err := transport.runtime.Probe(ctx, transport.target, nodeID, append([]byte(nil), request...))
	if err != nil {
		return nil, err
	}
	if err := validateRuntimeProbeResult(result, nodeID, transport.identities[nodeID]); err != nil {
		return nil, err
	}
	return newRuntimeProbeBody(result.Bytes()), nil
}

func validateRuntimeProbeResult(result remoteruntime.NodeResult, nodeID, identity string) error {
	if result.NodeID != nodeID || result.Identity != identity || len(result.Raw) == 0 || len(result.Raw) > remoteruntime.MaxNodeResultBytes {
		return fmt.Errorf("%w: probe result identity", ErrRuntimeAdapter)
	}
	return nil
}

func exactRuntimeNodeEndpointMap(config remoteruntime.RuntimeConfig, manifest heteroci.Manifest) (map[string]string, map[string]string, error) {
	bindings := make(map[string]remoteruntime.NodeBinding, len(config.Nodes))
	for _, binding := range config.Nodes {
		if _, exists := bindings[binding.NodeID]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate runtime node", ErrRuntimeAdapter)
		}
		bindings[binding.NodeID] = binding
	}
	endpoints := make(map[string]string, len(manifest.Nodes))
	identities := make(map[string]string, len(manifest.Nodes))
	for _, node := range manifest.Nodes {
		binding, ok := bindings[node.NodeID]
		if !ok || binding.Endpoint != node.Endpoint || binding.Platform != remoteruntime.Platform(node.Platform) || binding.ProbeIdentity != node.ProbeIdentity {
			return nil, nil, fmt.Errorf("%w: runtime/topology node binding drifted", ErrRuntimeAdapter)
		}
		if _, exists := endpoints[node.Endpoint]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate endpoint binding", ErrRuntimeAdapter)
		}
		endpoints[node.Endpoint] = node.NodeID
		identities[node.NodeID] = node.ProbeIdentity
	}
	if len(bindings) != len(endpoints) {
		return nil, nil, fmt.Errorf("%w: stale runtime node binding", ErrRuntimeAdapter)
	}
	return endpoints, identities, nil
}

type runtimeProbeBody struct {
	mutex  sync.Mutex
	data   []byte
	offset int
	closed bool
}

func newRuntimeProbeBody(data []byte) *runtimeProbeBody {
	return &runtimeProbeBody{data: append([]byte(nil), data...)}
}

// Read 在读取前检查 context，并以有界 chunk 返回已缓存 probe response。
func (body *runtimeProbeBody) Read(ctx context.Context, destination []byte) (int, error) {
	if body == nil || ctx == nil {
		return 0, fmt.Errorf("%w: response body", ErrRuntimeAdapter)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return 0, fmt.Errorf("%w: response body is closed", ErrRuntimeAdapter)
	}
	if body.offset == len(body.data) {
		return 0, io.EOF
	}
	read := copy(destination, body.data[body.offset:])
	body.offset += read
	return read, nil
}

// Close 只允许一次，保持 ProbeResponseBody 的严格生命周期。
func (body *runtimeProbeBody) Close() error {
	if body == nil {
		return fmt.Errorf("%w: nil response body", ErrRuntimeAdapter)
	}
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return fmt.Errorf("%w: response body closed twice", ErrRuntimeAdapter)
	}
	body.closed = true
	body.data = nil
	return nil
}

// ReceiptStoreFactory 为每个 exact node 注入独立的 durable batch receipt store；production adapter 不创建 PG。
type ReceiptStoreFactory func(remoteruntime.NodeBinding) (heterociexec.ReceiptStore, error)

// ExactExecutorBinder 为 plan 中每个节点创建一个 CommandBatchExecutor，并拒绝额外或缺失 node lane。
type ExactExecutorBinder struct {
	runtime        RuntimeBatchClient
	target         remoteruntime.TargetFacts
	nodes          map[string]remoteruntime.NodeBinding
	workloads      map[string]remoteportable.PortableManifestWorkload
	moduleIdentity string
	manifestDigest string
	receipts       ReceiptStoreFactory
}

var _ heterociworkflow.ExecutorBinder = (*ExactExecutorBinder)(nil)

// NewExactExecutorBinder 构造 provider-neutral 单 Ubuntu/Wine binder；所有 workload、target、module 和 receipt port 必须显式注入。
func NewExactExecutorBinder(runtime RuntimeBatchClient, portable remoteportable.BoundPortableRun, workloads []remoteportable.PortableManifestWorkload, moduleIdentity string, receipts ReceiptStoreFactory) (*ExactExecutorBinder, error) {
	if err := validateExactBinderDependencies(runtime, moduleIdentity, receipts, workloads); err != nil {
		return nil, err
	}
	target, err := exactBinderTarget(portable)
	if err != nil {
		return nil, err
	}
	nodes, err := exactBinderNodes(runtime)
	if err != nil {
		return nil, err
	}
	workloadMap, err := exactBinderWorkloads(portable.Manifest, workloads)
	if err != nil {
		return nil, err
	}
	return &ExactExecutorBinder{runtime: runtime, target: target, nodes: nodes, workloads: workloadMap, moduleIdentity: moduleIdentity, manifestDigest: portable.ManifestDigest, receipts: receipts}, nil
}

func validateExactBinderDependencies(runtime RuntimeBatchClient, moduleIdentity string, receipts ReceiptStoreFactory, workloads []remoteportable.PortableManifestWorkload) error {
	if runtime == nil || receipts == nil || strings.TrimSpace(moduleIdentity) != moduleIdentity || moduleIdentity == "" || len(workloads) == 0 {
		return fmt.Errorf("%w: binder dependencies", ErrRuntimeAdapter)
	}
	return nil
}

func exactBinderTarget(portable remoteportable.BoundPortableRun) (remoteruntime.TargetFacts, error) {
	if err := portable.Manifest.Validate(); err != nil {
		return remoteruntime.TargetFacts{}, fmt.Errorf("%w: portable manifest: %w", ErrRuntimeAdapter, err)
	}
	if portable.Manifest.Target.RepositoryID != portable.RepositoryID || portable.Manifest.Target.ObjectFormat != portable.ObjectFormat {
		return remoteruntime.TargetFacts{}, fmt.Errorf("%w: portable target binding drifted", ErrRuntimeAdapter)
	}
	target := remoteruntime.TargetFacts{RepositoryID: portable.RepositoryID, ObjectFormat: portable.ObjectFormat, CommitSHA: portable.CommitSHA, TreeSHA: portable.TreeSHA}
	if err := target.Validate(); err != nil {
		return remoteruntime.TargetFacts{}, fmt.Errorf("%w: target binding: %w", ErrRuntimeAdapter, err)
	}
	return target, nil
}

func exactBinderNodes(runtime RuntimeBatchClient) (map[string]remoteruntime.NodeBinding, error) {
	config := runtime.Config()
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("%w: runtime config: %w", ErrRuntimeAdapter, err)
	}
	nodes := make(map[string]remoteruntime.NodeBinding, len(config.Nodes))
	for _, node := range config.Nodes {
		nodes[node.NodeID] = node
	}
	return nodes, nil
}

func exactBinderWorkloads(manifest remoteportable.PortableManifest, expanded []remoteportable.PortableManifestWorkload) (map[string]remoteportable.PortableManifestWorkload, error) {
	if err := remoteportable.ValidatePortableManifestWorkloads(expanded); err != nil {
		return nil, fmt.Errorf("%w: expanded portable workloads: %w", ErrRuntimeAdapter, err)
	}
	workloads := make(map[string]remoteportable.PortableManifestWorkload, len(expanded))
	for _, workload := range expanded {
		workloads[workload.UnitID] = workload
	}
	for _, raw := range manifest.Workloads {
		expandedWorkload, ok := workloads[raw.UnitID]
		if !ok || !reflect.DeepEqual(raw, expandedWorkload) {
			return nil, fmt.Errorf("%w: expanded workload does not cover raw manifest workload", ErrRuntimeAdapter)
		}
	}
	return workloads, nil
}

// Bind 校验 durable plan 后为每个 plan lane 创建 exactly one command executor。
func (binder *ExactExecutorBinder) Bind(plan heterociworkflow.ExecutorBindingPlan) (map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, error) {
	if err := validateExactBinderPlan(binder, plan); err != nil {
		return nil, err
	}
	return binder.bindExactNodes(plan)
}

func validateExactBinderPlan(binder *ExactExecutorBinder, plan heterociworkflow.ExecutorBindingPlan) error {
	if binder == nil || binder.runtime == nil || binder.receipts == nil {
		return fmt.Errorf("%w: binder is nil", ErrRuntimeAdapter)
	}
	if err := heterociworkflow.ValidateDurableExecutorBindingPlan(plan); err != nil {
		return fmt.Errorf("%w: binding plan: %w", ErrRuntimeAdapter, err)
	}
	if plan.ManifestDigest != binder.manifestDigest {
		return fmt.Errorf("%w: binding plan manifest digest drifted", ErrRuntimeAdapter)
	}
	if len(plan.Bindings) != len(binder.nodes) {
		return fmt.Errorf("%w: binding plan node count drifted", ErrRuntimeAdapter)
	}
	return nil
}

func (binder *ExactExecutorBinder) bindExactNodes(plan heterociworkflow.ExecutorBindingPlan) (map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, error) {
	bound := make(map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, len(plan.Bindings))
	for _, binding := range plan.Bindings {
		executor, err := binder.bindExactNode(binding)
		if err != nil {
			return nil, err
		}
		key := heterociworkflow.ExecutorKey{Kind: binding.ExecutorKind, NodeID: binding.NodeID}
		bound[key] = executor
	}
	if len(bound) != len(binder.nodes) {
		return nil, fmt.Errorf("%w: binding plan contains duplicate or missing node", ErrRuntimeAdapter)
	}
	return bound, nil
}

func (binder *ExactExecutorBinder) bindExactNode(binding heterociworkflow.ExecutorBinding) (heterociworkflow.BatchExecutor, error) {
	node, ok := binder.nodes[binding.NodeID]
	if !ok || binding.ExecutorKind != heteroci.ExecutorKindLocalNode {
		return nil, fmt.Errorf("%w: binding node is not exact", ErrRuntimeAdapter)
	}
	store, err := binder.receipts(node)
	if err != nil || store == nil {
		return nil, fmt.Errorf("%w: receipt store for node %q", ErrRuntimeAdapter, node.NodeID)
	}
	allowed := make(map[heterociworkflow.BatchID]struct{}, len(binding.BatchIDs))
	for _, batchID := range binding.BatchIDs {
		allowed[batchID] = struct{}{}
	}
	factory := binder.commandFactory(node, allowed)
	runner := &exactBatchRunner{runtime: binder.runtime, target: binder.target, node: node, moduleIdentity: binder.moduleIdentity}
	executor, err := heterociexec.NewCommandBatchExecutor(factory, runner, store)
	if err != nil {
		return nil, fmt.Errorf("%w: create executor for node %q: %w", ErrRuntimeAdapter, node.NodeID, err)
	}
	return executor, nil
}

func (binder *ExactExecutorBinder) commandFactory(node remoteruntime.NodeBinding, allowed map[heterociworkflow.BatchID]struct{}) heterociexec.CommandFactory {
	return func(batch heterociworkflow.ExecutionBatch) (heterociexec.Command, error) {
		if batch.Key.NodeID != node.NodeID || batch.Key.Kind != heteroci.ExecutorKindLocalNode {
			return heterociexec.Command{}, fmt.Errorf("%w: batch executor key drifted", ErrRuntimeAdapter)
		}
		if _, ok := allowed[batch.BatchID]; !ok {
			return heterociexec.Command{}, fmt.Errorf("%w: batch is not in node binding", ErrRuntimeAdapter)
		}
		request, requestBytes, workloadDigest, err := buildExactBatchRequest(node, binder.workloads, batch)
		if err != nil {
			return heterociexec.Command{}, err
		}
		return encodeExactBatchCommand(node, binder.target, binder.moduleIdentity, batch, request, requestBytes, workloadDigest)
	}
}

func buildExactBatchRequest(node remoteruntime.NodeBinding, workloads map[string]remoteportable.PortableManifestWorkload, batch heterociworkflow.ExecutionBatch) (heterocinode.BatchRequest, []byte, string, error) {
	if len(batch.Units) == 0 {
		return heterocinode.BatchRequest{}, nil, "", fmt.Errorf("%w: batch workloads are empty", ErrRuntimeAdapter)
	}
	requestWorkloads := make([]heterocinode.BatchWorkload, 0, len(batch.Units))
	seen := make(map[string]struct{}, len(batch.Units))
	targetPlatform, runtimeCapability := "", ""
	for _, unit := range batch.Units {
		if _, duplicate := seen[unit.UnitID]; duplicate {
			return heterocinode.BatchRequest{}, nil, "", fmt.Errorf("%w: batch workload is duplicated", ErrRuntimeAdapter)
		}
		portable, ok := workloads[unit.UnitID]
		if !ok || portable.TargetPlatform != string(unit.TargetPlatform) || portable.RuntimeCapability != string(unit.RequiredRuntime) || portable.CPUMilli != unit.CPUMilli || portable.MemoryBytes != unit.MemoryBytes {
			return heterocinode.BatchRequest{}, nil, "", fmt.Errorf("%w: batch workload coverage drifted", ErrRuntimeAdapter)
		}
		if targetPlatform == "" {
			targetPlatform, runtimeCapability = portable.TargetPlatform, portable.RuntimeCapability
		} else if portable.TargetPlatform != targetPlatform || portable.RuntimeCapability != runtimeCapability {
			return heterocinode.BatchRequest{}, nil, "", fmt.Errorf("%w: batch workloads must use one target/runtime pair", ErrRuntimeAdapter)
		}
		requestWorkloads = append(requestWorkloads, heterocinode.BatchWorkload{
			UnitID: unit.UnitID, Kind: portable.Kind, ModuleRoot: portable.ModuleRoot, Package: portable.Package, Name: portable.Name,
			TargetPlatform: portable.TargetPlatform, RuntimeCapability: portable.RuntimeCapability,
			CPUMilli: unit.CPUMilli, MemoryBytes: unit.MemoryBytes,
			ExecutionPolicy: heterocinode.BatchExecutionPolicy(portable.ExecutionPolicy), CommandDigest: portable.CommandDigest, ExecutionDigest: portable.ExecutionDigest,
		})
		seen[unit.UnitID] = struct{}{}
	}
	if _, err := node.ExecutionBaseFacts(remoteruntime.Platform(targetPlatform), runtimeCapability); err != nil {
		return heterocinode.BatchRequest{}, nil, "", fmt.Errorf("%w: batch target/runtime: %w", ErrRuntimeAdapter, err)
	}
	sort.Slice(requestWorkloads, func(left, right int) bool { return requestWorkloads[left].UnitID < requestWorkloads[right].UnitID })
	request := heterocinode.BatchRequest{SchemaVersion: heterocinode.BatchRequestSchemaVersion, GoExecutable: node.GoExecutable, Workloads: requestWorkloads}
	if runtimeCapability == remoteruntime.RuntimeUbuntuWineContainer {
		if node.Wine == nil {
			return heterocinode.BatchRequest{}, nil, "", fmt.Errorf("%w: Wine binding is missing", ErrRuntimeAdapter)
		}
		request.WineExecutable, request.WineExecutableIdentity = node.Wine.WineExecutable, node.Wine.WineExecutableIdentity
		request.WineServerExecutable, request.WineServerExecutableIdentity = node.Wine.WineServerExecutable, node.Wine.WineServerExecutableIdentity
		request.WinePrefixRoot = node.Wine.WinePrefixRoot
	}
	requestBytes, err := heterocinode.EncodeBatchRequest(request)
	if err != nil {
		return heterocinode.BatchRequest{}, nil, "", fmt.Errorf("%w: encode exact batch request: %w", ErrRuntimeAdapter, err)
	}
	workloadDigest, err := deriveExactBatchWorkloadDigest(request.Workloads)
	if err != nil {
		return heterocinode.BatchRequest{}, nil, "", err
	}
	return request, requestBytes, workloadDigest, nil
}

type exactBatchWorkloadDigestMaterial struct {
	Workloads []heterocinode.BatchWorkload `json:"workloads"`
}

func deriveExactBatchWorkloadDigest(workloads []heterocinode.BatchWorkload) (string, error) {
	encoded, err := json.Marshal(exactBatchWorkloadDigestMaterial{Workloads: workloads})
	if err != nil {
		return "", fmt.Errorf("%w: derive workload digest material", ErrRuntimeAdapter)
	}
	sum := sha256.Sum256(append([]byte("remote-ci-batch-workload-set/v1\x00"), encoded...))
	return "sha256:" + fmt.Sprintf("%x", sum[:]), nil
}

func encodeExactBatchCommand(node remoteruntime.NodeBinding, target remoteruntime.TargetFacts, moduleIdentity string, batch heterociworkflow.ExecutionBatch, request heterocinode.BatchRequest, requestBytes []byte, workloadDigest string) (heterociexec.Command, error) {
	if len(request.Workloads) == 0 || len(requestBytes) == 0 || workloadDigest == "" {
		return heterociexec.Command{}, fmt.Errorf("%w: exact batch command material is incomplete", ErrRuntimeAdapter)
	}
	argv := []string{
		node.WorkerExecutable, "--adapter-schema", exactBatchCommandSchema, "--node-id", node.NodeID,
		"--batch-id", string(batch.BatchID), "--run-id", batch.RunID, "--repository-id", target.RepositoryID,
		"--object-format", target.ObjectFormat, "--commit-sha", target.CommitSHA, "--tree-sha", target.TreeSHA,
		"--module-identity", moduleIdentity, "--workload-digest", workloadDigest,
		"--request-base64", base64.StdEncoding.EncodeToString(requestBytes),
	}
	return heterociexec.EncodeCommand(heteroci.Platform(batch.Units[0].TargetPlatform), argv)
}

type exactBatchCommand struct {
	nodeID, batchID, runID         string
	target                         remoteruntime.TargetFacts
	moduleIdentity, workloadDigest string
	request                        []byte
}

func decodeExactBatchCommand(command heterociexec.Command, node remoteruntime.NodeBinding, target remoteruntime.TargetFacts) (exactBatchCommand, error) {
	if err := validateExactBatchCommandCanonical(command); err != nil {
		return exactBatchCommand{}, err
	}
	values, err := exactBatchCommandValues(command, node)
	if err != nil {
		return exactBatchCommand{}, err
	}
	if err := validateExactBatchCommandBinding(values, node, target); err != nil {
		return exactBatchCommand{}, err
	}
	request, err := decodeExactBatchRequestToken(values[22])
	if err != nil {
		return exactBatchCommand{}, err
	}
	return exactBatchCommand{nodeID: values[4], batchID: values[6], runID: values[8], target: target, moduleIdentity: values[18], workloadDigest: values[20], request: request}, nil
}

func validateExactBatchCommandCanonical(command heterociexec.Command) error {
	canonical, err := heterociexec.EncodeCommand(command.Platform, command.Argv)
	if err != nil || !slices.Equal(canonical.Argv, command.Argv) {
		return fmt.Errorf("%w: command is not canonical", ErrRuntimeAdapter)
	}
	return nil
}

func exactBatchCommandValues(command heterociexec.Command, node remoteruntime.NodeBinding) ([]string, error) {
	if len(command.Argv) != maxExactBatchCommandArgs || command.Argv[0] != node.WorkerExecutable || !validExactBatchCommandFlags(command.Argv) {
		return nil, fmt.Errorf("%w: command shape is invalid", ErrRuntimeAdapter)
	}
	return command.Argv, nil
}

func validateExactBatchCommandBinding(values []string, node remoteruntime.NodeBinding, target remoteruntime.TargetFacts) error {
	if values[4] != node.NodeID || values[10] != target.RepositoryID || values[12] != target.ObjectFormat || values[14] != target.CommitSHA || values[16] != target.TreeSHA {
		return fmt.Errorf("%w: command binding drifted", ErrRuntimeAdapter)
	}
	return nil
}

func decodeExactBatchRequestToken(token string) ([]byte, error) {
	request, err := base64.StdEncoding.DecodeString(token)
	if err != nil || len(request) == 0 || len(request) > 1<<20 || base64.StdEncoding.EncodeToString(request) != token {
		return nil, fmt.Errorf("%w: command request encoding is invalid", ErrRuntimeAdapter)
	}
	return request, nil
}

func validExactBatchCommandFlags(argv []string) bool {
	want := map[int]string{1: "--adapter-schema", 3: "--node-id", 5: "--batch-id", 7: "--run-id", 9: "--repository-id", 11: "--object-format", 13: "--commit-sha", 15: "--tree-sha", 17: "--module-identity", 19: "--workload-digest", 21: "--request-base64"}
	for index, value := range want {
		if argv[index] != value {
			return false
		}
	}
	return argv[2] == exactBatchCommandSchema
}

type exactBatchRunner struct {
	runtime        RuntimeBatchClient
	target         remoteruntime.TargetFacts
	node           remoteruntime.NodeBinding
	moduleIdentity string
}

// Run 严格解码 adapter command/request，仅执行一次并要求 cleanup proof。
func (runner *exactBatchRunner) Run(ctx context.Context, command heterociexec.Command) (heterociexec.RunOutcome, error) {
	decoded, request, err := decodeExactBatchRunnerInput(command, runner.node, runner.target)
	if err != nil {
		return heterociexec.RunOutcome{}, err
	}
	facts, err := runner.executionFacts(decoded, request)
	if err != nil {
		return heterociexec.RunOutcome{}, err
	}
	result, err := runner.runtime.Run(ctx, runner.target, facts, runner.node.NodeID, decoded.request)
	if err != nil {
		return heterociexec.RunOutcome{}, err
	}
	return runner.decodeRunOutcome(request, result)
}

func decodeExactBatchRunnerInput(command heterociexec.Command, node remoteruntime.NodeBinding, target remoteruntime.TargetFacts) (exactBatchCommand, heterocinode.BatchRequest, error) {
	decoded, err := decodeExactBatchCommand(command, node, target)
	if err != nil {
		return exactBatchCommand{}, heterocinode.BatchRequest{}, err
	}
	request, err := heterocinode.DecodeBatchRequest(decoded.request)
	if err != nil {
		return exactBatchCommand{}, heterocinode.BatchRequest{}, fmt.Errorf("%w: strict batch request: %w", ErrRuntimeAdapter, err)
	}
	workloadDigest, err := deriveExactBatchWorkloadDigest(request.Workloads)
	if err != nil || workloadDigest != decoded.workloadDigest || decoded.moduleIdentity == "" {
		return exactBatchCommand{}, heterocinode.BatchRequest{}, fmt.Errorf("%w: batch workload or module identity drifted", ErrRuntimeAdapter)
	}
	return decoded, request, nil
}

func (runner *exactBatchRunner) executionFacts(decoded exactBatchCommand, request heterocinode.BatchRequest) (remoteruntime.ExecutionFacts, error) {
	if err := request.Validate(); err != nil {
		return remoteruntime.ExecutionFacts{}, fmt.Errorf("%w: batch request: %w", ErrRuntimeAdapter, err)
	}
	if len(request.Workloads) == 0 {
		return remoteruntime.ExecutionFacts{}, fmt.Errorf("%w: batch request workloads are empty", ErrRuntimeAdapter)
	}
	workloadDigest, err := deriveExactBatchWorkloadDigest(request.Workloads)
	if err != nil || decoded.workloadDigest == "" || workloadDigest != decoded.workloadDigest {
		return remoteruntime.ExecutionFacts{}, fmt.Errorf("%w: batch workload digest drifted", ErrRuntimeAdapter)
	}
	cpuMilli, memoryBytes, err := aggregateBatchResourceBudget(request.Workloads)
	if err != nil {
		return remoteruntime.ExecutionFacts{}, err
	}
	first := request.Workloads[0]
	facts, err := runner.node.ExecutionBaseFacts(remoteruntime.Platform(first.TargetPlatform), first.RuntimeCapability)
	if err != nil {
		return remoteruntime.ExecutionFacts{}, fmt.Errorf("%w: execution base facts: %w", ErrRuntimeAdapter, err)
	}
	facts.ModuleIdentity = decoded.moduleIdentity
	facts.WorkloadIdentity = decoded.workloadDigest
	facts.CPUMilli = cpuMilli
	facts.MemoryBytes = memoryBytes
	if err := facts.ValidateForNode(runner.node); err != nil {
		return remoteruntime.ExecutionFacts{}, fmt.Errorf("%w: execution facts: %w", ErrRuntimeAdapter, err)
	}
	if decoded.moduleIdentity != runner.moduleIdentity {
		return remoteruntime.ExecutionFacts{}, fmt.Errorf("%w: module identity drifted", ErrRuntimeAdapter)
	}
	return facts, nil
}

func aggregateBatchResourceBudget(workloads []heterocinode.BatchWorkload) (int64, int64, error) {
	if len(workloads) == 0 {
		return 0, 0, fmt.Errorf("%w: batch workloads are empty", ErrRuntimeAdapter)
	}
	maxInt64 := int64(^uint64(0) >> 1)
	var cpuMilli, memoryBytes int64
	for _, workload := range workloads {
		if workload.CPUMilli <= 0 || workload.MemoryBytes <= 0 {
			return 0, 0, fmt.Errorf("%w: batch workload resource budget is invalid", ErrRuntimeAdapter)
		}
		if workload.CPUMilli > maxInt64-cpuMilli || workload.MemoryBytes > maxInt64-memoryBytes {
			return 0, 0, fmt.Errorf("%w: batch resource budget overflows", ErrRuntimeAdapter)
		}
		cpuMilli += workload.CPUMilli
		memoryBytes += workload.MemoryBytes
	}
	return cpuMilli, memoryBytes, nil
}

func (runner *exactBatchRunner) decodeRunOutcome(request heterocinode.BatchRequest, result remoteruntime.NodeResult) (heterociexec.RunOutcome, error) {
	if result.NodeID != runner.node.NodeID || result.Identity != runner.node.RunnerIdentity {
		return heterociexec.RunOutcome{}, fmt.Errorf("%w: node runner identity drifted", ErrRuntimeAdapter)
	}
	if !result.CleanupVerified {
		return heterociexec.RunOutcome{}, ErrRuntimeAdapterCleanup
	}
	batchResult, err := heterocinode.DecodeBatchResult(result.Bytes())
	if err != nil {
		return heterociexec.RunOutcome{}, fmt.Errorf("%w: strict batch result: %w", ErrRuntimeAdapter, err)
	}
	if err := validateExactBatchResultCoverage(request, batchResult); err != nil {
		return heterociexec.RunOutcome{}, err
	}
	return exactRunOutcome(batchResult), nil
}

func validateExactBatchResultCoverage(request heterocinode.BatchRequest, result heterocinode.BatchResult) error {
	if len(request.Workloads) != len(result.Outcomes) {
		return fmt.Errorf("%w: batch result workload coverage drifted", ErrRuntimeAdapter)
	}
	for index, workload := range request.Workloads {
		if workload.UnitID != result.Outcomes[index].UnitID {
			return fmt.Errorf("%w: batch result unit coverage drifted", ErrRuntimeAdapter)
		}
	}
	return nil
}

func exactRunOutcome(result heterocinode.BatchResult) heterociexec.RunOutcome {
	outcomes := make([]heterociworkflow.BatchOutcome, len(result.Outcomes))
	exitCode := 0
	for index, outcome := range result.Outcomes {
		state := heteroci.LeaseStateSucceeded
		if outcome.Status != heterocinode.BatchOutcomeSuccess {
			state = heteroci.LeaseStateFailed
			if outcome.ExitCode > 0 && exitCode == 0 {
				exitCode = outcome.ExitCode
			}
		}
		outcomes[index] = heterociworkflow.BatchOutcome{UnitID: outcome.UnitID, State: state}
	}
	if result.Outcome == heterocinode.BatchOutcomeFailed && exitCode == 0 {
		exitCode = 1
	}
	return heterociexec.RunOutcome{ExitCode: exitCode, Outcomes: outcomes}
}
