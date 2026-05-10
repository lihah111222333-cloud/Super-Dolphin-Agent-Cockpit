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

type CreateDAGInput struct {
	AgentID     string               `json:"agent_id"`
	DagKey      string               `json:"dag_key"`
	Title       string               `json:"title"`
	Description string               `json:"description,omitempty"`
	Schedule    DAGScheduleInput     `json:"schedule"`
	Nodes       []CreateDAGNodeInput `json:"nodes,omitempty"`
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

type UpdateNodeInput struct {
	DagKey  string `json:"dag_key"`
	NodeKey string `json:"node_key"`
	Status  string `json:"status"`
	Result  string `json:"result,omitempty"`
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

func HandleUpdateNode(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in UpdateNodeInput) (any, error) {
		req, err := updateNodeRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.UpdateNodeStatus(ctx, req)
	})
}

// StartDAGInput 是 task_start_dag MCP 工具的 typed 入参（T1.1）。
type StartDAGInput struct {
	DagKey         string `json:"dag_key"`
	TriggerSource  string `json:"trigger_source,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
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
		dagKey, err := requireTrimmed(in.DagKey, "dag_key")
		if err != nil {
			return nil, err
		}
		return svc.ListRuns(ctx, contract.ListRunsRequest{
			DagKey: dagKey,
			Status: strings.TrimSpace(in.Status),
			Limit:  in.Limit,
		})
	})
}

// HandleStartDAG 是 task_start_dag MCP 工具的 handler（T1.1）。
// 骨架阶段：service.StartDAG 返回 ErrLifecycleNotImplemented，
// MCP 客户端会收到结构化错误；T1.2 接通真实路径后返回 RunKey + Version。
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
		dagKey, err := requireTrimmed(in.DagKey, "dag_key")
		if err != nil {
			return nil, err
		}
		resp, err := svc.StartDAG(ctx, contract.StartDAGRequest{
			DagKey:         dagKey,
			TriggerSource:  strings.TrimSpace(in.TriggerSource),
			IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
		})
		if err != nil {
			return nil, translateStartDAGError(dagKey, err)
		}
		return resp, nil
	})
}

// HandleGetRun 是 task_get_run MCP 工具的 handler（T3.1）。
// 调用 service.GetRun 返回 contract.GetRunResponse（仅 run，不 inline 节点）。
//
// 错误转译：
//   - ErrRunNotFound → 中英双语提示 + run_key。
//
// HandleGetRun is the task_get_run MCP tool handler (T3.1). It calls
// service.GetRun and returns contract.GetRunResponse (run only; nodes are
// not inlined).
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

func taskToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("task_create_dag", "Create or upsert a DAG and its nodes in the orchestration store.", createDAGSchema(), HandleCreateDAG(svc)),
		defineTool("task_get_dag", "Fetch a DAG and all of its nodes.", ObjectSchema(map[string]Schema{
			"dag_key": StringSchema("Unique DAG key."),
		}, "dag_key"), HandleGetDAG(svc)),
		defineTool("task_update_node", "Update the runtime status for a DAG node.", ObjectSchema(map[string]Schema{
			"dag_key":  StringSchema("DAG key."),
			"node_key": StringSchema("Node key within the DAG."),
			"status":   EnumStringSchema("New node status.", "pending", "running", "done", "failed"),
			"result":   StringSchema("Optional result summary."),
		}, "dag_key", "node_key", "status"), HandleUpdateNode(svc)),
		defineTool("task_start_dag", "Trigger a new DAG execution (creates a run, snapshots dag.version). Skeleton stage returns ErrLifecycleNotImplemented; T1.2 wires the real path.", ObjectSchema(map[string]Schema{
			"dag_key":         StringSchema("DAG to start."),
			"trigger_source":  EnumStringSchema("Trigger source.", "manual", "auto", "scheduled", "external"),
			"idempotency_key": StringSchema("Optional, prevents duplicate run on retry."),
		}, "dag_key"), HandleStartDAG(svc)),
		defineTool("task_get_run", "Fetch a single DAG run by run_key. Returns the run row only; node-level data is fetched separately via task_get_dag.", ObjectSchema(map[string]Schema{
			"run_key": StringSchema("Run key returned by task_start_dag."),
		}, "run_key"), HandleGetRun(svc)),
		defineTool("task_dag_apply_ops", "Apply a typed ops batch (add_node / update_node / remove_node / update_dag) with base_version OCC. Ops shape: see nodeexec.Ops. Skeleton stage returns ErrLifecycleNotImplemented.", ObjectSchema(map[string]Schema{
			"dag_key":      StringSchema("Target DAG key."),
			"base_version": IntegerSchema("Expected current dag.version (OCC; mismatch returns conflict)."),
			"ops":          ArraySchema(ObjectSchema(map[string]Schema{}, "op"), "Typed ops array; each item must include 'op' discriminator."),
		}, "dag_key", "base_version", "ops"), HandleApplyOps(svc)),
		defineTool("task_list_runs", "List recent runs for a DAG (object response wraps the runs slice for forward-compatibility). Status enum mirrors migration 0080 task_dag_runs.status CHECK.", ObjectSchema(map[string]Schema{
			"dag_key": StringSchema("DAG key to list runs for."),
			"status":  EnumStringSchema("Optional status filter.", "running", "succeeded", "failed", "cancelled"),
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
	metadata, err := encodeJSONRaw(createDAGMetadata(in.Schedule))
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	nodes, err := createDAGNodesFromInput(in.Nodes)
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
	status, err := requireTrimmed(in.Status, "status")
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
		Status:  status,
		Result:  result,
	}, nil
}

// createDAGMetadata 把 schedule 字段编码为 DAG metadata JSON 子树。
// 旧版 metadata 字段 (S15.1 删除 / migrations/0075_dag_v2_compat.sql 迁移) 不再处理：
// 数据库老行已一次性映射到 trigger 一等字段，tools 入参 schema 不再接受，
// 调用方如果传入会被忽略（向后兼容）。
func createDAGMetadata(schedule DAGScheduleInput) map[string]any {
	return map[string]any{"schedule": scheduleMap(schedule)}
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
