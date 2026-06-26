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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type stdioTransport interface {
	ReadMessage() (json.RawMessage, error)
	WriteMessage(any) error
	Close() error
}

// stdioMCPClient 管理一个 stdio MCP 子进程及其 JSON-RPC 请求序列。
type stdioMCPClient struct {
	cmd       *exec.Cmd
	guard     *stdioProcessGuard
	transport stdioTransport
	stdin     io.Closer
	mu        sync.Mutex
	nextID    int64
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

// defaultStdioClientFactory 根据 MCP binary 配置选择 HTTP 或 stdio client。
func (h *Handler) defaultStdioClientFactory(ctx context.Context, binary providerdto.MCPBinary) (mcpClient, error) {
	if strings.EqualFold(strings.TrimSpace(binary.Type), "http") || strings.TrimSpace(binary.URL) != "" {
		return newHTTPMCPClient(ctx, binary)
	}
	if len(binary.Command) == 0 || strings.TrimSpace(binary.Command[0]) == "" {
		return nil, fmt.Errorf("toolbridge: missing stdio command for %q", binary.Name)
	}
	return newStdioMCPClient(ctx, binary)
}

// newStdioMCPClient 启动 stdio MCP 子进程并完成 initialize 握手。
func newStdioMCPClient(ctx context.Context, binary providerdto.MCPBinary) (*stdioMCPClient, error) {
	cmd := exec.Command(strings.TrimSpace(binary.Command[0]), binary.Command[1:]...)
	cmd.Env = append(contract.ScrubDatabaseEnv(os.Environ()), manifestEnv(binary.Env)...)
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
		guard:     stdioAttachProcessGuard(cmd),
		transport: common.NewStdioTransport(stdout, stdin),
		stdin:     stdin,
		closed:    make(chan struct{}),
	}
	if _, err := client.request(ctx, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "super-agent-codex", "version": "dev"}}); err != nil {
		_ = client.Close()
		return nil, err
	}
	_ = client.transport.WriteMessage(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return client, nil
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

// ListTools 调用 stdio peer 的 tools/list 并解码工具列表。
func (c *stdioMCPClient) ListTools(ctx context.Context) ([]mcpdto.MCPTool, error) {
	raw, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var decoded peerToolsListResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded.Tools, nil
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
		return toolCallErrorResult(err.Error()), nil
	}
	var decoded peerToolCallResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return adaptMCPResponse(decoded)
}

// request 串行发送一个 JSON-RPC 请求并等待匹配 id 的响应。
// ctx 取消会关闭 client，避免读循环永久阻塞。
func (c *stdioMCPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	if err := c.transport.WriteMessage(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			_ = c.Close()
			return nil, ctx.Err()
		default:
		}
		raw, err := c.readMessage(ctx)
		if err != nil {
			return nil, err
		}
		if len(raw) > common.MaxStdioMessageBytes {
			return nil, fmt.Errorf("toolbridge: stdio MCP response size %d exceeds stdio message limit %d", len(raw), common.MaxStdioMessageBytes)
		}
		var resp stdioRPCResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		if resp.ID == 0 {
			continue
		}
		if resp.ID != id {
			return nil, fmt.Errorf("toolbridge: unexpected stdio MCP response id %d, want %d", resp.ID, id)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("%s", resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// stdioReadResult 把异步读消息结果传回 request。
type stdioReadResult struct {
	raw json.RawMessage
	err error
}

// readMessage 在 goroutine 中读取 stdio 消息，让 ctx 取消可以主动关闭 client。
func (c *stdioMCPClient) readMessage(ctx context.Context) (json.RawMessage, error) {
	readDone := make(chan stdioReadResult, 1)
	safego.Go(ctx, pkglogger.Get(), "toolbridge.stdioMCPClient.readMessage", func(context.Context) {
		raw, err := c.transport.ReadMessage()
		readDone <- stdioReadResult{raw: raw, err: err}
	})
	select {
	case result := <-readDone:
		return result.raw, result.err
	case <-ctx.Done():
		_ = c.Close()
		return nil, ctx.Err()
	}
}

// Close 幂等关闭 stdio MCP client、transport 和子进程。
func (c *stdioMCPClient) Close() error {
	if c == nil {
		return nil
	}
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
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.transport != nil {
		_ = c.transport.Close()
	}
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
