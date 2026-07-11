package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge"
)

// dagToolCaller 抽象 toolbridge handler，便于 DAG runtime 只依赖工具调用能力。
type dagToolCaller interface {
	HandleToolCall(context.Context, contract.ToolCallRawMessage) (any, error)
}

// mcpOrchDAGRuntime 通过 toolbridge 调用独立 mcp-orch 的 DAG 工具。
// 它不直接导入 orchestration 模块，避免桌面进程内嵌另一套调度器。
type mcpOrchDAGRuntime struct {
	tools             dagToolCaller
	peerReadyTimeout  time.Duration
	peerReadyInterval time.Duration
	now               func() time.Time
}

// mcpOrchDAGRuntime 必须满足 DAG 读写运行时接口。
var _ contract.DAGRuntime = (*mcpOrchDAGRuntime)(nil)

// mcpOrchDAGRuntime 必须满足 DAG 创建运行时接口。
var _ contract.DAGCreateRuntime = (*mcpOrchDAGRuntime)(nil)

// mcpOrchDAGRuntime 必须满足 DAG 删除运行时接口。
var _ contract.DAGDeleteRuntime = (*mcpOrchDAGRuntime)(nil)

// mcp-orch peer 就绪等待默认值。
const (
	defaultDAGRuntimePeerReadyTimeout      = 10 * time.Second
	defaultDAGRuntimePeerReadyPollInterval = 300 * time.Millisecond
)

// newMCPOrchDAGRuntime 创建基于 toolbridge 的 DAG runtime。
func newMCPOrchDAGRuntime(handler *toolbridge.Handler) *mcpOrchDAGRuntime {
	return &mcpOrchDAGRuntime{tools: handler}
}

// ListDAGs 通过 mcp-orch task_list_dags 列出 DAG。
func (r *mcpOrchDAGRuntime) ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	var out struct {
		DAGs []contract.DAGSummary `json:"dags"`
	}
	err := r.call(ctx, "task_list_dags", map[string]any{
		"status":  strings.TrimSpace(filter.Status),
		"keyword": strings.TrimSpace(filter.Keyword),
		"limit":   filter.Limit,
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.DAGs == nil {
		return []contract.DAGSummary{}, nil
	}
	return out.DAGs, nil
}

// GetDAG 通过 mcp-orch task_get_dag 读取 DAG 明细。
func (r *mcpOrchDAGRuntime) GetDAG(ctx context.Context, dagKey string) (contract.DAGDetail, error) {
	var out contract.DAGDetail
	err := r.call(ctx, "task_get_dag", map[string]any{
		"dag_key": strings.TrimSpace(dagKey),
	}, &out)
	return out, err
}

// CreateDAG 通过 mcp-orch 的 task_create_dag 工具创建 DAG 模板。
func (r *mcpOrchDAGRuntime) CreateDAG(ctx context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
	args, err := createDAGToolArgs(req)
	if err != nil {
		return contract.DAGDetail{}, err
	}
	var out contract.DAGDetail
	err = r.call(ctx, "task_create_dag", args, &out)
	return out, err
}

// StartDAG 通过 mcp-orch task_start_dag 启动 DAG。
func (r *mcpOrchDAGRuntime) StartDAG(ctx context.Context, req contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	var out contract.StartDAGResponse
	err := r.call(ctx, "task_start_dag", map[string]any{
		"dag_key":         strings.TrimSpace(req.DagKey),
		"trigger_source":  strings.TrimSpace(req.TriggerSource),
		"idempotency_key": strings.TrimSpace(req.IdempotencyKey),
	}, &out)
	return out, err
}

// TerminateDAG 通过 mcp-orch task_terminate_dag 停止运行。
func (r *mcpOrchDAGRuntime) TerminateDAG(ctx context.Context, req contract.TerminateDAGRequest) error {
	return r.call(ctx, "task_terminate_dag", map[string]any{
		"dag_key": strings.TrimSpace(req.DagKey),
		"run_key": strings.TrimSpace(req.RunKey),
		"reason":  strings.TrimSpace(req.Reason),
	}, nil)
}

// DeleteDAG 通过 mcp-orch task_delete_dag 删除 DAG。
func (r *mcpOrchDAGRuntime) DeleteDAG(ctx context.Context, req contract.DeleteDAGRequest) error {
	return r.call(ctx, "task_delete_dag", map[string]any{
		"dag_key": strings.TrimSpace(req.DagKey),
	}, nil)
}

// ApplyOps 通过 mcp-orch task_dag_apply_ops 修改 DAG 模板。
func (r *mcpOrchDAGRuntime) ApplyOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	var out contract.ApplyOpsResponse
	err := r.call(ctx, "task_dag_apply_ops", map[string]any{
		"dag_key":      strings.TrimSpace(req.DagKey),
		"base_version": req.BaseVersion,
		"ops":          json.RawMessage(append([]byte(nil), req.Ops...)),
	}, &out)
	return out, err
}

