package wstest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/datasource"
	mcpserver "github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/gorilla/websocket"
)

func TestWailsWebSocketRequestsMCPServerAndDatasourceRPC(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	writeDatasourceFiles(t, project, "b.txt", "a.pdf")

	conn := dialWailsWebSocket(t, newWailsWSServer(t))
	defer conn.Close()

	var files datasource.ListFilesResult
	callWailsRPC(t, conn, 1, "datasource/list", map[string]any{}, &files)
	if want := []string{"a.pdf", "b.txt"}; !slices.Equal(files.FileNames, want) {
		t.Fatalf("datasource/list fileNames = %#v, want %#v", files.FileNames, want)
	}

	addReq := mcpserver.AddServersRequest{
		MCPServers: map[string]mcpserver.ServerConfig{
			"my-search": {
				Transport: "http",
				URL:       "https://your-domain.com/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer YOUR_API_KEY",
				},
			},
		},
	}
	var addResult mcpserver.AddServersResult
	callWailsRPC(t, conn, 2, "mcpServer/add", addReq, &addResult)
	wantConfigPath := filepath.Join(project, ".agent", "mcp_server", "config.json")
	if addResult.ConfigPath != wantConfigPath {
		t.Fatalf("mcpServer/add configPath = %q, want %q", addResult.ConfigPath, wantConfigPath)
	}
	if !slices.Equal(addResult.ServerNames, []string{"my-search"}) {
		t.Fatalf("mcpServer/add serverNames = %#v, want my-search", addResult.ServerNames)
	}

	var listResult mcpserver.ListServersResult
	callWailsRPC(t, conn, 3, "mcpServer/list", map[string]any{}, &listResult)
	gotServer, ok := listResult.MCPServers["my-search"]
	if !ok {
		t.Fatalf("mcpServer/list missing my-search: %#v", listResult.MCPServers)
	}
	if gotServer.Transport != "http" || gotServer.URL != "https://your-domain.com/mcp" {
		t.Fatalf("mcpServer/list my-search = %#v", gotServer)
	}
	if gotServer.Headers["Authorization"] != "Bearer YOUR_API_KEY" {
		t.Fatalf("mcpServer/list authorization header = %q", gotServer.Headers["Authorization"])
	}
}

func TestWailsWebSocketUploadsDatasourceFromAbsolutePath(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	sourceBytes := []byte("websocket datasource upload")
	sourcePath, sourceInfo := writeDatasourceUploadSource(t, project, "测试.txt", sourceBytes)

	conn := dialWailsWebSocket(t, newWailsWSServer(t))
	defer conn.Close()

	var uploadResult datasource.UploadFileResult
	callWailsRPC(t, conn, 1, "datasource/upload", datasource.UploadFileRequest{
		SourcePath: sourcePath,
	}, &uploadResult)

	wantStoredPath := filepath.Join(project, ".agent", "datasources", "uploads", "测试.txt")
	if uploadResult.Name != "测试.txt" {
		t.Fatalf("datasource/upload name = %q, want 测试.txt", uploadResult.Name)
	}
	if uploadResult.Extension != ".txt" {
		t.Fatalf("datasource/upload extension = %q, want .txt", uploadResult.Extension)
	}
	if uploadResult.Size != sourceInfo.Size() {
		t.Fatalf("datasource/upload size = %d, want %d", uploadResult.Size, sourceInfo.Size())
	}
	if uploadResult.StoredPath != wantStoredPath {
		t.Fatalf("datasource/upload storedPath = %q, want %q", uploadResult.StoredPath, wantStoredPath)
	}

	storedBytes, err := os.ReadFile(wantStoredPath)
	if err != nil {
		t.Fatalf("read uploaded datasource file: %v", err)
	}
	if !slices.Equal(storedBytes, sourceBytes) {
		t.Fatalf("uploaded datasource bytes differ from source")
	}
}

func writeDatasourceUploadSource(t *testing.T, project, name string, contents []byte) (string, os.FileInfo) {
	t.Helper()
	sourceDir := filepath.Join(project, "fixtures")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("create datasource source fixture dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, name)
	if err := os.WriteFile(sourcePath, contents, 0o600); err != nil {
		t.Fatalf("write datasource upload fixture: %v", err)
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatalf("stat datasource upload fixture %q: %v", sourcePath, err)
	}
	if !sourceInfo.Mode().IsRegular() {
		t.Fatalf("datasource upload fixture %q is not a regular file", sourcePath)
	}
	return sourcePath, sourceInfo
}

func newWailsWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	mcpStore := newWSMCPServerStore()

	rpcServer := platformrpc.NewServer(platformrpc.Params{
		Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"},
	})
	rpcServer.Register(
		datasource.NewHandlers(datasource.NewService()).Handlers,
		mcpserver.NewHandlers(mcpserver.NewServiceWithStore(mcpStore)).Handlers,
	)

	mux := http.NewServeMux()
	mux.Handle("/wails/ws", platformrpc.WSHandler(rpcServer, nil))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return httpServer
}

