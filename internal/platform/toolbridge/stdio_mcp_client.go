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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpwire"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type stdioTransport interface {
	ReadMessage() (json.RawMessage, error)
	WriteMessage(any) error
	Close() error
}

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

type stdioRPCResponse struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (h *Handler) defaultStdioClientFactory(ctx context.Context, binary providerdto.MCPBinary) (mcpClient, error) {
	if strings.EqualFold(strings.TrimSpace(binary.Type), "http") || strings.TrimSpace(binary.URL) != "" {
		return newHTTPMCPClient(ctx, binary)
	}
	if len(binary.Command) == 0 || strings.TrimSpace(binary.Command[0]) == "" {
		return nil, fmt.Errorf("toolbridge: missing stdio command for %q", binary.Name)
	}
	return newStdioMCPClient(ctx, binary)
}

// newStdioMCPClient 创建stdioMCP客户端。
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
		transport: mcpwire.NewStdioTransport(stdout, stdin),
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

// ListTools 返回当前 peer 暴露的工具列表。
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

// CallTool 调用当前 peer 暴露的工具。
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
		return toolCallTextResult(false, err.Error()), err
	}
	var decoded peerToolCallResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return adaptMCPResponse(decoded)
}

// request 处理请求。
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

type stdioReadResult struct {
	raw json.RawMessage
	err error
}

func (c *stdioMCPClient) readMessage(ctx context.Context) (json.RawMessage, error) {
	readDone := make(chan stdioReadResult, 1)
	kernel.SafeGoContext(ctx, pkglogger.Get(), "toolbridge.stdioMCPClient.readMessage", func(context.Context) {
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

// Close 关闭平台toolbridge资源。
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

// close 关闭平台toolbridge。
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
	kernel.SafeGoContext(context.Background(), pkglogger.Get(), "toolbridge.stdioMCPClient.wait", func(context.Context) {
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
