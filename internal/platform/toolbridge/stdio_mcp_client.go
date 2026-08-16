package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/mcpwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type stdioTransport interface {
	ReadMessage() (json.RawMessage, error)
	WriteMessage(any) error
	Close() error
}

const maxStdioPendingRequests = 64

var errStdioMCPClientClosed = errors.New("toolbridge: stdio MCP client closed")

// stdioMCPClient 管理一个 stdio MCP 子进程及其 JSON-RPC 请求序列。
type stdioMCPClient struct {
	cmd       *exec.Cmd
	guard     *stdioProcessGuard
	transport stdioTransport
	stdin     io.Closer

	stateMu     sync.Mutex
	writeMu     sync.Mutex
	nextID      int64
	pending     map[int64]chan stdioRequestResult
	terminalErr error
	readOnce    sync.Once

	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error
}

// stdioRPCResponse 是 stdio MCP 响应的最小 JSON-RPC 外壳。
type stdioRPCResponse struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// stdioRequestResult 把持久读泵分派的响应或终止错误传回对应请求。
type stdioRequestResult struct {
	raw json.RawMessage
	err error
}

// defaultStdioClientFactory 根据 MCP binary 配置选择 HTTP 或 stdio client。
func (h *Handler) defaultStdioClientFactory(ctx context.Context, binary providerdto.MCPBinary) (mcpClient, error) {
	if err := contract.DefaultRuntimeMCPPolicy().ValidateManifestBinary(binary); err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(binary.Type), "http") || strings.TrimSpace(binary.URL) != "" {
		return newHTTPMCPClient(ctx, binary)
	}
	return newStdioMCPClientForValidatedBinary(ctx, binary)
}

// newStdioMCPClientForValidatedBinary 启动已经过 RuntimeMCPPolicy 校验的 stdio MCP 子进程。
// 生产调用必须先走 defaultStdioClientFactory；该入口保留给同包进程生命周期测试。
func newStdioMCPClientForValidatedBinary(ctx context.Context, binary providerdto.MCPBinary) (*stdioMCPClient, error) {
	if len(binary.Command) == 0 || strings.TrimSpace(binary.Command[0]) == "" {
		return nil, fmt.Errorf("toolbridge: missing stdio command for %q", binary.Name)
	}
	cmd := exec.Command(strings.TrimSpace(binary.Command[0]), binary.Command[1:]...)
	cmd.Env = append(stdioParentEnv(os.Environ()), manifestEnv(binary.Env)...)
	stdioConfigureCommand(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	client := &stdioMCPClient{
		cmd:       cmd,
		guard:     stdioAttachProcessGuard(cmd, stdioMCPAllowsBreakaway(binary)),
		transport: mcpwire.NewStdioTransport(stdout, stdin),
		stdin:     stdin,
		pending:   make(map[int64]chan stdioRequestResult),
		closed:    make(chan struct{}),
	}
	// 握手版本与 HTTP proxy client 保持一致，统一使用 ProxyProtocolVersion，避免 stdio/HTTP 双通道版本漂移。
	raw, err := client.request(ctx, "initialize", map[string]any{"protocolVersion": ProxyProtocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "super-agent-codex", "version": "dev"}})
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	if _, err := mcpwire.DecodeInitializeProtocolVersion(raw); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("toolbridge: stdio MCP initialize: %w", err)
	}
	if err := client.writeMessage(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("toolbridge: write stdio MCP initialized notification: %w", err)
	}
	return client, nil
}

// stdioMCPAllowsBreakaway 只允许进程内受信清单签发的 LSP sidecar 派生共享 daemon。
func stdioMCPAllowsBreakaway(binary providerdto.MCPBinary) bool {
	return binary.IsManagedMCPBinary() &&
		strings.EqualFold(strings.TrimSpace(binary.Name), string(providerdto.FamilyLSP))
}

// manifestEnv 过滤 MCP binary env 中禁止透传的数据库环境变量。
func manifestEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		if contract.IsForbiddenDatabaseEnvKey(key) {
			continue
		}
		out = append(out, key+"="+value)
	}
	return out
}

