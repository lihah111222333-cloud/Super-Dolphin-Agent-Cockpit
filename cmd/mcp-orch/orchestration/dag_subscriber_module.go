package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/sharedfileowner"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// ProvideDAGSubscriberNodeFlowStore narrows the aggregate taskdag.Store down
// to the NodeFlowStore needed by the DAG turn.completed subscriber
// (ADR-017 v1.2 §2.9). Type assertion is statically guarded by
// store_compile_assertions_test.go's
// `var _ NodeFlowStore = (*store)(nil)`.
//
// We mirror taskdag.ProvideDispatchNodeStore pattern (also a narrow-port
// adapter via type assertion). No new fx wrapper struct — direct interface
// return so fx can resolve `DAGSubscriberDeps.FlowStore`.
// ProvideDAGSubscriberNodeFlowStore 提供DAG订阅器节点flow存储。
func ProvideDAGSubscriberNodeFlowStore(store taskdag.Store) taskdag.NodeFlowStore {
	return store
}

// ProvideDAGSubscriberStopAgentService narrows *service down to the
// single-method StopAgentService port required by the DAG subscriber's
// stop_helper call (ADR-016 v1.2 §3.2 contract #2). The wrapping is needed
// because fx resolves interfaces by their declared types — passing *service
// directly would shadow other StopAgentService consumers (none today, but
// the indirection keeps the contract narrow).
// ProvideDAGSubscriberStopAgentService 提供DAG订阅器stop代理服务。
func ProvideDAGSubscriberStopAgentService(s *service) StopAgentService {
	return s
}

// ProvideDAGSubscriberAgentThreadLookup adapts the orchestration-internal
// AgentThreadStore (set on *service.agentThreads via runtime wiring) into
// the AgentThreadLookup narrow port. The store ALREADY satisfies the
// required lookup/status methods, but fx resolves by declared interface — this
// provider keeps ListAll out of the subscriber's DI graph.
//
// Returning a nil AgentThreadLookup when *service has no agentThreads
// wired is intentional: StopSpawnedAgent's preflight handles a nil
// AgentThreadLookup with StopResultSkippedLookupFailed (stop_helper.go:150).
//
// ⚠️ P2 风险（W-A1 reviewer B 二审揭出，未阅手）：当前 nil 返回依赖
// 唯一 consumer（dag_turn_completed_subscriber.go:341 stopSpawnedAgentForSubscriber）
// 在调用前判 deps.AgentThreads == nil 即 return 的应用层短路；未来若新增
// AgentThreadLookup consumer 未判 nil 即 deref 会 nil panic。根治修法详 H13
// follow-up：改返非 nil 哨兵 lookup（GetByThreadID 永返 ErrNotFound）避免
// consumer 变多后隔离失效。
func ProvideDAGSubscriberAgentThreadLookup(s *service) AgentThreadLookup {
	if s == nil || s.agentThreads == nil {
		return nil
	}
	return s.agentThreads
}

type DAGSubscriberMetrics struct {
	CompleteDone, CompleteFailed, IdempotentSkipped int64
	LookupNoNode, LookupDirtyData, LookupFailed     int64
	CompleteSizeCapExceeded, CompleteResultEmpty    int64
}

type dagSubscriberCounter struct {
	completeDone, completeFailed, idempotentSkipped atomic.Int64
	lookupNoNode, lookupDirtyData, lookupFailed     atomic.Int64
	completeSizeCapExceeded, completeResultEmpty    atomic.Int64
}

var dagSubscriberMetrics = &dagSubscriberCounter{}

// DAGSubscriberCounters 处理DAG订阅器counters。
func DAGSubscriberCounters() DAGSubscriberMetrics { return dagSubscriberMetrics.Snapshot() }

// IncCompleteDone 累加completedone。
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

// IncIdempotentSkipped 累加idempotentskipped。
func (c *dagSubscriberCounter) IncIdempotentSkipped() {
	if c != nil {
		c.idempotentSkipped.Add(1)
	}
}

// IncLookupNoNode 累加lookupno节点。
func (c *dagSubscriberCounter) IncLookupNoNode() {
	if c != nil {
		c.lookupNoNode.Add(1)
	}
}

// IncLookupDirtyData 累加lookupdirty数据。
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

// IncCompleteSizeCapExceeded 累加completesizecapexceeded。
func (c *dagSubscriberCounter) IncCompleteSizeCapExceeded() {
	if c != nil {
		c.completeSizeCapExceeded.Add(1)
	}
}

// IncCompleteResultEmpty 累加complete结果empty。
func (c *dagSubscriberCounter) IncCompleteResultEmpty() {
	if c != nil {
		c.completeResultEmpty.Add(1)
	}
}

// Snapshot 处理快照。
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

type turnOutputMaterializationFailure struct {
	Reason          string
	SizeCapExceeded bool
}

type turnOutputMaterialization struct {
	Result         json.RawMessage
	SharedfilePath string
	RawResult      string
	Artifact       *artifactMaterialization
}

const artifactUpdatedBy = "dag-artifact"

type artifactMaterialization struct {
	Params sharedfilestore.ImportLocalFileParams
}

func classifyMaterializationFailure(failure *turnOutputMaterializationFailure) nodeexec.FailureClass {
	if failure == nil {
		return nodeexec.FailureClassValidation
	}
	if strings.HasPrefix(failure.Reason, "infrastructure:") {
		return nodeexec.FailureClassInfrastructure
	}
	return nodeexec.FailureClassValidation
}

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

