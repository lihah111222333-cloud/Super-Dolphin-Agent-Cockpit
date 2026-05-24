package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// 下列包级 enum 切片是 schema 与 handler requireEnum 的单一真源。
// 修改 schema 字面量时必须同步切片，反之亦然 —— 编译期通过类型 + 单测覆盖
// 防止 drift。命名规约：<tool>_<field>_Enum。
//
// The slices below are the single source of truth shared by the schema
// builder (EnumStringSchema) and the handler-layer requireEnum fallback.
// Any change to one must update the other; tests cover the wiring.
var (
	listRunsStatusEnum   = []string{"running", "succeeded", "failed", "cancelled"}
	startDAGTriggerEnum  = []string{"manual", "auto", "scheduled", "external"}
	updateNodeStatusEnum = []string{"pending", "running", "done", "failed"}
)

type CreateDAGInput struct {
	AgentID      string               `json:"agent_id"`
	DagKey       string               `json:"dag_key"`
	Title        string               `json:"title"`
	Description  string               `json:"description,omitempty"`
	Schedule     DAGScheduleInput     `json:"schedule"`
	FinalNodeKey string               `json:"final_node_key,omitempty"`
	Nodes        []CreateDAGNodeInput `json:"nodes,omitempty"`
}

type DAGScheduleInput struct {
	Trigger           string `json:"trigger,omitempty"`
	DefaultRetry      int    `json:"default_retry,omitempty"`
	DefaultTimeoutSec int    `json:"default_timeout_sec,omitempty"`
	FailFast          bool   `json:"fail_fast,omitempty"`
	MaxConcurrency    int    `json:"max_concurrency,omitempty"`
	QueuePolicy       string `json:"queue_policy,omitempty"`
}

type CreateDAGNodeInput struct {
	NodeKey    string             `json:"node_key"`
	Title      string             `json:"title"`
	NodeType   string             `json:"node_type,omitempty"`
	AssignedTo string             `json:"assigned_to,omitempty"`
	DependsOn  []string           `json:"depends_on,omitempty"`
	CommandRef string             `json:"command_ref,omitempty"`
	Execution  *DAGExecutionInput `json:"execution,omitempty"`
}

type DAGExecutionInput struct {
	OnFailure  string `json:"on_failure,omitempty"`
	Pool       string `json:"pool,omitempty"`
	Priority   int    `json:"priority,omitempty"`
	Retry      int    `json:"retry,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type DAGKeyInput struct {
	DagKey string `json:"dag_key"`
}

type ListDAGsInput struct {
	Status  string `json:"status,omitempty"`
	Keyword string `json:"keyword,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type ListDAGsOutput struct {
	DAGs []contract.DAGSummary `json:"dags"`
}

type UpdateNodeInput struct {
	DagKey  string `json:"dag_key"`
	NodeKey string `json:"node_key"`
	RunID   int64  `json:"run_id"`
	Status  string `json:"status"`
	Result  string `json:"result,omitempty"`
}

// DispatchNodeInput 是 task_dispatch_node MCP 工具的 typed 入参。
// F6.5 后 run_id 必填，用来定位当前 run 的 runtime node。
//
// DispatchNodeInput is the typed input for the task_dispatch_node MCP tool.
// All fields are required; run_id scopes the runtime node.
type DispatchNodeInput struct {
	DagKey     string `json:"dag_key"`
	NodeKey    string `json:"node_key"`
	RunID      int64  `json:"run_id"`
	AssignedTo string `json:"assigned_to"`
}

func HandleCreateDAG(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in CreateDAGInput) (any, error) {
		req, err := createDAGRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.CreateDAG(ctx, req)
	})
}

func HandleGetDAG(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in DAGKeyInput) (any, error) {
		dagKey, err := requireTrimmed(in.DagKey, "dag_key")
		if err != nil {
			return nil, err
		}
		return svc.GetDAG(ctx, dagKey)
	})
}

func HandleListDAGs(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ListDAGsInput) (any, error) {
		dags, err := svc.ListDAGs(ctx, contract.ListDAGsFilter{
			Status:  strings.TrimSpace(in.Status),
			Keyword: strings.TrimSpace(in.Keyword),
			Limit:   in.Limit,
		})
		if err != nil {
			return nil, err
		}
		return ListDAGsOutput{DAGs: dags}, nil
	})
}

