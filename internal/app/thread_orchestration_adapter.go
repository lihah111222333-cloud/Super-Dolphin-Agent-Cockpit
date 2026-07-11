package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge"
)

// mcpOrchOrchestrationFacade 通过 toolbridge 调用独立 mcp-orch 的 agent 生命周期工具。
// 它不直接注入完整编排服务，避免桌面进程内嵌另一套编排器。
type mcpOrchOrchestrationFacade struct {
	tools             dagToolCaller
	dependency        contract.DependencyConfig
	peerReadyTimeout  time.Duration
	peerReadyInterval time.Duration
	now               func() time.Time
}

// mcpOrchOrchestrationFacade 必须分别满足 thread 的生命周期和 generation 绑定端口。
var (
	_ thread.OrchestrationFacade     = (*mcpOrchOrchestrationFacade)(nil)
	_ thread.SessionGenerationBinder = (*mcpOrchOrchestrationFacade)(nil)
)

const (
	defaultOrchFacadePeerReadyTimeout      = 10 * time.Second
	defaultOrchFacadePeerReadyPollInterval = 300 * time.Millisecond
)

// newMCPOrchOrchestrationFacade 创建基于 toolbridge 的 thread orchestration facade。
func newMCPOrchOrchestrationFacade(
	ref *toolbridgeHandlerRef,
	dependency contract.DependencyConfig,
) *mcpOrchOrchestrationFacade {
	return &mcpOrchOrchestrationFacade{tools: ref, dependency: dependency}
}

// toolbridgeHandlerRef 延迟持有 toolbridge handler，打断 thread service 与 toolbridge host tools 的 fx 构造环。
type toolbridgeHandlerRef struct {
	mu      sync.RWMutex
	handler *toolbridge.Handler
}

// newToolbridgeHandlerRef 创建可被 orchestration facade 立即注入的 handler 引用容器。
func newToolbridgeHandlerRef() *toolbridgeHandlerRef {
	return &toolbridgeHandlerRef{}
}

// bindToolbridgeHandlerRef 在 toolbridge handler 完成构造后绑定运行时调用入口。
func bindToolbridgeHandlerRef(ref *toolbridgeHandlerRef, handler *toolbridge.Handler) error {
	if ref == nil {
		return errors.New("app: toolbridge handler ref is not configured")
	}
	if handler == nil {
		return errors.New("app: toolbridge handler is not configured")
	}
	ref.set(handler)
	return nil
}

// set 更新当前 toolbridge handler，供 fx invoke 在构造末尾绑定。
func (r *toolbridgeHandlerRef) set(handler *toolbridge.Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handler = handler
}

// HandleToolCall 将调用转发给已绑定的 toolbridge handler。
func (r *toolbridgeHandlerRef) HandleToolCall(ctx context.Context, msg contract.ToolCallRawMessage) (any, error) {
	if r == nil {
		return nil, errors.New("app: toolbridge handler ref is not configured")
	}
	r.mu.RLock()
	handler := r.handler
	r.mu.RUnlock()
	if handler == nil {
		return nil, errors.New("app: toolbridge handler is not configured")
	}
	return handler.HandleToolCall(ctx, msg)
}

// LaunchAgent 在桌面进程中不调用 mcp-orch。
// thread 自身的 Start/SpawnIfNeeded 已负责启动当前 provider session；真正的子 agent 由模型工具
// launch_agent 经 toolbridge 进入 mcp-orch。这里如果反调 launch_agent，会在父 Codex surface 尚未建立时失败。
func (f *mcpOrchOrchestrationFacade) LaunchAgent(context.Context, thread.LaunchAgentRequest) error {
	return nil
}

// StopAgent 通过 mcp-orch 的 stop_agent 工具停止 agent。
func (f *mcpOrchOrchestrationFacade) StopAgent(ctx context.Context, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	return f.call(ctx, "stop_agent", map[string]any{
		"agent_id": agentID,
	}, orchFacadeToolMetadata{agentID: agentID}, nil)
}

// Recover 通过 mcp-orch 的 recover_agent 工具恢复 agent。
func (f *mcpOrchOrchestrationFacade) Recover(ctx context.Context, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	return f.call(ctx, "recover_agent", map[string]any{
		"agent_id": agentID,
	}, orchFacadeToolMetadata{agentID: agentID}, nil)
}

// BindSessionGeneration 绑定 agent session generation。
// 当前 mcp-orch 未暴露为独立 MCP 工具；允许缺失的 profile 返回 typed unsupported，生产 profile fail-fast。
func (f *mcpOrchOrchestrationFacade) BindSessionGeneration(_ context.Context, _ string, _ uint64) error {
	if f == nil {
		return errors.New("app: mcp-orch orchestration facade is required for thread.bind_session_generation")
	}
	return contract.MissingDependencyModeError("thread.bind_session_generation", f.dependency.Profile)
}

// orchFacadeToolMetadata 保存 toolbridge 生命周期层需要的顶层 metadata。
type orchFacadeToolMetadata struct {
	agentID string
	cwd     string
}