func dialWailsWebSocket(t *testing.T, httpServer *httptest.Server) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/wails/ws"
	dialer := websocket.Dialer{EnableCompression: true}
	conn, resp, err := dialer.Dial(wsURL, wailsBrowserHeaders(httpServer.URL))
	if err != nil {
		t.Fatalf("dial %s: %v%s", wsURL, err, websocketResponseBody(t, resp))
	}
	return conn
}

func wailsBrowserHeaders(origin string) http.Header {
	const edgeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36 Edg/149.0.0.0"

	headers := http.Header{}
	headers.Set("Origin", origin)
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6")
	headers.Set("Pragma", "no-cache")
	headers.Set("User-Agent", edgeUserAgent)
	return headers
}

func callWailsRPC(t *testing.T, conn *websocket.Conn, id int, method string, params any, out any) {
	t.Helper()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("write %s request: %v", method, err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var resp wailsRPCResponse
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read %s response: %v", method, err)
	}
	if resp.Error != nil {
		t.Fatalf("%s returned JSON-RPC error %d: %s", method, resp.Error.Code, resp.Error.Message)
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("%s jsonrpc = %q, want 2.0", method, resp.JSONRPC)
	}
	var gotID int
	if err := json.Unmarshal(resp.ID, &gotID); err != nil || gotID != id {
		t.Fatalf("%s id = %s, want %d", method, resp.ID, id)
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		t.Fatalf("decode %s result %s: %v", method, resp.Result, err)
	}
}

func writeDatasourceFiles(t *testing.T, project string, names ...string) {
	t.Helper()

	uploadDir := filepath.Join(project, ".agent", "datasources", "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create datasource upload dir: %v", err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(uploadDir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write datasource file %s: %v", name, err)
		}
	}
}

type wsMCPServerStore struct {
	servers   map[string]map[string]mcpserver.ServerConfig
	lifecycle map[wsMCPToolLifecycleKey]contract.MCPToolLifecycleDecision
}

type wsMCPToolLifecycleKey struct {
	workspaceRoot string
	serverName    string
	toolName      string
}

var _ mcpserver.MCPServerConfigStore = (*wsMCPServerStore)(nil)

func newWSMCPServerStore() *wsMCPServerStore {
	return &wsMCPServerStore{
		servers:   map[string]map[string]mcpserver.ServerConfig{},
		lifecycle: map[wsMCPToolLifecycleKey]contract.MCPToolLifecycleDecision{},
	}
}

func (s *wsMCPServerStore) InsertServer(_ context.Context, params mcpserver.StoreMCPServerConfigParams) (bool, error) {
	if s.servers[params.WorkspaceRoot] == nil {
		s.servers[params.WorkspaceRoot] = map[string]mcpserver.ServerConfig{}
	}
	if _, exists := s.servers[params.WorkspaceRoot][params.Name]; exists {
		return false, nil
	}
	s.servers[params.WorkspaceRoot][params.Name] = cloneWSMCPServerConfig(params.Config)
	return true, nil
}

func (s *wsMCPServerStore) ListServers(_ context.Context, workspaceRoot string) (map[string]mcpserver.ServerConfig, error) {
	return cloneWSMCPServers(s.servers[workspaceRoot]), nil
}

func (s *wsMCPServerStore) DeleteServer(_ context.Context, workspaceRoot, name string) (bool, error) {
	if s.servers[workspaceRoot] == nil {
		return false, nil
	}
	if _, exists := s.servers[workspaceRoot][name]; !exists {
		return false, nil
	}
	delete(s.servers[workspaceRoot], name)
	return true, nil
}

func (s *wsMCPServerStore) SetServerEnabled(_ context.Context, workspaceRoot, name string, enabled bool) (bool, error) {
	if s.servers[workspaceRoot] == nil {
		return false, nil
	}
	config, exists := s.servers[workspaceRoot][name]
	if !exists {
		return false, nil
	}
	config.Enabled = &enabled
	s.servers[workspaceRoot][name] = config
	return true, nil
}

func (s *wsMCPServerStore) GetToolLifecycle(
	_ context.Context,
	workspaceRoot string,
	serverName string,
	toolName string,
) (contract.MCPToolLifecycleDecision, error) {
	decision, ok := s.lifecycle[wsMCPToolLifecycleKey{workspaceRoot: workspaceRoot, serverName: serverName, toolName: toolName}]
	if !ok {
		return contract.MCPToolLifecycleDecision{}, platformdb.ErrNotFound
	}
	return cloneWSMCPToolLifecycleDecision(decision), nil
}