// stdioParentEnv 只继承启动子进程所需的安全基础环境。
// API key、数据库连接串等敏感父进程变量必须由 manifest 显式声明，不能自动外泄给第三方 MCP。
func stdioParentEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		if !isAllowedStdioParentEnvKey(key) || contract.IsForbiddenDatabaseEnvKey(key) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func isAllowedStdioParentEnvKey(key string) bool {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "PATH", "HOME", "TMPDIR", "TMP", "TEMP", "USER", "USERNAME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT":
		return true
	default:
		return false
	}
}

// ListTools 调用 stdio peer 的 tools/list 并解码工具列表。
func (c *stdioMCPClient) ListTools(ctx context.Context) ([]mcpdto.MCPTool, error) {
	raw, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodePeerToolsListResult(raw, "stdio MCP tools/list")
}

// CallTool 调用 stdio peer 暴露的工具，并透传 agent/thread/cwd 元数据。
func (c *stdioMCPClient) CallTool(ctx context.Context, name string, args json.RawMessage, req ToolCallRequest) (*ToolCallResult, error) {
	raw, err := c.request(ctx, ProxyMethodToolsCall, map[string]any{
		"name":                    name,
		"arguments":               args,
		MetadataKeyAgentID:        req.AgentID,
		MetadataKeyThreadID:       req.ThreadID,
		MetadataKeyCallID:         req.CallID,
		MetadataKeyCWD:            req.CWD,
		MetadataKeyWorkspaceRoots: append([]string(nil), req.WorkspaceRoots...),
	})
	if err != nil {
		logToolCallFailure("stdio_mcp", err)
		return toolCallPublicErrorResult(err), nil
	}
	var decoded peerToolCallResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return adaptMCPResponse(decoded)
}

// request 注册一个有界 pending 请求，串行写入后等待持久读泵按 id 分派响应。
// ctx 取消只移除当前 pending 并通知 peer，不会关闭仍可复用的共享 transport。
func (c *stdioMCPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	id, responseCh, err := c.registerPending()
	if err != nil {
		return nil, err
	}
	c.startReadPump()

	if err := c.writeMessage(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		writeErr := fmt.Errorf("toolbridge: write stdio MCP request: %w", err)
		c.failAndClose(writeErr)
		return nil, writeErr
	}

	select {
	case result := <-responseCh:
		return result.raw, result.err
	case <-ctx.Done():
		removed, cancelErr := c.cancelPending(id, ctx.Err())
		if !removed {
			result := <-responseCh
			return result.raw, result.err
		}
		if cancelErr != nil {
			return nil, errors.Join(ctx.Err(), cancelErr)
		}
		return nil, ctx.Err()
	}
}

// registerPending 在状态锁内分配请求 id，并强制限制共享 client 的在途请求数。
func (c *stdioMCPClient) registerPending() (int64, <-chan stdioRequestResult, error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.transport == nil || c.pending == nil {
		return 0, nil, errors.New("toolbridge: stdio MCP client is not initialized")
	}
	if c.terminalErr != nil {
		return 0, nil, c.terminalErr
	}
	if len(c.pending) >= maxStdioPendingRequests {
		return 0, nil, fmt.Errorf(
			"toolbridge: stdio MCP pending request limit %d reached",
			maxStdioPendingRequests,
		)
	}
	c.nextID++
	id := c.nextID
	responseCh := make(chan stdioRequestResult, 1)
	c.pending[id] = responseCh
	return id, responseCh, nil
}

// startReadPump 确保每个 client 只有一个持久 goroutine 拥有 ReadMessage。
func (c *stdioMCPClient) startReadPump() {
	c.readOnce.Do(func() {
		safego.Go(context.Background(), pkglogger.Get(), "toolbridge.stdioMCPClient.readPump", func(context.Context) {
			c.readPump()
		})
	})
}