// call 编码工具请求、等待 mcp-orch peer 并解码结构化结果。
func (f *mcpOrchOrchestrationFacade) call(ctx context.Context, toolName string, args any, meta orchFacadeToolMetadata, out any) error {
	if f == nil || f.tools == nil || isNilToolCaller(f.tools) {
		return errors.New("app: mcp-orch orchestration facade is not configured")
	}
	msg, err := encodeOrchFacadeToolCall(toolName, args, meta)
	if err != nil {
		return err
	}
	result, err := f.runToolCall(ctx, toolName, msg)
	if err != nil {
		return err
	}
	return decodeOrchFacadeToolResult(toolName, result, out)
}

// encodeOrchFacadeToolCall 将 facade 请求包装为 toolbridge tools/call 消息。
func encodeOrchFacadeToolCall(toolName string, args any, meta orchFacadeToolMetadata) (contract.ToolCallRawMessage, error) {
	argsRaw, err := json.Marshal(args)
	if err != nil {
		return contract.ToolCallRawMessage{}, fmt.Errorf("app: encode orchestration %s args: %w", toolName, err)
	}
	params := map[string]any{
		"name":       strings.TrimSpace(toolName),
		"arguments":  json.RawMessage(argsRaw),
		"clientKind": mcpdto.ClientKindOrch,
	}
	if agentID := strings.TrimSpace(meta.agentID); agentID != "" {
		params[toolbridge.MetadataKeyAgentID] = agentID
	}
	if cwd := strings.TrimSpace(meta.cwd); cwd != "" {
		params[toolbridge.MetadataKeyCWD] = cwd
		params[toolbridge.MetadataKeyWorkspaceRoots] = []string{cwd}
	}
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return contract.ToolCallRawMessage{}, fmt.Errorf("app: encode orchestration %s call: %w", toolName, err)
	}
	return contract.ToolCallRawMessage{
		ID:     json.RawMessage(`"thread-orch-facade"`),
		Method: toolbridge.ProxyMethodToolsCall,
		Params: json.RawMessage(paramsRaw),
	}, nil
}

// runToolCall 等待 mcp-orch peer 就绪后执行一次工具调用。
func (f *mcpOrchOrchestrationFacade) runToolCall(ctx context.Context, toolName string, msg contract.ToolCallRawMessage) (*toolbridge.ToolCallResult, error) {
	now := f.clock()
	timeout := f.peerTimeout()
	deadline := now().Add(timeout)
	for {
		result, err := f.runToolCallOnce(ctx, toolName, msg)
		if err == nil || !errors.Is(err, toolbridge.ErrNoPeerAvailable) {
			return result, err
		}
		if now().After(deadline) {
			return nil, fmt.Errorf("app: mcp-orch peer not ready for %s after %s: %w", toolName, timeout, err)
		}
		wait := f.peerPollInterval()
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

// runToolCallOnce 直接调用 toolbridge 并校验返回类型。
func (f *mcpOrchOrchestrationFacade) runToolCallOnce(ctx context.Context, toolName string, msg contract.ToolCallRawMessage) (*toolbridge.ToolCallResult, error) {
	value, err := f.tools.HandleToolCall(ctx, contract.ToolCallRawMessage{
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

// decodeOrchFacadeToolResult 校验工具调用成功并解码结构化结果。
func decodeOrchFacadeToolResult(toolName string, result *toolbridge.ToolCallResult, out any) error {
	if !result.Success {
		return fmt.Errorf("app: call mcp-orch %s failed: %s", toolName, orchFacadeResultMessage(result))
	}
	if out == nil {
		return nil
	}
	raw := result.StructuredContent
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("app: decode mcp-orch %s result: %w", toolName, err)
	}
	return nil
}

// orchFacadeResultMessage 提取工具结果中的可读消息。
func orchFacadeResultMessage(result *toolbridge.ToolCallResult) string {
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

// peerTimeout 返回等待 mcp-orch peer 的最长时间。
func (f *mcpOrchOrchestrationFacade) peerTimeout() time.Duration {
	if f != nil && f.peerReadyTimeout > 0 {
		return f.peerReadyTimeout
	}
	return defaultOrchFacadePeerReadyTimeout
}

// peerPollInterval 返回等待 peer 时的轮询间隔。
func (f *mcpOrchOrchestrationFacade) peerPollInterval() time.Duration {
	if f != nil && f.peerReadyInterval > 0 {
		return f.peerReadyInterval
	}
	return defaultOrchFacadePeerReadyPollInterval
}

// clock 返回可测试替换的当前时间函数。
func (f *mcpOrchOrchestrationFacade) clock() func() time.Time {
	if f != nil && f.now != nil {
		return f.now
	}
	return time.Now
}

// isNilToolCaller 检测 interface 持有的是否为 nil concrete 值。
// 当 *toolbridge.Handler(nil) 赋给 dagToolCaller 接口时，接口本身非 nil，
// 但底层指针为 nil；直接 == nil 检查会漏过这种情况。
func isNilToolCaller(c dagToolCaller) bool {
	if c == nil {
		return true
	}
	switch v := any(c).(type) {
	case *toolbridge.Handler:
		return v == nil
	default:
		return false
	}
}