func (s *wsMCPServerStore) ListToolLifecycle(
	_ context.Context,
	workspaceRoot string,
	serverName string,
) ([]contract.MCPToolLifecycleDecision, error) {
	out := []contract.MCPToolLifecycleDecision{}
	for key, decision := range s.lifecycle {
		if key.workspaceRoot == workspaceRoot && key.serverName == serverName {
			out = append(out, cloneWSMCPToolLifecycleDecision(decision))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ToolName < out[j].ToolName
	})
	return out, nil
}

func (s *wsMCPServerStore) ExportToolLifecycle(
	_ context.Context,
	workspaceRoot string,
) ([]contract.MCPToolLifecycleDecision, error) {
	out := []contract.MCPToolLifecycleDecision{}
	for key, decision := range s.lifecycle {
		if key.workspaceRoot == workspaceRoot {
			out = append(out, cloneWSMCPToolLifecycleDecision(decision))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServerName == out[j].ServerName {
			return out[i].ToolName < out[j].ToolName
		}
		return out[i].ServerName < out[j].ServerName
	})
	return out, nil
}

func (s *wsMCPServerStore) UpsertToolLifecycle(
	_ context.Context,
	params contract.StoreMCPToolLifecycleParams,
) (contract.MCPToolLifecycleDecision, error) {
	if s.lifecycle == nil {
		s.lifecycle = map[wsMCPToolLifecycleKey]contract.MCPToolLifecycleDecision{}
	}
	key := wsMCPToolLifecycleKey{
		workspaceRoot: params.WorkspaceRoot,
		serverName:    params.ServerName,
		toolName:      params.ToolName,
	}
	createdAt := params.NowMillis
	if existing, ok := s.lifecycle[key]; ok {
		createdAt = existing.CreatedAt
	}
	decision := contract.MCPToolLifecycleDecision{
		WorkspaceRoot:   params.WorkspaceRoot,
		ServerName:      params.ServerName,
		ManifestName:    params.ManifestName,
		ToolName:        params.ToolName,
		State:           params.State,
		Reason:          params.Reason,
		ReplacementTool: params.ReplacementTool,
		LastSeenAt:      params.NowMillis,
		CreatedAt:       createdAt,
		UpdatedAt:       params.NowMillis,
	}
	s.lifecycle[key] = decision
	return cloneWSMCPToolLifecycleDecision(decision), nil
}

func (s *wsMCPServerStore) BackfillToolLifecycle(
	_ context.Context,
	params contract.BackfillMCPToolLifecycleParams,
) (contract.MCPToolLifecycleDecision, error) {
	if s.lifecycle == nil {
		s.lifecycle = map[wsMCPToolLifecycleKey]contract.MCPToolLifecycleDecision{}
	}
	key := wsMCPToolLifecycleKey{
		workspaceRoot: params.WorkspaceRoot,
		serverName:    params.ServerName,
		toolName:      params.ToolName,
	}
	decision, ok := s.lifecycle[key]
	if !ok {
		decision = contract.MCPToolLifecycleDecision{
			WorkspaceRoot: params.WorkspaceRoot,
			ServerName:    params.ServerName,
			ManifestName:  params.ManifestName,
			ToolName:      params.ToolName,
			State:         contract.MCPToolLifecycleEnabled,
			CreatedAt:     params.NowMillis,
		}
	} else if params.ManifestName != "" {
		decision.ManifestName = params.ManifestName
	}
	decision.LastSeenAt = params.NowMillis
	decision.UpdatedAt = params.NowMillis
	s.lifecycle[key] = decision
	return cloneWSMCPToolLifecycleDecision(decision), nil
}

func cloneWSMCPServers(input map[string]mcpserver.ServerConfig) map[string]mcpserver.ServerConfig {
	out := make(map[string]mcpserver.ServerConfig, len(input))
	for name, config := range input {
		out[name] = cloneWSMCPServerConfig(config)
	}
	return out
}

func cloneWSMCPServerConfig(config mcpserver.ServerConfig) mcpserver.ServerConfig {
	headers := make(map[string]string, len(config.Headers))
	for name, value := range config.Headers {
		headers[name] = value
	}
	if len(headers) == 0 {
		headers = nil
	}
	config.Headers = headers
	return config
}

func cloneWSMCPToolLifecycleDecision(decision contract.MCPToolLifecycleDecision) contract.MCPToolLifecycleDecision {
	return decision
}

func websocketResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	if resp == nil || resp.Body == nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || len(body) == 0 {
		return ""
	}
	return ": " + strings.TrimSpace(string(body))
}

type wailsRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
