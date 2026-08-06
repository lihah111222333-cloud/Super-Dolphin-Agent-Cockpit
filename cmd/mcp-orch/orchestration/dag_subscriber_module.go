package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/documentartifact"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/sharedfileowner"
	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

// DAGSubscriberMetrics 是 DAG turn.completed subscriber 的计数器快照。
type DAGSubscriberMetrics struct {
	CompleteDone, CompleteFailed, IdempotentSkipped, LookupNoNode, LookupDirtyData, LookupFailed int64
	CompleteSizeCapExceeded, CompleteResultEmpty                                                 int64
}

// dagSubscriberCounter 保存 subscriber 运行期原子计数。
type dagSubscriberCounter struct {
	completeDone, completeFailed, idempotentSkipped atomic.Int64
	lookupNoNode, lookupDirtyData, lookupFailed     atomic.Int64
	completeSizeCapExceeded, completeResultEmpty    atomic.Int64
}

// newDAGSubscriberCounter 为一个 subscriber owner 创建独立的运行期计数器。
func newDAGSubscriberCounter() *dagSubscriberCounter { return &dagSubscriberCounter{} }

// DAGSubscriberCounters 返回指定 subscriber owner 的当前计数器快照。
func DAGSubscriberCounters(counters ...*dagSubscriberCounter) DAGSubscriberMetrics {
	if len(counters) == 0 {
		return DAGSubscriberMetrics{}
	}
	return counters[0].Snapshot()
}

// IncCompleteDone 累加成功完成节点的事件数。
func (c *dagSubscriberCounter) IncCompleteDone() {
	if c != nil {
		c.completeDone.Add(1)
	}
}

// IncCompleteFailed 累加完成事件处理失败次数。
func (c *dagSubscriberCounter) IncCompleteFailed() {
	if c != nil {
		c.completeFailed.Add(1)
	}
}

// IncIdempotentSkipped 累加幂等跳过的终态事件数。
func (c *dagSubscriberCounter) IncIdempotentSkipped() {
	if c != nil {
		c.idempotentSkipped.Add(1)
	}
}

// IncLookupNoNode 累加没有找到对应 runtime node 的事件数。
func (c *dagSubscriberCounter) IncLookupNoNode() {
	if c != nil {
		c.lookupNoNode.Add(1)
	}
}

// IncLookupDirtyData 累加查询到不一致或脏数据的事件数。
func (c *dagSubscriberCounter) IncLookupDirtyData() {
	if c != nil {
		c.lookupDirtyData.Add(1)
	}
}

// IncLookupFailed 累加节点查询失败次数。
func (c *dagSubscriberCounter) IncLookupFailed() {
	if c != nil {
		c.lookupFailed.Add(1)
	}
}

// IncCompleteSizeCapExceeded 累加节点结果超过大小上限的次数。
func (c *dagSubscriberCounter) IncCompleteSizeCapExceeded() {
	if c != nil {
		c.completeSizeCapExceeded.Add(1)
	}
}

// IncCompleteResultEmpty 累加 agent 输出为空但要求写 node.result 的次数。
func (c *dagSubscriberCounter) IncCompleteResultEmpty() {
	if c != nil {
		c.completeResultEmpty.Add(1)
	}
}

// Snapshot 读取所有原子计数并返回一致性足够的监控快照。
func (c *dagSubscriberCounter) Snapshot() DAGSubscriberMetrics {
	if c == nil {
		return DAGSubscriberMetrics{}
	}
	return DAGSubscriberMetrics{
		CompleteDone: c.completeDone.Load(), CompleteFailed: c.completeFailed.Load(), IdempotentSkipped: c.idempotentSkipped.Load(),
		LookupNoNode: c.lookupNoNode.Load(), LookupDirtyData: c.lookupDirtyData.Load(), LookupFailed: c.lookupFailed.Load(),
		CompleteSizeCapExceeded: c.completeSizeCapExceeded.Load(), CompleteResultEmpty: c.completeResultEmpty.Load(),
	}
}

