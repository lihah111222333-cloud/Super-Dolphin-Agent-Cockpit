package toolbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/httpegress"
)

const (
	httpMCPMaxResponseBytes       = 10 * 1024 * 1024
	httpMCPAcceptHeader           = "application/json, text/event-stream"
	httpMCPContentTypeEventStream = "text/event-stream"
	httpMCPHeaderSessionID        = "Mcp-Session-Id"
	httpMCPHeaderProtocolVersion  = "MCP-Protocol-Version"
)

var defaultHTTPMCPClient = &http.Client{Timeout: 30 * time.Second}

type httpMCPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type httpMCPClient struct {
	endpoint        string
	headers         map[string]string
	client          httpMCPDoer
	sessionID       string
	protocolVersion string
	mu              sync.Mutex
	nextID          int64
}

type httpMCPRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *httpMCPRPCError `json:"error,omitempty"`
}

type httpMCPRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// newHTTPMCPClient 创建 HTTP MCP 客户端，并先完成 initialize 握手以便后续 tools/list 和 tools/call。
func newHTTPMCPClient(ctx context.Context, binary providerdto.MCPBinary) (*httpMCPClient, error) {
	client, err := buildHTTPMCPClient(binary)
	if err != nil {
		return nil, err
	}
	raw, err := client.request(ctx, ProxyMethodInitialize, map[string]any{
		"protocolVersion": ProxyProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "super-agent-codex", "version": "dev"},
	})
	if err != nil {
		return nil, err
	}
	client.captureProtocolVersion(raw)
	if err := client.notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	return client, nil
}

// buildHTTPMCPClient 校验 HTTP MCP binary 配置；URL 或 header 异常时直接失败，避免启动半可用工具面。
func buildHTTPMCPClient(binary providerdto.MCPBinary) (*httpMCPClient, error) {
	if err := contract.DefaultRuntimeMCPPolicy().ValidateManifestBinary(binary); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(binary.Name)
	if name == "" {
		return nil, fmt.Errorf("toolbridge: HTTP MCP server name is required")
	}
	if typ := strings.TrimSpace(binary.Type); typ != "" && !strings.EqualFold(typ, "http") {
		return nil, fmt.Errorf("toolbridge: unsupported HTTP MCP transport %q for %q", typ, name)
	}
	endpoint := strings.TrimSpace(binary.URL)
	if endpoint == "" {
		return nil, fmt.Errorf("toolbridge: HTTP MCP url is required for %q", name)
	}
	client := httpMCPDoer(defaultHTTPMCPClient)
	if !contract.IsManagedRuntimeMCPServerName(name) {
		var err error
		endpoint, err = httpegress.ValidatePublicURL(endpoint)
		if err != nil {
			return nil, fmt.Errorf("toolbridge: HTTP MCP url for %q: %w", name, err)
		}
		client = httpegress.NewPublicHTTPClient(30 * time.Second)
	}
	headers, err := cloneHTTPMCPHeaders(binary.Headers)
	if err != nil {
		return nil, err
	}
	return &httpMCPClient{
		endpoint:        endpoint,
		headers:         headers,
		client:          client,
		protocolVersion: ProxyProtocolVersion,
	}, nil
}

// cloneHTTPMCPHeaders 复制并校验外部 HTTP MCP 请求头。
// 空 header 名和值、危险 header 都会阻断，避免运行时配置绕过 egress 策略。
func cloneHTTPMCPHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			return nil, fmt.Errorf("toolbridge: HTTP MCP header is empty")
		}
		out[name] = value
	}
	if err := httpegress.ValidateHeaders(out); err != nil {
		return nil, fmt.Errorf("toolbridge: HTTP MCP header: %w", err)
	}
	return out, nil
}

// ListTools 通过 HTTP MCP tools/list 拉取远端工具 schema，结果会进入 Codex thread/start dynamicTools。
func (c *httpMCPClient) ListTools(ctx context.Context) ([]mcpdto.MCPTool, error) {
	raw, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodePeerToolsListResult(raw, "HTTP MCP tools/list")
}