// prepareTurnCompletedResult 准备turncompleted结果。
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

func agentNodeUsesArtifactResult(rawConfig json.RawMessage) bool {
	cfg, err := nodeexec.ParseAgentConfig(rawConfig)
	return err == nil && cfg != nil && cfg.Outputs.ToArtifact != nil
}

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

func shouldMaterializeAgentNodeResult(out nodeexec.OutputsConfig) bool {
	return out.ToNodeResult || configuredSharedfilePath(out) == ""
}

func prepareArtifactTurnCompletedResult(node *taskdag.Node, target *nodeexec.ArtifactTarget, rawResult string) (turnOutputMaterialization, *turnOutputMaterializationFailure) {
	plan, err := nodeexec.BuildArtifactImportPlan(target, rawResult, taskNodeRunID(node))
	if err != nil {
		return turnOutputMaterialization{}, validationMaterializationFailure("outputs.to_artifact: " + err.Error())
	}
	params := sharedfilestore.ImportLocalFileParams{SourcePath: plan.SourcePath, TargetPath: plan.TargetPath, ContentType: plan.ContentType, AllowedExtensions: plan.AllowedExtensions, AllowedSourceRoots: plan.AllowedSourceRoots, MaxBytes: plan.MaxBytes, Overwrite: plan.Overwrite, UpdatedBy: artifactUpdatedBy}
	return turnOutputMaterialization{Result: encodeSharedfileResultRef(plan.TargetPath), Artifact: &artifactMaterialization{Params: params}}, nil
}

func configuredSharedfilePath(out nodeexec.OutputsConfig) string {
	if out.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(out.ToSharedfile.Path)
}

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

func validationMaterializationFailure(reason string) *turnOutputMaterializationFailure {
	return &turnOutputMaterializationFailure{Reason: "validation: " + reason}
}

func infrastructureMaterializationFailure(reason string) *turnOutputMaterializationFailure {
	return &turnOutputMaterializationFailure{Reason: "infrastructure: " + reason}
}

func materializeArtifactAfterClaim(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, materialized turnOutputMaterialization) (json.RawMessage, bool) {
	if materialized.Artifact == nil {
		return materialized.Result, true
	}
	if deps.ArtifactImporter == nil {
		handleMaterializationFailure(ctx, deps, logger, node, infrastructureMaterializationFailure("outputs.to_artifact: ArtifactImporter not wired"))
		return nil, false
	}
	if !claimNodeOutputMaterialization(ctx, deps.FlowStore, deps.EventBus, logger, node, materialized.Result) {
		return nil, false
	}
	if _, err := deps.ArtifactImporter.ImportLocalFile(ctx, materialized.Artifact.Params); err != nil {
		handleMaterializationFailure(ctx, deps, logger, node, artifactImportFailure(materialized.Artifact.Params.TargetPath, err))
		return nil, false
	}
	return materialized.Result, true
}

func artifactImportFailure(targetPath string, err error) *turnOutputMaterializationFailure {
	reason := "outputs.to_artifact[" + targetPath + "]: " + err.Error()
	if errors.Is(err, sharedfilestore.ErrImportValidation) {
		return validationMaterializationFailure(reason)
	}
	return infrastructureMaterializationFailure(reason)
}

func handleMaterializationFailure(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, failure *turnOutputMaterializationFailure) {
	if failure == nil {
		return
	}
	if failure.SizeCapExceeded {
		dagSubscriberMetrics.IncCompleteSizeCapExceeded()
	}
	logger.Warn("dag subscriber: materialize agent output failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "reason", failure.Reason)
	if advanceNodeFailedWithReason(ctx, deps.FlowStore, deps.EventBus, logger, node, failure.Reason, true) && deps.NodeRouter != nil {
		deps.NodeRouter.invokeTerminalFailureHooksForTaskNode(ctx, node, nodeexec.NodeOutcome{Status: nodeexec.NodeStatusFailed, ErrorSummary: failure.Reason, FailureClass: classifyMaterializationFailure(failure)})
	}
}

func recordLegacyResultCapMetric(logger *slog.Logger, node *taskdag.Node, result json.RawMessage) {
	if len(result) <= completeNodeResultCap {
		return
	}
	dagSubscriberMetrics.IncCompleteSizeCapExceeded()
	logger.Warn("dag subscriber: complete result exceeds ADR-006 4KB cap", "dag_key", node.DagKey, "node_key", node.NodeKey, "size", len(result))
}

func sharedfileOwnerFailure(reason string, err error) *turnOutputMaterializationFailure {
	if sharedfileowner.IsValidation(err) {
		return validationMaterializationFailure(reason)
	}
	return infrastructureMaterializationFailure(reason)
}

func writeAgentTurnSharedfile(ctx context.Context, writer nodeexec.SharedFileWriter, path, rawResult string, owner sharedfileowner.Owner) *turnOutputMaterializationFailure {
	if path == "" {
		return nil
	}
	if err := sharedfileowner.Write(ctx, writer, path, rawResult, owner); err != nil {
		return sharedfileOwnerFailure("outputs.to_sharedfile["+path+"]: "+err.Error(), err)
	}
	return nil
}
