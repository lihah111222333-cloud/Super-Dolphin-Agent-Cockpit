package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/httpegress"
)

const (
	mcpHTTPMaxResponseBytes = 10 * 1024 * 1024
	mcpHTTPAcceptHeader     = "application/json, text/event-stream"
)

var defaultMCPHTTPClient = &http.Client{Timeout: 30 * time.Second}

type mcpHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type mcpHTTPRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpHTTPRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *mcpHTTPRPCError `json:"error,omitempty"`
}

type mcpHTTPRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// requestMCPServerHTTPTools 按 MCP HTTP JSON-RPC 流程初始化远端 server 并读取 tools/list。
func requestMCPServerHTTPTools(ctx context.Context, client mcpHTTPDoer, config ServerConfig) ([]mcpdto.MCPTool, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: http client is not configured", errMCPServerToolsRequestFailed)
	}
	if _, err := sendMCPHTTPJSONRPC(ctx, client, config, "initialize", 1, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "super-agent-v3", "version": "dev"},
	}, true); err != nil {
		return nil, err
	}
	if _, err := sendMCPHTTPJSONRPC(ctx, client, config, "notifications/initialized", nil, nil, false); err != nil {
		return nil, err
	}
	raw, err := sendMCPHTTPJSONRPC(ctx, client, config, "tools/list", 2, map[string]any{}, true)
	if err != nil {
		return nil, err
	}
	return decodeMCPHTTPTools(raw)
}

// sendMCPHTTPJSONRPC 向 HTTP MCP server 发送一次 JSON-RPC 请求，并把网络、HTTP 与协议错误分层返回。
func sendMCPHTTPJSONRPC(
	ctx context.Context,
	client mcpHTTPDoer,
	config ServerConfig,
	method string,
	id any,
	params any,
	expectResponse bool,
) (json.RawMessage, error) {
	req, err := buildMCPHTTPJSONRPCRequest(ctx, config, method, id, params)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", errMCPServerToolsRequestFailed, method, err)
	}
	defer resp.Body.Close()
	raw, err := readMCPHTTPResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := rejectMCPHTTPStatus(resp.StatusCode, method, raw); err != nil {
		return nil, err
	}
	if !expectResponse && len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	return decodeMCPHTTPRPCResult(method, raw, expectResponse)
}

// buildMCPHTTPJSONRPCRequest 构造发往远端 HTTP MCP server 的 JSON-RPC 请求。
// 它在发起网络调用前校验公网可访问 URL 并套用用户配置 header，失败会归类为 tools/list 请求错误。
func buildMCPHTTPJSONRPCRequest(
	ctx context.Context,
	config ServerConfig,
	method string,
	id any,
	params any,
) (*http.Request, error) {
	payload, err := json.Marshal(mcpHTTPRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}
	targetURL, err := httpegress.ValidatePublicURL(config.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errMCPServerToolsRequestFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: build %s request: %v", errMCPServerToolsRequestFailed, method, err)
	}
	req.Header.Set("Accept", mcpHTTPAcceptHeader)
	req.Header.Set("Content-Type", "application/json")
	if err := applyMCPHTTPHeaders(req, config.Headers); err != nil {
		return nil, err
	}
	return req, nil
}

func applyMCPHTTPHeaders(req *http.Request, headers map[string]string) error {
	if err := httpegress.ValidateHeaders(headers); err != nil {
		return fmt.Errorf("%w: %v", errMCPServerToolsRequestFailed, err)
	}
	for name, value := range headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			return fmt.Errorf("%w: header is empty", errMCPServerToolsRequestFailed)
		}
		req.Header.Set(name, value)
	}
	return nil
}

func rejectMCPHTTPStatus(statusCode int, method string, _ []byte) error {
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return nil
	}
	return fmt.Errorf("%w: %s returned HTTP %d", errMCPServerToolsRequestFailed, method, statusCode)
}

func readMCPHTTPResponseBody(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, mcpHTTPMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", errMCPServerToolsRequestFailed, err)
	}
	if len(raw) > mcpHTTPMaxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", errInvalidToolsResponse, mcpHTTPMaxResponseBytes)
	}
	return raw, nil
}

func httpErrorBodySuffix(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	const limit = 512
	if len(text) > limit {
		text = text[:limit]
	}
	return ": " + text
}

// decodeMCPHTTPRPCResult 校验 JSON-RPC 响应外壳；远端 error 属于请求失败，畸形响应属于协议错误。
func decodeMCPHTTPRPCResult(method string, raw []byte, expectResponse bool) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		if expectResponse {
			return nil, fmt.Errorf("%w: %s returned empty response", errInvalidToolsResponse, method)
		}
		return nil, nil
	}
	var resp mcpHTTPRPCResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("%w: decode %s response: %v", errInvalidToolsResponse, method, err)
	}
	if strings.TrimSpace(resp.JSONRPC) != "2.0" {
		return nil, fmt.Errorf("%w: %s response jsonrpc must be 2.0", errInvalidToolsResponse, method)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%w: %s returned JSON-RPC error %d: %s", errMCPServerToolsRequestFailed, method, resp.Error.Code, strings.TrimSpace(resp.Error.Message))
	}
	if expectResponse && len(bytes.TrimSpace(resp.Result)) == 0 {
		return nil, fmt.Errorf("%w: %s response result is required", errInvalidToolsResponse, method)
	}
	return resp.Result, nil
}

// decodeMCPHTTPTools 只接受标准 tools/list 结果结构，工具缺少名称时立即报错。
func decodeMCPHTTPTools(raw json.RawMessage) ([]mcpdto.MCPTool, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%w: decode tools/list result: %v", errInvalidToolsResponse, err)
	}
	rawTools, ok := result["tools"]
	if !ok {
		return nil, fmt.Errorf("%w: tools/list result missing tools", errInvalidToolsResponse)
	}
	rawTools = bytes.TrimSpace(rawTools)
	if len(rawTools) == 0 || rawTools[0] != '[' {
		return nil, fmt.Errorf("%w: tools/list tools must be an array", errInvalidToolsResponse)
	}
	var tools []mcpdto.MCPTool
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return nil, fmt.Errorf("%w: decode tools/list tools: %v", errInvalidToolsResponse, err)
	}
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			return nil, fmt.Errorf("%w: tool name is required", errInvalidToolsResponse)
		}
	}
	return tools, nil
}