// CallTool 通过 HTTP MCP tools/call 调用远端工具，并透传可信的 agent/thread/cwd 元数据。
func (c *httpMCPClient) CallTool(ctx context.Context, name string, args json.RawMessage, req ToolCallRequest) (*ToolCallResult, error) {
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

// Close 保持 mcpClient 接口一致；HTTP MCP 没有本地进程或长连接需要释放。
func (c *httpMCPClient) Close() error {
	return nil
}

func (c *httpMCPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	return c.send(ctx, method, id, params, true)
}

func (c *httpMCPClient) notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.send(ctx, method, 0, params, false)
	return err
}

// send 发送一次 HTTP JSON-RPC 消息，并把 HTTP 状态、JSON-RPC error 和响应 id 都校验完。
func (c *httpMCPClient) send(ctx context.Context, method string, id int64, params any, expectResponse bool) (json.RawMessage, error) {
	req, err := c.buildRequest(ctx, method, id, params, expectResponse)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("toolbridge: HTTP MCP %s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := c.readHTTPMCPResponsePayload(resp, method, id, expectResponse)
	if err != nil {
		return nil, err
	}
	return decodeHTTPMCPResult(method, id, body, expectResponse)
}

// readHTTPMCPResponsePayload 先处理 HTTP 层错误与初始化 session，再按响应类型读取 JSON 或 SSE payload。
func (c *httpMCPClient) readHTTPMCPResponsePayload(resp *http.Response, method string, id int64, expectResponse bool) ([]byte, error) {
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := readHTTPMCPBody(resp.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, rejectHTTPMCPStatus(resp.StatusCode, method, body)
	}
	if method == ProxyMethodInitialize {
		if err := c.captureSessionID(resp.Header.Get(httpMCPHeaderSessionID)); err != nil {
			return nil, err
		}
	}
	body, err := c.readResponseBody(resp.Body, resp.Header.Get("Content-Type"), method, id, expectResponse)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// buildRequest 生成 Streamable HTTP MCP POST 请求，并在初始化后附带 session 与协议版本头。
func (c *httpMCPClient) buildRequest(ctx context.Context, method string, id int64, params any, expectResponse bool) (*http.Request, error) {
	payload, err := marshalHTTPMCPRequest(method, id, params, expectResponse)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("toolbridge: build HTTP MCP %s request: %w", method, err)
	}
	req.Header.Set("Accept", httpMCPAcceptHeader)
	req.Header.Set("Content-Type", "application/json")
	if method != ProxyMethodInitialize {
		if c.sessionID != "" {
			req.Header.Set(httpMCPHeaderSessionID, c.sessionID)
		}
		if c.protocolVersion != "" {
			req.Header.Set(httpMCPHeaderProtocolVersion, c.protocolVersion)
		}
	}
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	return req, nil
}

func marshalHTTPMCPRequest(method string, id int64, params any, expectResponse bool) ([]byte, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if expectResponse {
		payload["id"] = id
	}
	if params != nil {
		payload["params"] = params
	}
	return json.Marshal(payload)
}

func readHTTPMCPBody(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, httpMCPMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("toolbridge: read HTTP MCP response: %w", err)
	}
	if len(raw) > httpMCPMaxResponseBytes {
		return nil, fmt.Errorf("toolbridge: HTTP MCP response exceeds %d bytes", httpMCPMaxResponseBytes)
	}
	return raw, nil
}

func (c *httpMCPClient) readResponseBody(body io.Reader, contentType string, method string, id int64, expectResponse bool) ([]byte, error) {
	if isHTTPMCPEventStream(contentType) {
		return readHTTPMCPSSEBody(body, method, id, expectResponse)
	}
	return readHTTPMCPBody(body)
}

func isHTTPMCPEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	return strings.EqualFold(mediaType, httpMCPContentTypeEventStream)
}