// turnOutputMaterializationFailure 描述 agent 输出物化失败的分类和大小上限信号。
type turnOutputMaterializationFailure struct {
	Reason          string
	SizeCapExceeded bool
}

// turnOutputMaterialization 保存 turn 输出要写回 node.result、sharedfile 或 artifact 的材料。
type turnOutputMaterialization struct {
	Result         json.RawMessage
	SharedfilePath string
	RawResult      string
	Artifact       *artifactMaterialization
}

// artifactUpdatedBy 是 DAG subscriber 写 artifact 时使用的审计身份。
const artifactUpdatedBy = "dag-artifact"

// artifactMaterialization 保存 artifact import 所需参数。
type artifactMaterialization struct {
	Params sharedfilestore.ImportLocalFileParams
}

// classifyMaterializationFailure 把物化失败原因映射到节点失败分类。
func classifyMaterializationFailure(failure *turnOutputMaterializationFailure) nodeexec.FailureClass {
	if failure == nil {
		return nodeexec.FailureClassValidation
	}
	if strings.HasPrefix(failure.Reason, "infrastructure:") {
		return nodeexec.FailureClassInfrastructure
	}
	return nodeexec.FailureClassValidation
}

// encodeTurnResultForNodeUpdate 把原始 turn 输出编码成 node.result 可接受的 JSON。
func encodeTurnResultForNodeUpdate(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	wrapped, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: raw})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(wrapped)
}

// prepareTurnCompletedResult 根据 agent node outputs 配置决定输出落点。
// 非 agent 节点直接写 node.result；agent 节点可转写 sharedfile/artifact 或受 4KB 上限约束。
func prepareTurnCompletedResult(node *taskdag.Node, rawResult string) (turnOutputMaterialization, *turnOutputMaterializationFailure) {
	if node == nil || strings.TrimSpace(node.NodeType) != "agent" {
		return turnOutputMaterialization{Result: encodeTurnResultForNodeUpdate(rawResult)}, nil
	}
	cfg, failure := parseAgentOutputConfig(node.Config)
	if failure != nil {
		return turnOutputMaterialization{}, failure
	}
	if cfg.Outputs.ToArtifact != nil {
		return prepareArtifactTurnCompletedResult(node, cfg.Outputs.ToArtifact, rawResult)
	}
	trimmed := strings.TrimSpace(rawResult)
	path := configuredSharedfilePath(cfg.Outputs)
	emitNodeResult := shouldMaterializeAgentNodeResult(cfg.Outputs)
	if trimmed == "" {
		if path != "" {
			return turnOutputMaterialization{Result: finalAgentMaterializedResult(rawResult, nil, path, emitNodeResult), SharedfilePath: path, RawResult: rawResult}, nil
		}
		if emitNodeResult {
			return turnOutputMaterialization{}, validationMaterializationFailure("empty agent output")
		}
	}
	nodeResult, failure := buildAgentNodeResult(rawResult, emitNodeResult)
	if failure != nil {
		return turnOutputMaterialization{}, failure
	}
	return turnOutputMaterialization{Result: finalAgentMaterializedResult(rawResult, nodeResult, path, emitNodeResult), SharedfilePath: path, RawResult: rawResult}, nil
}

// parseAgentOutputConfig 解码 agent 节点输出配置。
// 配置缺失或非法会物化为 validation 失败，避免 subscriber 把解析错误误归类为基础设施异常。
func parseAgentOutputConfig(raw json.RawMessage) (*nodeexec.AgentNodeConfig, *turnOutputMaterializationFailure) {
	cfg, err := nodeexec.ParseAgentConfig(raw)
	if err != nil {
		return nil, validationMaterializationFailure("decode agent config: " + err.Error())
	}
	if cfg == nil {
		return nil, validationMaterializationFailure("decode agent config: nil parsed config")
	}
	return cfg, nil
}