// DispatchNode 通过 mcp-orch task_dispatch_node 派发运行节点。
func (r *mcpOrchDAGRuntime) DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	var out contract.DispatchNodeResponse
	err := r.call(ctx, "task_dispatch_node", map[string]any{
		"dag_key":     strings.TrimSpace(req.DagKey),
		"run_id":      req.RunID,
		"node_key":    strings.TrimSpace(req.NodeKey),
		"assigned_to": strings.TrimSpace(req.AssignedTo),
	}, &out)
	return out, err
}

// ListRuns 通过 mcp-orch task_list_runs 列出 DAG run。
func (r *mcpOrchDAGRuntime) ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	var out contract.ListRunsResponse
	err := r.call(ctx, "task_list_runs", map[string]any{
		"dag_key": strings.TrimSpace(req.DagKey),
		"status":  strings.TrimSpace(req.Status),
		"limit":   req.Limit,
	}, &out)
	if out.Runs == nil {
		out.Runs = []contract.Run{}
	}
	return out, err
}

// GetRun 通过 mcp-orch task_get_run 读取运行快照。
func (r *mcpOrchDAGRuntime) GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	var out contract.GetRunResponse
	err := r.call(ctx, "task_get_run", map[string]any{
		"run_key": strings.TrimSpace(req.RunKey),
	}, &out)
	if out.Nodes == nil {
		out.Nodes = []contract.DAGNode{}
	}
	return out, err
}

// call 编码工具请求、等待 mcp-orch peer 并解码结构化结果。
func (r *mcpOrchDAGRuntime) call(ctx context.Context, toolName string, args any, out any) error {
	if r == nil || r.tools == nil {
		return errors.New("app: mcp-orch DAG runtime is not configured")
	}
	msg, err := encodeDAGToolCall(toolName, args)
	if err != nil {
		return err
	}
	result, err := r.runDAGToolCall(ctx, toolName, msg)
	if err != nil {
		return err
	}
	return decodeDAGToolResult(toolName, result, out)
}

// encodeDAGToolCall 将 DAG runtime 请求包装为 toolbridge tools/call 消息。
func encodeDAGToolCall(toolName string, args any) (contract.ToolCallRawMessage, error) {
	argsRaw, err := json.Marshal(args)
	if err != nil {
		return contract.ToolCallRawMessage{}, fmt.Errorf("app: encode %s args: %w", toolName, err)
	}
	paramsRaw, err := json.Marshal(map[string]any{
		"name":       strings.TrimSpace(toolName),
		"arguments":  json.RawMessage(argsRaw),
		"clientKind": mcpdto.ClientKindOrch,
	})
	if err != nil {
		return contract.ToolCallRawMessage{}, fmt.Errorf("app: encode %s tool call: %w", toolName, err)
	}
	return contract.ToolCallRawMessage{
		ID:     json.RawMessage(`"dashboard-dag-runtime"`),
		Method: toolbridge.ProxyMethodToolsCall,
		Params: json.RawMessage(paramsRaw),
	}, nil
}

// createDAGToolArgs 将服务层创建请求转成 task_create_dag 入参。
func createDAGToolArgs(req contract.CreateDAGRequest) (map[string]any, error) {
	metadata, err := createDAGMetadataMap(req.Metadata)
	if err != nil {
		return nil, err
	}
	args := map[string]any{
		"agent_id":    strings.TrimSpace(req.CreatedBy),
		"dag_key":     strings.TrimSpace(req.DagKey),
		"title":       strings.TrimSpace(req.Title),
		"description": strings.TrimSpace(req.Description),
		"nodes":       createDAGToolNodes(req.Nodes),
	}
	if schedule, ok := metadata["schedule"].(map[string]any); ok && len(schedule) > 0 {
		args["schedule"] = schedule
	}
	if finalNodeKey := strings.TrimSpace(fmt.Sprint(metadata["final_node_key"])); finalNodeKey != "" && finalNodeKey != "<nil>" {
		args["final_node_key"] = finalNodeKey
	}
	return args, nil
}

// createDAGMetadataMap 解码 DAG metadata。
// 空 metadata 视为空对象，非法 JSON 直接报错，避免创建参数被静默丢弃。
func createDAGMetadataMap(raw json.RawMessage) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var metadata map[string]any
	if err := json.Unmarshal(trimmed, &metadata); err != nil {
		return nil, fmt.Errorf("app: decode task_create_dag metadata: %w", err)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	return metadata, nil
}