// readHTTPMCPSSEBody 读取 Streamable HTTP 的 SSE 响应，直到拿到当前 JSON-RPC 请求对应的响应。
func readHTTPMCPSSEBody(body io.Reader, method string, id int64, expectResponse bool) ([]byte, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), httpMCPMaxResponseBytes)
	var data []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if raw, ok, err := selectHTTPMCPSSEData(method, id, data, expectResponse); ok || err != nil {
				return raw, err
			}
			data = nil
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimSpace(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("toolbridge: read HTTP MCP %s event stream: %w", method, err)
	}
	if raw, ok, err := selectHTTPMCPSSEData(method, id, data, expectResponse); ok || err != nil {
		return raw, err
	}
	if expectResponse {
		return nil, fmt.Errorf("toolbridge: HTTP MCP %s event stream ended before response", method)
	}
	return nil, nil
}

func selectHTTPMCPSSEData(method string, id int64, data []string, expectResponse bool) ([]byte, bool, error) {
	raw := strings.TrimSpace(strings.Join(data, "\n"))
	if raw == "" || !expectResponse {
		return nil, false, nil
	}
	var resp httpMCPRPCResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, false, fmt.Errorf("toolbridge: decode HTTP MCP %s event stream message: %w", method, err)
	}
	if string(bytes.TrimSpace(resp.ID)) != strconv.FormatInt(id, 10) {
		return nil, false, nil
	}
	return []byte(raw), true, nil
}

func rejectHTTPMCPStatus(statusCode int, method string, body []byte) error {
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return nil
	}
	return fmt.Errorf("toolbridge: HTTP MCP %s returned HTTP %d%s", method, statusCode, httpMCPBodySuffix(body))
}

func httpMCPBodySuffix(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	const limit = 512
	if len(text) > limit {
		text = text[:limit]
	}
	return ": " + text
}

// decodeHTTPMCPResult 校验 JSON-RPC 响应外壳；通知响应只在远端显式返回 error 时失败。
func decodeHTTPMCPResult(method string, id int64, body []byte, expectResponse bool) (json.RawMessage, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		if expectResponse {
			return nil, fmt.Errorf("toolbridge: HTTP MCP %s returned empty response", method)
		}
		return nil, nil
	}
	var resp httpMCPRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("toolbridge: decode HTTP MCP %s response: %w", method, err)
	}
	if strings.TrimSpace(resp.JSONRPC) != "2.0" {
		return nil, fmt.Errorf("toolbridge: HTTP MCP %s response jsonrpc must be 2.0", method)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("toolbridge: HTTP MCP %s JSON-RPC error %d: %s", method, resp.Error.Code, strings.TrimSpace(resp.Error.Message))
	}
	if err := validateHTTPMCPResponseID(method, id, resp.ID, expectResponse); err != nil {
		return nil, err
	}
	if expectResponse && len(bytes.TrimSpace(resp.Result)) == 0 {
		return nil, fmt.Errorf("toolbridge: HTTP MCP %s result is required", method)
	}
	return resp.Result, nil
}

func validateHTTPMCPResponseID(method string, id int64, raw json.RawMessage, expectResponse bool) error {
	if !expectResponse {
		return nil
	}
	got := string(bytes.TrimSpace(raw))
	want := strconv.FormatInt(id, 10)
	if got == "" {
		return fmt.Errorf("toolbridge: HTTP MCP %s response id is required", method)
	}
	if got != want {
		return fmt.Errorf("toolbridge: HTTP MCP %s response id %s, want %s", method, got, want)
	}
	return nil
}

func (c *httpMCPClient) captureSessionID(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	for _, ch := range sessionID {
		if ch < 0x21 || ch > 0x7e {
			return fmt.Errorf("toolbridge: HTTP MCP session id contains invalid character")
		}
	}
	c.sessionID = sessionID
	return nil
}

func (c *httpMCPClient) captureProtocolVersion(raw json.RawMessage) {
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return
	}
	if version := strings.TrimSpace(result.ProtocolVersion); version != "" {
		c.protocolVersion = version
	}
}