// agentNodeUsesArtifactResult 判断 agent 节点是否配置 outputs.to_artifact。
func agentNodeUsesArtifactResult(rawConfig json.RawMessage) bool {
	cfg, err := nodeexec.ParseAgentConfig(rawConfig)
	return err == nil && cfg != nil && cfg.Outputs.ToArtifact != nil
}

// buildAgentNodeResult 在需要写 node.result 时执行大小上限检查。
func buildAgentNodeResult(rawResult string, emit bool) (json.RawMessage, *turnOutputMaterializationFailure) {
	if !emit {
		return nil, nil
	}
	nodeResult := encodeTurnResultForNodeUpdate(rawResult)
	if len(nodeResult) <= completeNodeResultCap {
		return nodeResult, nil
	}
	return nil, &turnOutputMaterializationFailure{Reason: fmt.Sprintf("result exceeds 4KB size cap (%d > %d bytes), configure outputs.to_sharedfile (ADR-006)", len(nodeResult), completeNodeResultCap), SizeCapExceeded: true}
}

// finalAgentMaterializedResult 选择最终写回 node.result 的内容或 sharedfile 引用。
func finalAgentMaterializedResult(rawResult string, nodeResult json.RawMessage, path string, emit bool) json.RawMessage {
	switch {
	case emit:
		return nodeResult
	case path != "":
		return encodeSharedfileResultRef(path)
	default:
		return encodeTurnResultForNodeUpdate(rawResult)
	}
}

// shouldMaterializeAgentNodeResult 判断是否需要把 agent 输出写入 node.result。
func shouldMaterializeAgentNodeResult(out nodeexec.OutputsConfig) bool {
	return out.ToNodeResult || configuredSharedfilePath(out) == ""
}

// prepareArtifactTurnCompletedResult 构造 artifact import 参数，并把 node.result 写成 artifact 引用。
func prepareArtifactTurnCompletedResult(node *taskdag.Node, target *nodeexec.ArtifactTarget, rawResult string) (turnOutputMaterialization, *turnOutputMaterializationFailure) {
	params, err := documentartifact.BuildImportParamsFromTarget(target, rawResult, taskNodeRunID(node), artifactUpdatedBy)
	if err != nil {
		return turnOutputMaterialization{}, validationMaterializationFailure("outputs.to_artifact: " + err.Error())
	}
	return turnOutputMaterialization{Result: encodeSharedfileResultRef(params.TargetPath), Artifact: &artifactMaterialization{Params: params}}, nil
}

// configuredSharedfilePath 返回 outputs.to_sharedfile.path 的清理后值。
func configuredSharedfilePath(out nodeexec.OutputsConfig) string {
	if out.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(out.ToSharedfile.Path)
}

// encodeSharedfileResultRef 生成指向 sharedfile/artifact 路径的 node.result JSON。
func encodeSharedfileResultRef(path string) json.RawMessage {
	payload, err := json.Marshal(struct {
		Sharedfile struct {
			Path string `json:"path"`
		} `json:"sharedfile"`
	}{Sharedfile: struct {
		Path string `json:"path"`
	}{Path: path}})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return payload
}

// validationMaterializationFailure 构造 validation 类物化失败。
func validationMaterializationFailure(reason string) *turnOutputMaterializationFailure {
	return &turnOutputMaterializationFailure{Reason: "validation: " + reason}
}

// infrastructureMaterializationFailure 构造 infrastructure 类物化失败。
func infrastructureMaterializationFailure(reason string) *turnOutputMaterializationFailure {
	return &turnOutputMaterializationFailure{Reason: "infrastructure: " + reason}
}

