package orchestration

import (
      "context"
      "encoding/json"
      "fmt"
      "log/slog"
      "strings"
      "sync/atomic"

      "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
      "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
  )
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)
>>>>>>> my-v3-work-20260528

// ProvideDAGSubscriberNodeFlowStore narrows the aggregate taskdag.Store down
// to the NodeFlowStore needed by the DAG turn.completed subscriber
// (ADR-017 v1.2 §2.9). Type assertion is statically guarded by
// store_compile_assertions_test.go's
// `var _ NodeFlowStore = (*store)(nil)`.
//
// We mirror taskdag.ProvideDispatchNodeStore pattern (also a narrow-port
// adapter via type assertion). No new fx wrapper struct — direct interface
// return so fx can resolve `DAGSubscriberDeps.FlowStore`.
func ProvideDAGSubscriberNodeFlowStore(store taskdag.Store) taskdag.NodeFlowStore {
	return store
}

// ProvideDAGSubscriberStopAgentService narrows *service down to the
// single-method StopAgentService port required by the DAG subscriber's
// stop_helper call (ADR-016 v1.2 §3.2 contract #2). The wrapping is needed
// because fx resolves interfaces by their declared types — passing *service
// directly would shadow other StopAgentService consumers (none today, but
// the indirection keeps the contract narrow).
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

func DAGSubscriberCounters() DAGSubscriberMetrics { return dagSubscriberMetrics.Snapshot() }
func (c *dagSubscriberCounter) IncCompleteDone()  { if c != nil { c.completeDone.Add(1) } }
func (c *dagSubscriberCounter) IncCompleteFailed() {
	if c != nil {
		c.completeFailed.Add(1)
	}
}
func (c *dagSubscriberCounter) IncIdempotentSkipped() {
	if c != nil {
		c.idempotentSkipped.Add(1)
	}
}
func (c *dagSubscriberCounter) IncLookupNoNode() { if c != nil { c.lookupNoNode.Add(1) } }
func (c *dagSubscriberCounter) IncLookupDirtyData() {
	if c != nil {
		c.lookupDirtyData.Add(1)
	}
}
func (c *dagSubscriberCounter) IncLookupFailed() { if c != nil { c.lookupFailed.Add(1) } }
func (c *dagSubscriberCounter) IncCompleteSizeCapExceeded() {
	if c != nil {
		c.completeSizeCapExceeded.Add(1)
	}
}
func (c *dagSubscriberCounter) IncCompleteResultEmpty() {
	if c != nil {
		c.completeResultEmpty.Add(1)
	}
}
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
	wrapped, err := json.Marshal(struct{ Text string `json:"text"` }{Text: raw})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(wrapped)
}

func prepareTurnCompletedResult(nodeConfig json.RawMessage, nodeType, rawResult string) (turnOutputMaterialization, *turnOutputMaterializationFailure) {
	if strings.TrimSpace(nodeType) != "agent" {
		return turnOutputMaterialization{Result: encodeTurnResultForNodeUpdate(rawResult)}, nil
	}
	cfg, failure := parseAgentOutputConfig(nodeConfig)
	if failure != nil {
		return turnOutputMaterialization{}, failure
	}
	path := configuredSharedfilePath(cfg.Outputs)
	emitNodeResult := shouldMaterializeAgentNodeResult(cfg.Outputs)
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

func configuredSharedfilePath(out nodeexec.OutputsConfig) string {
	if out.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(out.ToSharedfile.Path)
}

func encodeSharedfileResultRef(path string) json.RawMessage {
	payload, err := json.Marshal(struct {
		Sharedfile struct{ Path string `json:"path"` } `json:"sharedfile"`
	}{Sharedfile: struct{ Path string `json:"path"` }{Path: path}})
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

func handleMaterializationFailure(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, failure *turnOutputMaterializationFailure) {
	if failure == nil {
		return
	}
	if failure.SizeCapExceeded {
		dagSubscriberMetrics.IncCompleteSizeCapExceeded()
	}
	logger.Warn("dag subscriber: materialize agent output failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "reason", failure.Reason)
	if advanceNodeFailedWithReason(ctx, deps.FlowStore, logger, node, failure.Reason) && deps.NodeRouter != nil {
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

func configuredSharedfileAlreadyExists(ctx context.Context, reader nodeexec.SharedFileReader, path string) (bool, *turnOutputMaterializationFailure) {
	if path == "" {
		return false, nil
	}
	if failure := validateAgentSharedfileReader(reader); failure != nil {
		return false, failure
	}
	_, exists, err := reader.ReadSharedFile(ctx, path)
	if err != nil {
		return false, infrastructureMaterializationFailure("outputs.to_sharedfile[" + path + "] preflight read: " + err.Error())
	}
	return exists, nil
}

func validateAgentSharedfileReader(reader nodeexec.SharedFileReader) *turnOutputMaterializationFailure {
	if reader != nil {
		return nil
	}
	return infrastructureMaterializationFailure("outputs.to_sharedfile configured but SharedFileReader not wired in DAG subscriber")
}

func validateAgentSharedfileWriter(writer nodeexec.SharedFileWriter) *turnOutputMaterializationFailure {
	if writer != nil {
		return nil
	}
	return infrastructureMaterializationFailure("outputs.to_sharedfile configured but SharedFileWriter not wired in DAG subscriber")
}

func writeAgentTurnSharedfile(ctx context.Context, writer nodeexec.SharedFileWriter, path, rawResult string) *turnOutputMaterializationFailure {
	if path == "" {
		return nil
	}
	if failure := validateAgentSharedfileWriter(writer); failure != nil {
		return failure
	}
	if err := writer.WriteSharedFile(ctx, path, rawResult); err != nil {
		return infrastructureMaterializationFailure("outputs.to_sharedfile[" + path + "]: " + err.Error())
	}
	return nil
}