func HandleUpdateNode(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in UpdateNodeInput) (any, error) {
		req, err := updateNodeRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.UpdateNodeStatus(ctx, req)
	})
}

// HandleDispatchNode 是 task_dispatch_node MCP 工具的 handler（ADR-004 §Open Q1）。
// 在 service.DispatchNode 返 ErrDispatchStoreUnset / ErrDispatchNodeIneligible
// 时转中英双语错误，让使用者一眼看出不能继续的原因。
//
// HandleDispatchNode wires the task_dispatch_node MCP tool. Sentinel errors
// (ErrDispatchStoreUnset / ErrDispatchNodeIneligible) are translated into
// bilingual messages here so callers get actionable context.
func HandleDispatchNode(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in DispatchNodeInput) (any, error) {
		resp, err := svc.DispatchNode(ctx, contract.DispatchNodeRequest{
			DagKey:     in.DagKey,
			NodeKey:    in.NodeKey,
			RunID:      in.RunID,
			AssignedTo: in.AssignedTo,
		})
		if err != nil {
			return nil, translateDispatchNodeError(in, err)
		}
		return resp, nil
	})
}

func translateDispatchNodeError(in DispatchNodeInput, err error) error {
	switch {
	case errors.Is(err, orchestration.ErrDispatchStoreUnset):
		return fmt.Errorf(
			"dispatch store 未接线，该 mcp-orch 启动模式不支持 task_dispatch_node; dispatch store not wired in this mcp-orch build: %w",
			err,
		)
	case errors.Is(err, orchestration.ErrDispatchNodeIneligible):
		return fmt.Errorf(
			"节点 %s/%s 当前状态不允许 dispatch（仅 pending/ready 可推进）; node %s/%s not in pending/ready: %w",
			in.DagKey, in.NodeKey, in.DagKey, in.NodeKey, err,
		)
	}
	return err
}