// materializeArtifactAfterClaim 先声明 node 输出已被 claim，再执行 artifact import。
// import 失败会把节点推进 failed，避免 artifact 与节点状态不一致地静默成功。
func materializeArtifactAfterClaim(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, materialized turnOutputMaterialization) (json.RawMessage, bool, error) {
	if materialized.Artifact == nil {
		return materialized.Result, true, nil
	}
	if deps.ArtifactImporter == nil {
		err := handleMaterializationFailure(ctx, deps, logger, node, infrastructureMaterializationFailure("outputs.to_artifact: ArtifactImporter not wired"))
		return nil, false, err
	}
	defer documentartifact.CleanupSource(materialized.Artifact.Params.SourcePath)
	claimed, err := claimNodeOutputMaterialization(ctx, deps.FlowStore, deps.EventBus, deps.Metrics, logger, node, materialized.Result)
	if err != nil || !claimed {
		return nil, false, err
	}
	if _, err := deps.ArtifactImporter.ImportLocalFile(ctx, materialized.Artifact.Params); err != nil {
		failureErr := handleMaterializationFailure(ctx, deps, logger, node, artifactImportFailure(materialized.Artifact.Params.TargetPath, err))
		return nil, false, failureErr
	}
	return materialized.Result, true, nil
}

// artifactImportFailure 根据 import 错误类型选择 validation 或 infrastructure 分类。
func artifactImportFailure(targetPath string, err error) *turnOutputMaterializationFailure {
	reason := "outputs.to_artifact[" + targetPath + "]: " + err.Error()
	if errors.Is(err, sharedfilestore.ErrImportValidation) {
		return validationMaterializationFailure(reason)
	}
	return infrastructureMaterializationFailure(reason)
}

// handleMaterializationFailure 记录物化失败并把节点推进 failed。
func handleMaterializationFailure(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, failure *turnOutputMaterializationFailure) error {
	if failure == nil {
		return nil
	}
	if failure.SizeCapExceeded {
		deps.Metrics.IncCompleteSizeCapExceeded()
	}
	logger.Warn("dag subscriber: materialize agent output failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "reason", failure.Reason)
	advanced, err := advanceNodeFailedWithReason(ctx, deps.FlowStore, deps.EventBus, deps.Metrics, logger, node, failure.Reason, true)
	if err != nil {
		return err
	}
	if advanced && deps.NodeRouter != nil {
		deps.NodeRouter.invokeTerminalFailureHooksForTaskNode(ctx, node, nodeexec.NodeOutcome{Status: nodeexec.NodeStatusFailed, ErrorSummary: failure.Reason, FailureClass: classifyMaterializationFailure(failure)})
	}
	return nil
}

// recordLegacyResultCapMetric 为旧输出路径记录 4KB 上限超标指标。
func recordLegacyResultCapMetric(metrics *dagSubscriberCounter, logger *slog.Logger, node *taskdag.Node, result json.RawMessage) {
	if len(result) <= completeNodeResultCap {
		return
	}
	metrics.IncCompleteSizeCapExceeded()
	logger.Warn("dag subscriber: complete result exceeds ADR-006 4KB cap", "dag_key", node.DagKey, "node_key", node.NodeKey, "size", len(result))
}

// sharedfileOwnerFailure 按 sharedfileowner 错误分类构造物化失败。
func sharedfileOwnerFailure(reason string, err error) *turnOutputMaterializationFailure {
	if sharedfileowner.IsValidation(err) {
		return validationMaterializationFailure(reason)
	}
	return infrastructureMaterializationFailure(reason)
}

// writeAgentTurnSharedfile 按 owner 规则写入 agent 输出 sharedfile。
func writeAgentTurnSharedfile(ctx context.Context, writer nodeexec.SharedFileWriter, path, rawResult string, owner sharedfileowner.Owner) *turnOutputMaterializationFailure {
	if path == "" {
		return nil
	}
	if err := sharedfileowner.Write(ctx, writer, path, rawResult, owner); err != nil {
		return sharedfileOwnerFailure("outputs.to_sharedfile["+path+"]: "+err.Error(), err)
	}
	return nil
}
