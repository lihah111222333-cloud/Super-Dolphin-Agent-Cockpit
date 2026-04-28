package claudecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
	"github.com/anthropic-ai/super-agent-v3/pkg/skilltool"
)

const (
	skillMCPServerName             = "skill"
	skillExpandBodyRPCMethod       = "skills/expandBody"
	skillReadResourceRPCMethod     = "skills/readResource"
	skillApprovalRequiredRPCCode   = -31002
	skillMCPDefaultProtocolVersion = "dev"
	skillMCPGeneratedCallIDPrefix  = "claude-skill-mcp"
)

type skillMCPRuntime struct {
	RPCAddr  string
	CWD      string
	AgentID  string
	ThreadID string
}

type skillHostRPCCaller interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

type skillMCPToolProvider struct {
	runtime skillMCPRuntime
	caller  skillHostRPCCaller
	now     func() time.Time
}

type expandBodyToolArgs struct {
	Name     string `json:"name"`
	Anchor   string `json:"anchor,omitempty"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

type readResourceToolArgs struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

type skillHostRPCClient struct {
	addr string
}

type skillJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type skillJSONRPCResponse struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      int                `json:"id"`
	Result  json.RawMessage    `json:"result,omitempty"`
	Error   *skillHostRPCError `json:"error,omitempty"`
}

type skillHostRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *skillHostRPCError) Error() string {
	if e == nil {
		return "host rpc error"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "host rpc error"
	}
	return message
}

func RunSkillMCPMode(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	pkglogger.InitWithConsoleWriter(os.Stderr)
	transport := common.NewStdioTransport(stdin, stdout)
	provider := newSkillMCPToolProvider(skillMCPRuntimeFromEnv(), nil)
	server := common.NewServer(skillMCPServerName, skillMCPDefaultProtocolVersion, transport, provider)
	return server.Run(ctx)
}

func newSkillMCPToolProvider(runtime skillMCPRuntime, caller skillHostRPCCaller) *skillMCPToolProvider {
	return &skillMCPToolProvider{runtime: runtime, caller: caller, now: time.Now}
}

func skillMCPRuntimeFromEnv() skillMCPRuntime {
	return skillMCPRuntime{
		RPCAddr:  firstEnv("GO_AGENT_CTL_RPC_ADDR", "RPC_ADDR"),
		CWD:      firstEnv(dto.MCPEnvSkillCWD),
		AgentID:  firstEnv(dto.MCPEnvSkillAgentID, "GO_AGENT_CTL_AGENT_ID", "GO_AGENT_MCP_AGENT_ID"),
		ThreadID: firstEnv(dto.MCPEnvSkillThreadID, "GO_AGENT_CTL_THREAD_ID", "GO_AGENT_MCP_THREAD_ID"),
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func (p *skillMCPToolProvider) ListTools(context.Context) ([]common.MCPTool, error) {
	expandSchema, _ := json.Marshal(skilltool.ExpandBodyInputSchema())
	readSchema, _ := json.Marshal(skilltool.ReadResourceInputSchema())
	return []common.MCPTool{
		{
			Name:        skilltool.ToolNameExpandBody,
			Description: skilltool.DescriptionExpandBody,
			InputSchema: expandSchema,
		},
		{
			Name:        skilltool.ToolNameReadResource,
			Description: skilltool.DescriptionReadResource,
			InputSchema: readSchema,
		},
	}, nil
}

func (p *skillMCPToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	started := time.Now()
	name = strings.TrimSpace(name)
	skillmetrics.IncSkillMCPToolCall()

	method, params, err := p.hostRPCRequest(name, args)
	if err != nil {
		skillmetrics.IncSkillMCPToolError()
		pkglogger.Warn("skill mcp: tool call rejected",
			"tool", name,
			"outcome", "invalid_request",
			"elapsed", time.Since(started),
			"error", err,
		)
		return nil, err
	}
	raw, err := p.hostRPC().Call(ctx, method, params)
	if err != nil {
		envelope := skillMCPToolErrorEnvelope(name, err)
		outcome := skillMCPEnvelopeKind(envelope)
		if outcome == "approval_required" {
			skillmetrics.IncSkillMCPApprovalRequired()
		} else {
			skillmetrics.IncSkillMCPToolError()
		}
		attrs := []any{
			"tool", name,
			"method", method,
			"outcome", outcome,
			"elapsed", time.Since(started),
		}
		if code, ok := envelope["rpc_code"]; ok {
			attrs = append(attrs, "rpc_code", code)
		}
		pkglogger.Warn("skill mcp: tool call completed", attrs...)
		return envelope, nil
	}
	skillmetrics.IncSkillMCPToolSuccess()
	pkglogger.Info("skill mcp: tool call completed",
		"tool", name,
		"method", method,
		"outcome", "success",
		"elapsed", time.Since(started),
		"result_len", len(raw),
	)
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (p *skillMCPToolProvider) hostRPC() skillHostRPCCaller {
	if p.caller != nil {
		return p.caller
	}
	return skillHostRPCClient{addr: p.runtime.RPCAddr}
}

func (p *skillMCPToolProvider) hostRPCRequest(name string, args json.RawMessage) (string, map[string]any, error) {
	switch name {
	case skilltool.ToolNameExpandBody:
		var input expandBodyToolArgs
		if err := decodeSkillToolArgs(args, &input); err != nil {
			return "", nil, err
		}
		params := p.runtimeParams(skilltool.ToolNameExpandBody)
		params["name"] = strings.TrimSpace(input.Name)
		if anchor := strings.TrimSpace(input.Anchor); anchor != "" {
			params["anchor"] = anchor
		}
		if input.MaxBytes > 0 {
			params["max_bytes"] = input.MaxBytes
		}
		return skillExpandBodyRPCMethod, params, nil
	case skilltool.ToolNameReadResource:
		var input readResourceToolArgs
		if err := decodeSkillToolArgs(args, &input); err != nil {
			return "", nil, err
		}
		params := p.runtimeParams(skilltool.ToolNameReadResource)
		params["name"] = strings.TrimSpace(input.Name)
		params["path"] = strings.TrimSpace(input.Path)
		if input.MaxBytes > 0 {
			params["max_bytes"] = input.MaxBytes
		}
		return skillReadResourceRPCMethod, params, nil
	default:
		return "", nil, fmt.Errorf("skill mcp: unknown tool %q", name)
	}
}

func decodeSkillToolArgs(raw json.RawMessage, out any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("skill mcp: decode arguments: %w", err)
	}
	return nil
}

func (p *skillMCPToolProvider) runtimeParams(tool string) map[string]any {
	params := map[string]any{
		"cwd":      strings.TrimSpace(p.runtime.CWD),
		"agentId":  strings.TrimSpace(p.runtime.AgentID),
		"threadId": strings.TrimSpace(p.runtime.ThreadID),
		"callId":   p.nextCallID(tool),
	}
	return params
}

func (p *skillMCPToolProvider) nextCallID(tool string) string {
	now := p.now
	if now == nil {
		now = time.Now
	}
	safeTool := strings.NewReplacer("_", "-", "/", "-", " ", "-").Replace(strings.TrimSpace(tool))
	return skillMCPGeneratedCallIDPrefix + "-" + safeTool + "-" + strconv.FormatInt(now().UnixNano(), 10)
}

func (c skillHostRPCClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	addr := strings.TrimSpace(c.addr)
	if addr == "" {
		return nil, errors.New("skill mcp: host rpc address is not configured")
	}
	callCtx, cancel := platformconfig.WithRPCRequestTimeout(ctx)
	defer cancel()

	conn, err := new(net.Dialer).DialContext(callCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("skill mcp: dial host rpc: %w", err)
	}
	defer conn.Close()

	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("skill mcp: encode host rpc params: %w", err)
	}
	req := skillJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  rawParams,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("skill mcp: write host rpc request: %w", err)
	}

	var resp skillJSONRPCResponse
	decoder := json.NewDecoder(bufio.NewReader(conn))
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("skill mcp: read host rpc response: %w", err)
	}
	if strings.TrimSpace(resp.JSONRPC) != "2.0" {
		return nil, fmt.Errorf("skill mcp: invalid host rpc jsonrpc version %q", resp.JSONRPC)
	}
	if resp.ID != req.ID {
		return nil, fmt.Errorf("skill mcp: host rpc response id = %d, want %d", resp.ID, req.ID)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return append(json.RawMessage(nil), resp.Result...), nil
}

func skillMCPToolErrorEnvelope(tool string, err error) map[string]any {
	envelope := map[string]any{
		"kind":  "host_tool_error",
		"tool":  strings.TrimSpace(tool),
		"error": err.Error(),
	}
	var rpcErr *skillHostRPCError
	if !errors.As(err, &rpcErr) || rpcErr == nil {
		return envelope
	}
	envelope["rpc_code"] = rpcErr.Code
	if rpcErr.Code == skillApprovalRequiredRPCCode {
		envelope["kind"] = "approval_required"
		envelope["status"] = "required"
		if approval := decodeRPCErrorData(rpcErr.Data); approval != nil {
			envelope["approval"] = approval
		}
		return envelope
	}
	if strings.Contains(strings.ToLower(rpcErr.Message), "approval denied") {
		envelope["kind"] = "approval_denied"
	}
	return envelope
}

func skillMCPEnvelopeKind(envelope map[string]any) string {
	if envelope == nil {
		return "host_tool_error"
	}
	if kind, ok := envelope["kind"].(string); ok && strings.TrimSpace(kind) != "" {
		return strings.TrimSpace(kind)
	}
	return "host_tool_error"
}

func decodeRPCErrorData(raw json.RawMessage) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
