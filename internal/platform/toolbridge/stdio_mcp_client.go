package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type stdioTransport interface {
	ReadMessage() (json.RawMessage, error)
	WriteMessage(any) error
}

type stdioMCPClient struct {
	cmd       *exec.Cmd
	transport stdioTransport
	stdin     io.Closer
	mu        sync.Mutex
	nextID    int64
}

type stdioRPCResponse struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (h *Handler) defaultStdioClientFactory(ctx context.Context, binary providerdto.MCPBinary) (mcpClient, error) {
	if strings.TrimSpace(binary.Type) == "http" || strings.TrimSpace(binary.URL) != "" {
		return nil, fmt.Errorf("toolbridge: codex surface requires stdio MCP for %q", binary.Name)
	}
	if len(binary.Command) == 0 || strings.TrimSpace(binary.Command[0]) == "" {
		return nil, fmt.Errorf("toolbridge: missing stdio command for %q", binary.Name)
	}
	return newStdioMCPClient(ctx, binary)
}

func newStdioMCPClient(ctx context.Context, binary providerdto.MCPBinary) (*stdioMCPClient, error) {
	cmd := exec.Command(strings.TrimSpace(binary.Command[0]), binary.Command[1:]...)
	cmd.Env = append(os.Environ(), manifestEnv(binary.Env)...)
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
	client := &stdioMCPClient{cmd: cmd, transport: common.NewStdioTransport(stdout, stdin), stdin: stdin}
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
		out = append(out, key+"="+value)
	}
	return out
}

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
			return nil, ctx.Err()
		default:
		}
		raw, err := c.transport.ReadMessage()
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

func (c *stdioMCPClient) Close() error {
	if c == nil {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	done := make(chan error, 1)
	safego.Go(context.Background(), pkglogger.Get(), "toolbridge.stdioMCPClient.wait", func(context.Context) {
		done <- c.cmd.Wait()
	})
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return <-done
	}
}