// readPump 持续读取 peer 消息，并把乱序响应投递给对应 pending 请求。
func (c *stdioMCPClient) readPump() {
	for {
		raw, err := c.transport.ReadMessage()
		if err != nil {
			c.failAndClose(fmt.Errorf("toolbridge: read stdio MCP response: %w", err))
			return
		}
		if len(raw) > mcpwire.MaxStdioMessageBytes {
			c.failAndClose(fmt.Errorf(
				"toolbridge: stdio MCP response size %d exceeds stdio message limit %d",
				len(raw),
				mcpwire.MaxStdioMessageBytes,
			))
			return
		}
		var resp stdioRPCResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			c.failAndClose(fmt.Errorf("toolbridge: decode stdio MCP response: %w", err))
			return
		}
		if resp.ID == 0 {
			continue
		}
		result := stdioRequestResult{raw: resp.Result}
		if resp.Error != nil {
			result.err = errors.New(resp.Error.Message)
		}
		c.completePending(resp.ID, result)
	}
}

// completePending 原子移除目标 pending；取消后迟到或未知的响应直接丢弃。
func (c *stdioMCPClient) completePending(id int64, result stdioRequestResult) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	responseCh, ok := c.pending[id]
	if !ok {
		return
	}
	delete(c.pending, id)
	responseCh <- result
}

// cancelPending 原子移除单个 pending，并在锁外串行写入 MCP 取消通知。
func (c *stdioMCPClient) cancelPending(id int64, cause error) (bool, error) {
	c.stateMu.Lock()
	if _, ok := c.pending[id]; !ok {
		c.stateMu.Unlock()
		return false, nil
	}
	delete(c.pending, id)
	c.stateMu.Unlock()

	if err := c.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params": map[string]any{
			"requestId": id,
			"reason":    cause.Error(),
		},
	}); err != nil {
		cancelErr := fmt.Errorf("toolbridge: write stdio MCP cancellation: %w", err)
		c.failAndClose(cancelErr)
		return true, cancelErr
	}
	return true, nil
}

// writeMessage 只串行化 transport 写入，不持有 pending 状态锁。
func (c *stdioMCPClient) writeMessage(payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.transport.WriteMessage(payload)
}

// failPending 记录首个终止错误，并一次性唤醒全部在途请求。
func (c *stdioMCPClient) failPending(err error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.terminalErr == nil {
		c.terminalErr = err
	}
	for id, responseCh := range c.pending {
		delete(c.pending, id)
		responseCh <- stdioRequestResult{err: c.terminalErr}
	}
}

// failAndClose 只在真实 I/O 或协议终止错误后关闭共享 client。
func (c *stdioMCPClient) failAndClose(err error) {
	c.failPending(err)
	if closeErr := c.Close(); closeErr != nil {
		pkglogger.Warn("toolbridge: close stdio MCP client after terminal error failed", "error", closeErr)
	}
}

// Close 幂等关闭 stdio MCP client、transport 和子进程。
func (c *stdioMCPClient) Close() error {
	if c == nil {
		return nil
	}
	c.failPending(errStdioMCPClientClosed)
	if c.closed == nil {
		return c.close()
	}
	c.closeOnce.Do(func() {
		c.closeErr = c.close()
		close(c.closed)
	})
	<-c.closed
	return c.closeErr
}

// close 执行实际关闭顺序：stdin、transport、等待进程，超时后终止进程树。
func (c *stdioMCPClient) close() error {
	c.writeMu.Lock()
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.transport != nil {
		_ = c.transport.Close()
	}
	c.writeMu.Unlock()
	if c.cmd == nil || c.cmd.Process == nil {
		return stdioCleanupProcessTree(c.cmd, c.guard)
	}
	done := make(chan error, 1)
	safego.Go(context.Background(), pkglogger.Get(), "toolbridge.stdioMCPClient.wait", func(context.Context) {
		done <- c.cmd.Wait()
	})
	select {
	case err := <-done:
		return errors.Join(stdioExpectedCloseWaitError(err), stdioCleanupProcessTree(c.cmd, c.guard))
	case <-time.After(2 * time.Second):
		stopErr := stdioTerminateProcessTree(c.cmd, c.guard)
		waitErr := <-done
		return errors.Join(stopErr, stdioExpectedCloseWaitError(waitErr), stdioCleanupProcessTree(c.cmd, c.guard))
	}
}
