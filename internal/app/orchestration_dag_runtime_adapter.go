package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
)

type dagToolCaller interface {
	HandleToolCall(context.Context, contract.ToolCallRawMessage) (any, error)
}

type mcpOrchDAGRuntime struct {
	tools             dagToolCaller
	peerReadyTimeout  time.Duration
	peerReadyInterval time.Duration
	now               func() time.Time
}

var _ contract.DAGRuntime = (*mcpOrchDAGRuntime)(nil)

const (
	defaultDAGRuntimePeerReadyTimeout      = 10 * time.Second
	defaultDAGRuntimePeerReadyPollInterval = 300 * time.Millisecond
)

func newMCPOrchDAGRuntime(handler *toolbridge.Handler) *mcpOrchDAGRuntime {
	return &mcpOrchDAGRuntime{tools: handler}
}

// ListDAGs 列出dags。
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

// GetDAG 读取DAG。
func (r *mcpOrchDAGRuntime) GetDAG(ctx context.Context, dagKey string) (contract.DAGDetail, error) {
	var out contract.DAGDetail
	err := r.call(ctx, "task_get_dag", map[string]any{
		"dag_key": strings.TrimSpace(dagKey),
	}, &out)
	return out, err
}

// StartDAG 启动DAG。
func (r *mcpOrchDAGRuntime) StartDAG(ctx context.Context, req contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	var out contract.StartDAGResponse
	err := r.call(ctx, "task_start_dag", map[string]any{
		"dag_key":         strings.TrimSpace(req.DagKey),
		"trigger_source":  strings.TrimSpace(req.TriggerSource),
		"idempotency_key": strings.TrimSpace(req.IdempotencyKey),
	}, &out)
	return out, err
}

// TerminateDAG 处理terminateDAG。
func (r *mcpOrchDAGRuntime) TerminateDAG(ctx context.Context, req contract.TerminateDAGRequest) error {
	return r.call(ctx, "task_terminate_dag", map[string]any{
		"dag_key": strings.TrimSpace(req.DagKey),
		"run_key": strings.TrimSpace(req.RunKey),
		"reason":  strings.TrimSpace(req.Reason),
	}, nil)
}

// DeleteDAG 删除DAG。
func (r *mcpOrchDAGRuntime) DeleteDAG(ctx context.Context, req contract.DeleteDAGRequest) error {
	return r.call(ctx, "task_delete_dag", map[string]any{
		"dag_key": strings.TrimSpace(req.DagKey),
	}, nil)
}

// ApplyOps 应用ops。
func (r *mcpOrchDAGRuntime) ApplyOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	var out contract.ApplyOpsResponse
	err := r.call(ctx, "task_dag_apply_ops", map[string]any{
		"dag_key":      strings.TrimSpace(req.DagKey),
		"base_version": req.BaseVersion,
		"ops":          json.RawMessage(append([]byte(nil), req.Ops...)),
	}, &out)
	return out, err
}

// DispatchNode 派发节点。
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

// ListRuns 列出运行记录。
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

// GetRun 读取运行记录。
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

// runDAGToolCall 运行DAG工具call。
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

func (r *mcpOrchDAGRuntime) peerTimeout() time.Duration {
	if r != nil && r.peerReadyTimeout > 0 {
		return r.peerReadyTimeout
	}
	return defaultDAGRuntimePeerReadyTimeout
}

func (r *mcpOrchDAGRuntime) peerPollInterval() time.Duration {
	if r != nil && r.peerReadyInterval > 0 {
		return r.peerReadyInterval
	}
	return defaultDAGRuntimePeerReadyPollInterval
}

func (r *mcpOrchDAGRuntime) clock() func() time.Time {
	if r != nil && r.now != nil {
		return r.now
	}
	return time.Now
}

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

// decodeDAGToolResult 解码DAG工具结果。
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