// createDAGToolNodes 将服务层节点请求转成 task_create_dag 节点数组。
func createDAGToolNodes(nodes []contract.CreateDAGNodeRequest) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, map[string]any{
			"node_key":    strings.TrimSpace(node.NodeKey),
			"title":       strings.TrimSpace(node.Title),
			"node_type":   strings.TrimSpace(node.NodeType),
			"assigned_to": strings.TrimSpace(node.AssignedTo),
			"depends_on":  append([]string(nil), node.DependsOn...),
			"command_ref": strings.TrimSpace(node.CommandRef),
			"config":      json.RawMessage(append([]byte(nil), node.Config...)),
		})
	}
	return out
}

// runDAGToolCall 等待 mcp-orch peer 就绪后执行一次工具调用。
// ErrNoPeerAvailable 会在短时间内轮询重试，其他错误立即返回。
func (r *mcpOrchDAGRuntime) runDAGToolCall(ctx context.Context, toolName string, msg contract.ToolCallRawMessage) (*toolbridge.ToolCallResult, error) {
	now := r.clock()
	timeout := r.peerTimeout()
	deadline := now().Add(timeout)
	for {
		result, err := r.runDAGToolCallOnce(ctx, toolName, msg)
		if err == nil || !errors.Is(err, toolbridge.ErrNoPeerAvailable) {
			return result, err
		}
		if now().After(deadline) {
			return nil, fmt.Errorf("app: mcp-orch peer not ready for %s after %s: %w", toolName, timeout, err)
		}
		wait := r.peerPollInterval()
		if remaining := deadline.Sub(now()); remaining < wait {
			wait = remaining
		}
		if wait <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("app: wait for mcp-orch %s peer: %w", toolName, ctx.Err())
		case <-time.After(wait):
		}
	}
}

// peerTimeout 返回等待 mcp-orch peer 的最长时间。
func (r *mcpOrchDAGRuntime) peerTimeout() time.Duration {
	if r != nil && r.peerReadyTimeout > 0 {
		return r.peerReadyTimeout
	}
	return defaultDAGRuntimePeerReadyTimeout
}

// peerPollInterval 返回等待 peer 时的轮询间隔。
func (r *mcpOrchDAGRuntime) peerPollInterval() time.Duration {
	if r != nil && r.peerReadyInterval > 0 {
		return r.peerReadyInterval
	}
	return defaultDAGRuntimePeerReadyPollInterval
}

// clock 返回可测试替换的当前时间函数。
func (r *mcpOrchDAGRuntime) clock() func() time.Time {
	if r != nil && r.now != nil {
		return r.now
	}
	return time.Now
}

// runDAGToolCallOnce 直接调用 toolbridge 并校验返回类型。
func (r *mcpOrchDAGRuntime) runDAGToolCallOnce(ctx context.Context, toolName string, msg contract.ToolCallRawMessage) (*toolbridge.ToolCallResult, error) {
	value, err := r.tools.HandleToolCall(ctx, contract.ToolCallRawMessage{
		ID:     msg.ID,
		Method: msg.Method,
		Params: msg.Params,
	})
	if err != nil {
		return nil, fmt.Errorf("app: call mcp-orch %s: %w", toolName, err)
	}
	result, ok := value.(*toolbridge.ToolCallResult)
	if !ok || result == nil {
		return nil, fmt.Errorf("app: call mcp-orch %s returned %T, want *toolbridge.ToolCallResult", toolName, value)
	}
	return result, nil
}

// decodeDAGToolResult 校验工具调用成功并解码结构化结果。
// out 为 nil 表示调用方只关心成功/失败，不要求响应体。
func decodeDAGToolResult(toolName string, result *toolbridge.ToolCallResult, out any) error {
	if !result.Success {
		return fmt.Errorf("app: call mcp-orch %s failed: %s", toolName, toolCallResultMessage(result))
	}
	if out == nil {
		return nil
	}
	raw := bytes.TrimSpace(result.StructuredContent)
	if len(raw) == 0 {
		raw = []byte(toolCallResultMessage(result))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("app: call mcp-orch %s returned empty result", toolName)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("app: decode mcp-orch %s result: %w", toolName, err)
	}
	return nil
}

// toolCallResultMessage 提取工具结果中的可读消息。
func toolCallResultMessage(result *toolbridge.ToolCallResult) string {
	if result == nil {
		return ""
	}
	for _, item := range result.ContentItems {
		if text := strings.TrimSpace(item.Text); text != "" {
			return text
		}
	}
	return strings.TrimSpace(string(result.StructuredContent))
}