// StartDAGInput 是 task_start_dag MCP 工具的 typed 入参（T1.1）。
type StartDAGInput struct {
	DagKey         string `json:"dag_key"`
	TriggerSource  string `json:"trigger_source,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// TerminateDAGInput 是 task_terminate_dag MCP 工具的 typed 入参。
type TerminateDAGInput struct {
	DagKey string `json:"dag_key"`
	RunKey string `json:"run_key"`
	Reason string `json:"reason,omitempty"`
}

// GetRunInput 是 task_get_run MCP 工具的 typed 入参（T3.1）。
// GetRunInput is the typed input for the task_get_run MCP tool (T3.1).
type GetRunInput struct {
	RunKey string `json:"run_key"`
}

// ListRunsInput 是 task_list_runs MCP 工具的 typed 入参（T3.2）。
// dag_key 必填；status / limit 可选。status 枚举与 migration 0080
// task_dag_runs.status CHECK 对齐：service / store 不重复校验，错误
// status 由 DB CHECK 拒绝。
//
// ListRunsInput is the typed input for the task_list_runs MCP tool (T3.2).
// dag_key required; status / limit optional. The status enum mirrors
// migration 0080 task_dag_runs.status CHECK; service / store skip
// re-validation and rely on the DB CHECK to reject illegal values.
type ListRunsInput struct {
	DagKey string `json:"dag_key"`
	Status string `json:"status,omitempty"`
	Limit  int32  `json:"limit,omitempty"`
}

// ApplyOpsInput 是 task_dag_apply_ops MCP 工具的 typed 入参（T2.1）。
// Ops 是 raw JSON：service 内部用 nodeexec.Ops UnmarshalJSON 解码为 typed Op slice。
type ApplyOpsInput struct {
	DagKey      string          `json:"dag_key"`
	BaseVersion int64           `json:"base_version"`
	Ops         json.RawMessage `json:"ops"`
}

// HandleApplyOps 是 task_dag_apply_ops MCP 工具的 handler（T2.1）。
// 骨架阶段：service.ApplyOps 返回 ErrLifecycleNotImplemented；F4.1-F4.5 真实补齐
// add/update/remove + 环检测 + base_version OCC。
func HandleApplyOps(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ApplyOpsInput) (any, error) {
		dagKey, err := requireTrimmed(in.DagKey, "dag_key")
		if err != nil {
			return nil, err
		}
		return svc.ApplyOps(ctx, contract.ApplyOpsRequest{
			DagKey:      dagKey,
			BaseVersion: in.BaseVersion,
			Ops:         append(json.RawMessage(nil), in.Ops...),
		})
	})
}

// HandleListRuns 是 task_list_runs MCP 工具的 handler（T3.2）。
// 调 service.ListRuns 后返回 {runs: [...]} 包对象（留 next_cursor / total
// 等扩展位）。list 路径无业务 sentinel（DAG 不存在返空 slice，store
// 未定义判空为 sentinel），错误走默认 fallback。
//
// HandleListRuns is the task_list_runs MCP tool handler (T3.2). It calls
// service.ListRuns and returns {runs: [...]} (object wrapper reserves room
// for next_cursor / total etc.). The list path has no business sentinels
// (a missing DAG yields an empty slice rather than an error), so errors
// fall through to the default translation.
func HandleListRuns(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ListRunsInput) (any, error) {
		req, err := listRunsRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.ListRuns(ctx, req)
	})
}

// listRunsRequestFromInput 把 ListRunsInput 校验为 contract.ListRunsRequest。
// status 可选：空串视为「不过滤」放行；非空必须命中 listRunsStatusEnum
// （与 schema 单源 + DB CHECK 三层互锁）。
//
// listRunsRequestFromInput validates the ListRunsInput. status is optional:
// empty means "no filter"; a non-empty value must hit listRunsStatusEnum
// (single source shared with the schema; mirrored by the migration CHECK).
func listRunsRequestFromInput(in ListRunsInput) (contract.ListRunsRequest, error) {
	dagKey, err := requireTrimmed(in.DagKey, "dag_key")
	if err != nil {
		return contract.ListRunsRequest{}, err
	}
	status := strings.TrimSpace(in.Status)
	if status != "" {
		validated, err := requireEnum(status, "status", listRunsStatusEnum)
		if err != nil {
			return contract.ListRunsRequest{}, err
		}
		status = validated
	}
	return contract.ListRunsRequest{
		DagKey: dagKey,
		Status: status,
		Limit:  in.Limit,
	}, nil
}

// HandleStartDAG 是 task_start_dag MCP 工具的 handler（T1.1）。
// 骨架阶段：service.StartDAG 返回 ErrLifecycleNotImplemented，
// MCP 客户端会收到结构化错误；T1.2 接通真实路径后返回 run_key + version。
//
// 错误转译（路线 N）：
//   - ErrIdempotencyKeyExhausted → 中英双语提示 + 携带旧 RunKey + status，
//     方便 AI agent 决策是否换 idempotency_key 重试。
//   - ErrDAGAlreadyRunning → 中英双语提示。
//   - ErrDAGNotFound → 中英双语提示 + 提示先调 task_create_dag。
//
// Error translation (route N):
//   - ErrIdempotencyKeyExhausted → bilingual hint with previous RunKey +
//     status so the AI caller can decide to retry with a fresh idempotency_key.
//   - ErrDAGAlreadyRunning → bilingual hint.
//   - ErrDAGNotFound → bilingual hint pointing the caller to task_create_dag.
func HandleStartDAG(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in StartDAGInput) (any, error) {
		req, err := startDAGRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		resp, err := svc.StartDAG(ctx, req)
		if err != nil {
			return nil, translateStartDAGError(req.DagKey, err)
		}
		return resp, nil
	})
}

func HandleTerminateDAG(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in TerminateDAGInput) (any, error) {
		req, err := terminateDAGRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		if err := svc.TerminateDAG(ctx, req); err != nil {
			return nil, translateTerminateDAGError(req, err)
		}
		return struct{}{}, nil
	})
}

// startDAGRequestFromInput 把 StartDAGInput 校验为 contract.StartDAGRequest。
// trigger_source 可选：非空必须命中 startDAGTriggerEnum（schema 单源 +
// migration 0082 CHECK 双闸）。
//
// startDAGRequestFromInput validates the StartDAGInput. trigger_source is
// optional; a non-empty value must hit startDAGTriggerEnum (single source
// shared with the schema; mirrored by migration 0082 CHECK).
func startDAGRequestFromInput(in StartDAGInput) (contract.StartDAGRequest, error) {
	dagKey, err := requireTrimmed(in.DagKey, "dag_key")
	if err != nil {
		return contract.StartDAGRequest{}, err
	}
	trigger := strings.TrimSpace(in.TriggerSource)
	if trigger != "" {
		validated, err := requireEnum(trigger, "trigger_source", startDAGTriggerEnum)
		if err != nil {
			return contract.StartDAGRequest{}, err
		}
		trigger = validated
	}
	return contract.StartDAGRequest{
		DagKey:         dagKey,
		TriggerSource:  trigger,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
	}, nil
}

func terminateDAGRequestFromInput(in TerminateDAGInput) (contract.TerminateDAGRequest, error) {
	dagKey, err := requireTrimmed(in.DagKey, "dag_key")
	if err != nil {
		return contract.TerminateDAGRequest{}, err
	}
	runKey, err := requireTrimmed(in.RunKey, "run_key")
	if err != nil {
		return contract.TerminateDAGRequest{}, err
	}
	return contract.TerminateDAGRequest{
		DagKey: dagKey,
		RunKey: runKey,
		Reason: strings.TrimSpace(in.Reason),
	}, nil
}

// HandleGetRun 是 task_get_run MCP 工具的 handler（T3.1）。
// 调用 service.GetRun 返回 contract.GetRunResponse（run + runtime nodes）。
//
// 错误转译：
//   - ErrRunNotFound → 中英双语提示 + run_key。
//
// HandleGetRun is the task_get_run MCP tool handler (T3.1). It calls
// service.GetRun and returns contract.GetRunResponse (run + runtime nodes).
//
// Error translation:
//   - ErrRunNotFound → bilingual hint with the offending run_key.
func HandleGetRun(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in GetRunInput) (any, error) {
		runKey, err := requireTrimmed(in.RunKey, "run_key")
		if err != nil {
			return nil, err
		}
		resp, err := svc.GetRun(ctx, contract.GetRunRequest{RunKey: runKey})
		if err != nil {
			return nil, translateGetRunError(runKey, err)
		}
		return resp, nil
	})
}

// translateGetRunError 把 service 层 sentinel 包为中英双语 MCP 错误。
// 保留 errors.Is 可命中原 sentinel。
//
// translateGetRunError wraps service-layer sentinels into bilingual MCP
// errors while preserving errors.Is matching against the original sentinel.
func translateGetRunError(runKey string, err error) error {
	if errors.Is(err, orchestration.ErrRunNotFound) {
		return fmt.Errorf(
			"run 不存在：run_key=%s。请检查传入的 run_key 是否正确，或先调 task_start_dag 启动 run (run_key=%s, please verify the run_key or call task_start_dag first): %w",
			runKey, runKey, err,
		)
	}
	return err
}

func translateTerminateDAGError(req contract.TerminateDAGRequest, err error) error {
	if errors.Is(err, orchestration.ErrRunNotFound) {
		return fmt.Errorf(
			"无法停止：run 不存在或已不可访问 (dag_key=%s, run_key=%s); cannot stop missing run (dag_key=%s, run_key=%s): %w",
			req.DagKey, req.RunKey, req.DagKey, req.RunKey, err,
		)
	}
	return err
}

// translateStartDAGError 把 service 层的 sentinel 包装成中英双语 MCP 错误。
// 保留 errors.Is 可命中原 sentinel（依赖 fmt.Errorf %w）。
//
// translateStartDAGError wraps service-layer sentinels into bilingual MCP
// errors while preserving errors.Is matching against the original sentinel.
func translateStartDAGError(dagKey string, err error) error {
	var exhausted *orchestration.IdempotencyKeyExhaustedError
	if errors.As(err, &exhausted) {
		return fmt.Errorf(
			"幂等键已耗尽：上次 run 已失败/取消，请换新 idempotency_key 重试 (run_key=%s, status=%s); idempotency key exhausted: previous run is failed/cancelled, retry with a new idempotency_key (run_key=%s, status=%s): %w",
			exhausted.RunKey, exhausted.Status,
			exhausted.RunKey, exhausted.Status,
			err,
		)
	}
	if errors.Is(err, orchestration.ErrDAGAlreadyRunning) {
		return fmt.Errorf(
			"DAG 已有在跑 run，拒绝并发启动 (dag_key=%s); dag already has an active run, refusing concurrent start (dag_key=%s): %w",
			dagKey, dagKey, err,
		)
	}
	if errors.Is(err, orchestration.ErrDAGNotFound) {
		// dag_key 取自 handler 入参（service 层触发点也带，这里选入参途径保证不受 sentinel 包装形式影响）。
		// dag_key comes from the handler input; service-layer error already wraps it,
		// but using the input keeps the bilingual message stable regardless of
		// upstream wrapping shape.
		return fmt.Errorf(
			"DAG 不存在：dag_key=%s。请先调 task_create_dag 创建后再启动 (dag_key=%s, please call task_create_dag first): %w",
			dagKey, dagKey, err,
		)
	}
	return err
}

// taskToolDefinitions 按 CRUD + lifecycle 分组排序，让读者一眼看出 DAG
// 写入 / 生命周期 / 读取 三类工具的边界：
//   - 写入类（状态变更）：task_create_dag / task_dag_apply_ops / task_update_node
//   - 生命周期（起停）：task_start_dag / task_terminate_dag
//   - 读取类：task_get_dag / task_get_run / task_list_runs
//
// taskToolDefinitions groups tools as writes → lifecycle → reads so readers
// can locate a tool by intent at a glance.
func taskToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return buildToolDefinitions(
		// ---- writes ----
		defineTool("task_create_dag", "Create or upsert a DAG and its nodes in the orchestration store.", createDAGSchema(), HandleCreateDAG(svc)),
		defineTool("task_dag_apply_ops", "Apply a typed ops batch (add_node / update_node / remove_node / update_dag) with base_version OCC. Ops shape: see nodeexec.Ops. Skeleton stage returns ErrLifecycleNotImplemented.", ObjectSchema(map[string]Schema{
			"dag_key":      StringSchema("Target DAG key."),
			"base_version": IntegerSchema("Expected current dag.version (OCC; mismatch returns conflict)."),
			"ops":          ArraySchema(ObjectSchema(map[string]Schema{}, "op"), "Typed ops array; each item must include 'op' discriminator."),
		}, "dag_key", "base_version", "ops"), HandleApplyOps(svc)),
		defineTool("task_update_node", "Update the runtime status for a DAG node.", ObjectSchema(map[string]Schema{
			"dag_key":  StringSchema("DAG key."),
			"node_key": StringSchema("Node key within the DAG."),
			"run_id":   IntegerSchema("Task DAG run id that owns the runtime node."),
			"status":   EnumStringSchema("New node status.", updateNodeStatusEnum...),
			"result":   StringSchema("Optional result summary."),
		}, "dag_key", "node_key", "run_id", "status"), HandleUpdateNode(svc)),
		defineTool("task_dispatch_node", "Explicitly assign an agent to a pending/ready DAG node and enqueue a wakeup so the dispatcher launches it. Use when a node has assigned_to='' (ADR-004 §Open Q1).", ObjectSchema(map[string]Schema{
			"dag_key":     StringSchema("DAG key."),
			"node_key":    StringSchema("Node key within the DAG."),
			"run_id":      IntegerSchema("Task DAG run id that owns the runtime node."),
			"assigned_to": StringSchema("Agent id to dispatch the node to."),
		}, "dag_key", "node_key", "run_id", "assigned_to"), HandleDispatchNode(svc)),
		// ---- lifecycle ----
		defineTool("task_start_dag", "Trigger a new DAG execution (creates a run, snapshots dag.version). Skeleton stage returns ErrLifecycleNotImplemented; T1.2 wires the real path.", ObjectSchema(map[string]Schema{
			"dag_key":         StringSchema("DAG to start."),
			"trigger_source":  EnumStringSchema("Trigger source.", startDAGTriggerEnum...),
			"idempotency_key": StringSchema("Optional, prevents duplicate run on retry."),
		}, "dag_key"), HandleStartDAG(svc)),
		defineTool("task_terminate_dag", "Cancel one running DAG execution by run_key. This marks non-terminal runtime nodes cancelled and stops pending/dispatching/sent wakeups.", ObjectSchema(map[string]Schema{
			"dag_key": StringSchema("DAG key used as a fence for the run."),
			"run_key": StringSchema("Run key to cancel."),
			"reason":  StringSchema("Optional cancellation reason."),
		}, "dag_key", "run_key"), HandleTerminateDAG(svc)),
		// ---- reads ----
		defineTool("task_list_dags", "List DAGs for console views and final_output retention checks.", ObjectSchema(map[string]Schema{
			"status":  StringSchema("Optional status filter."),
			"keyword": StringSchema("Optional keyword filter."),
			"limit":   IntegerSchema("Optional max rows; defaults to service limit when 0/omitted."),
		}), HandleListDAGs(svc)),
		defineTool("task_get_dag", "Fetch a DAG and all of its nodes.", ObjectSchema(map[string]Schema{
			"dag_key": StringSchema("Unique DAG key."),
		}, "dag_key"), HandleGetDAG(svc)),
		defineTool("task_get_run", "Fetch a single DAG run by run_key, including the run's runtime node snapshot. task_get_dag reads the DAG template.", ObjectSchema(map[string]Schema{
			"run_key": StringSchema("Run key returned by task_start_dag."),
		}, "run_key"), HandleGetRun(svc)),
		defineTool("task_list_runs", "List recent runs for a DAG (object response wraps the runs slice for forward-compatibility). Status enum mirrors migration 0080 task_dag_runs.status CHECK.", ObjectSchema(map[string]Schema{
			"dag_key": StringSchema("DAG key to list runs for."),
			"status":  EnumStringSchema("Optional status filter.", listRunsStatusEnum...),
			"limit":   IntegerSchema("Optional max rows; defaults to 50 when 0/omitted."),
		}, "dag_key"), HandleListRuns(svc)),
	)
}

func createDAGSchema() Schema {
	return ObjectSchema(map[string]Schema{
		"agent_id":    StringSchema("Creator orchestration agent ID."),
		"dag_key":     StringSchema("Unique DAG key."),
		"title":       StringSchema("DAG title."),
		"description": StringSchema("Optional DAG description."),
		"schedule": ObjectSchema(map[string]Schema{
			"trigger":             StringSchema("Start trigger."),
			"default_retry":       IntegerSchema("Default retry count for nodes."),
			"default_timeout_sec": IntegerSchema("Default node timeout in seconds."),
			"fail_fast":           BooleanSchema("Stop scheduling new nodes on first failure."),
			"max_concurrency":     IntegerSchema("Maximum parallel runnable nodes."),
			"queue_policy":        StringSchema("Ready-queue policy."),
		}),
		"final_node_key": StringSchema("Optional node_key that produces the run-level final_output."),
		"nodes": ArraySchema(ObjectSchema(map[string]Schema{
			"node_key":    StringSchema("Unique node key within the DAG."),
			"title":       StringSchema("Node title."),
			"node_type":   StringSchema("Optional node type."),
			"assigned_to": StringSchema("Optional assignee."),
			"depends_on":  ArraySchema(StringSchema("Dependency node key."), "Node dependency keys."),
			"command_ref": StringSchema("Optional command card key."),
			"execution": ObjectSchema(map[string]Schema{
				"on_failure":  StringSchema("Failure policy override."),
				"pool":        StringSchema("Execution pool name."),
				"priority":    IntegerSchema("Queue priority."),
				"retry":       IntegerSchema("Retry override."),
				"timeout_sec": IntegerSchema("Timeout override in seconds."),
			}),
		}, "node_key", "title"), "Optional DAG nodes."),
	}, "agent_id", "dag_key", "title", "schedule")
}

func createDAGRequestFromInput(in CreateDAGInput) (contract.CreateDAGRequest, error) {
	// Preserve schedule in DAG metadata until the service contract grows a
	// first-class schedule field.
	agentID, err := requireTrimmed(in.AgentID, "agent_id")
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	dagKey, err := requireTrimmed(in.DagKey, "dag_key")
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	title, err := requireTrimmed(in.Title, "title")
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	nodes, err := createDAGNodesFromInput(in.Nodes)
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	finalNodeKey, err := normalizeFinalNodeKey(in.FinalNodeKey, nodes)
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	metadata, err := encodeJSONRaw(createDAGMetadata(in.Schedule, finalNodeKey))
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	return contract.CreateDAGRequest{
		DagKey:      dagKey,
		Title:       title,
		Description: strings.TrimSpace(in.Description),
		CreatedBy:   agentID,
		Metadata:    metadata,
		Nodes:       nodes,
	}, nil
}

func createDAGNodesFromInput(nodes []CreateDAGNodeInput) ([]contract.CreateDAGNodeRequest, error) {
	mapped := make([]contract.CreateDAGNodeRequest, 0, len(nodes))
	for i, node := range nodes {
		nodeKey, err := requireTrimmed(node.NodeKey, fmt.Sprintf("nodes[%d].node_key", i))
		if err != nil {
			return nil, err
		}
		title, err := requireTrimmed(node.Title, fmt.Sprintf("nodes[%d].title", i))
		if err != nil {
			return nil, err
		}
		// Preserve node-level execution overrides in Config for the same reason.
		config, err := encodeJSONRaw(nodeConfig(node.Execution))
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, contract.CreateDAGNodeRequest{
			NodeKey:    nodeKey,
			Title:      title,
			NodeType:   strings.TrimSpace(node.NodeType),
			AssignedTo: strings.TrimSpace(node.AssignedTo),
			DependsOn:  append([]string(nil), node.DependsOn...),
			CommandRef: strings.TrimSpace(node.CommandRef),
			Config:     config,
		})
	}
	return mapped, nil
}

func updateNodeRequestFromInput(in UpdateNodeInput) (contract.UpdateNodeStatusRequest, error) {
	dagKey, err := requireTrimmed(in.DagKey, "dag_key")
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	nodeKey, err := requireTrimmed(in.NodeKey, "node_key")
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	if in.RunID <= 0 {
		return contract.UpdateNodeStatusRequest{}, fmt.Errorf("run_id is required for runtime node status update")
	}
	// status 必填 + 必须命中 schema enum；ValidateTransition (service 层) 仍会
	// 进一步检查 from→to 的合法性。
	// status is required and must hit the schema enum; ValidateTransition
	// (service layer) still checks the from→to transition.
	status, err := requireEnum(in.Status, "status", updateNodeStatusEnum)
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	result, err := encodeOptionalString(strings.TrimSpace(in.Result))
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	return contract.UpdateNodeStatusRequest{
		DagKey:  dagKey,
		NodeKey: nodeKey,
		RunID:   in.RunID,
		Status:  status,
		Result:  result,
	}, nil
}

// createDAGMetadata 把 schedule 字段编码为 DAG metadata JSON 子树。
// 旧版 metadata 字段 (S15.1 删除 / migrations/0075_dag_v2_compat.sql 迁移) 不再处理：
// 数据库老行已一次性映射到 trigger 一等字段，tools 入参 schema 不再接受，
// 调用方如果传入会被忽略（向后兼容）。
func createDAGMetadata(schedule DAGScheduleInput, finalNodeKey string) map[string]any {
	metadata := map[string]any{"schedule": scheduleMap(schedule)}
	if finalNodeKey != "" {
		metadata["final_node_key"] = finalNodeKey
	}
	return metadata
}

func normalizeFinalNodeKey(raw string, nodes []contract.CreateDAGNodeRequest) (string, error) {
	finalNodeKey := strings.TrimSpace(raw)
	if finalNodeKey == "" {
		return "", nil
	}
	for _, node := range nodes {
		if node.NodeKey == finalNodeKey {
			return finalNodeKey, nil
		}
	}
	return "", fmt.Errorf("final_node_key %s does not match any node_key", finalNodeKey)
}

func nodeConfig(execution *DAGExecutionInput) map[string]any {
	if execution == nil {
		return nil
	}
	return map[string]any{"execution": executionMap(*execution)}
}

func scheduleMap(in DAGScheduleInput) map[string]any {
	payload := make(map[string]any)
	if in.Trigger != "" {
		payload["trigger"] = strings.TrimSpace(in.Trigger)
	}
	if in.DefaultRetry != 0 {
		payload["default_retry"] = in.DefaultRetry
	}
	if in.DefaultTimeoutSec != 0 {
		payload["default_timeout_sec"] = in.DefaultTimeoutSec
	}
	if in.FailFast {
		payload["fail_fast"] = true
	}
	if in.MaxConcurrency != 0 {
		payload["max_concurrency"] = in.MaxConcurrency
	}
	if in.QueuePolicy != "" {
		payload["queue_policy"] = strings.TrimSpace(in.QueuePolicy)
	}
	return payload
}

func executionMap(in DAGExecutionInput) map[string]any {
	payload := make(map[string]any)
	if in.OnFailure != "" {
		payload["on_failure"] = strings.TrimSpace(in.OnFailure)
	}
	if in.Pool != "" {
		payload["pool"] = strings.TrimSpace(in.Pool)
	}
	if in.Priority != 0 {
		payload["priority"] = in.Priority
	}
	if in.Retry != 0 {
		payload["retry"] = in.Retry
	}
	if in.TimeoutSec != 0 {
		payload["timeout_sec"] = in.TimeoutSec
	}
	return payload
}

func encodeJSONRaw(value any) (json.RawMessage, error) {
	return marshalRawJSON(value, rawJSONOptions{})
}

func encodeOptionalString(value string) (json.RawMessage, error) {
	return marshalRawJSON(value, rawJSONOptions{OmitEmptyString: true})
}
