package mcpserver

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

func TestAddServersPersistsProjectMCPServerConfig(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)

	got, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"my-search": {
				Transport: "http",
				URL:       "https://your-domain.com/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer YOUR_API_KEY",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("AddServers() error = %v", err)
	}

	wantPath := filepath.Join(project, ".agent", "mcp_server", "config.json")
	if got.ConfigPath != wantPath {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, wantPath)
	}
	if !slices.Equal(got.ServerNames, []string{"my-search"}) {
		t.Fatalf("ServerNames = %#v, want my-search", got.ServerNames)
	}

	server, ok := store.servers[project]["my-search"]
	if !ok {
		t.Fatalf("stored servers = %#v, want my-search", store.servers)
	}
	if server.Transport != "http" {
		t.Fatalf("Transport = %q, want http", server.Transport)
	}
	if server.URL != "https://your-domain.com/mcp" {
		t.Fatalf("URL = %q", server.URL)
	}
	if server.Headers["Authorization"] != "Bearer YOUR_API_KEY" {
		t.Fatalf("Authorization header = %q", server.Headers["Authorization"])
	}
}

func TestNewServiceWritePathReturnsStoreNotConfigured(t *testing.T) {
	svc := NewService()

	_, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"my-search": {
				Transport: "http",
				URL:       "https://example.com/mcp",
			},
		},
	})
	if !errors.Is(err, errMCPServerStoreNotConfigured) {
		t.Fatalf("AddServers() error = %v, want errMCPServerStoreNotConfigured", err)
	}
}

func TestAddServersKeepsExistingTableRows(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "existing", ServerConfig{
		Transport: "http",
		URL:       "https://existing.example/mcp",
	})

	got, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"new-search": {
				Transport: "http",
				URL:       "https://new.example/mcp",
			},
		},
	})
	if err != nil {
		t.Fatalf("AddServers() error = %v", err)
	}
	if !slices.Equal(got.ServerNames, []string{"new-search"}) {
		t.Fatalf("ServerNames = %#v, want new-search", got.ServerNames)
	}

	listed, err := svc.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if _, ok := listed.MCPServers["existing"]; !ok {
		t.Fatalf("existing server missing from %#v", listed.MCPServers)
	}
	if _, ok := listed.MCPServers["new-search"]; !ok {
		t.Fatalf("new server missing from %#v", listed.MCPServers)
	}
}

func TestAddServersRejectsDuplicateServer(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://existing.example/mcp",
	})

	_, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"my-search": {
				Transport: "http",
				URL:       "https://new.example/mcp",
			},
		},
	})
	if !errors.Is(err, errServerAlreadyExists) {
		t.Fatalf("AddServers() error = %v, want errServerAlreadyExists", err)
	}
}

func TestAddServersRejectsInvalidHTTPURL(t *testing.T) {
	svc := NewServiceWithStore(newMemoryMCPServerStore())
	t.Chdir(t.TempDir())

	_, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"bad": {
				Transport: "http",
				URL:       "ftp://example.com/mcp",
			},
		},
	})
	if !errors.Is(err, errInvalidServerURL) {
		t.Fatalf("AddServers() error = %v, want errInvalidServerURL", err)
	}
}

func TestListServersReadsProjectTableRows(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer YOUR_API_KEY",
		},
	})

	got, err := svc.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if got.ConfigPath != filepath.Join(project, ".agent", "mcp_server", "config.json") {
		t.Fatalf("ConfigPath = %q", got.ConfigPath)
	}
	server, ok := got.MCPServers["my-search"]
	if !ok {
		t.Fatalf("mcpServers = %#v, want my-search", got.MCPServers)
	}
	if server.Transport != "http" {
		t.Fatalf("Transport = %q, want http", server.Transport)
	}
	if server.URL != "https://your-domain.com/mcp" {
		t.Fatalf("URL = %q", server.URL)
	}
	if server.Headers["Authorization"] != "Bearer YOUR_API_KEY" {
		t.Fatalf("Authorization header = %q", server.Headers["Authorization"])
	}
}

func TestListServersReturnsEmptyWhenProjectHasNoTableRows(t *testing.T) {
	svc := NewServiceWithStore(newMemoryMCPServerStore())
	project := t.TempDir()
	t.Chdir(project)

	got, err := svc.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if got.ConfigPath != filepath.Join(project, ".agent", "mcp_server", "config.json") {
		t.Fatalf("ConfigPath = %q", got.ConfigPath)
	}
	if len(got.MCPServers) != 0 {
		t.Fatalf("MCPServers = %#v, want empty", got.MCPServers)
	}
}

func TestListServerToolsReturnsNotFoundForMissingServer(t *testing.T) {
	svc := NewServiceWithStore(newMemoryMCPServerStore())
	t.Chdir(t.TempDir())

	_, err := svc.ListServerTools(context.Background(), ListServerToolsRequest{ServerName: "missing"})
	if !errors.Is(err, errServerNotFound) {
		t.Fatalf("ListServerTools() error = %v, want errServerNotFound", err)
	}
}

func TestListServerToolsReturnsRPCErrorFromHTTPMCPServer(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store).(*service)
	svc.httpClient = &scriptedMCPHTTPDoer{t: t, toolsListError: true}
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
	})

	_, err := svc.ListServerTools(context.Background(), ListServerToolsRequest{ServerName: "my-search"})
	if !errors.Is(err, errMCPServerToolsRequestFailed) {
		t.Fatalf("ListServerTools() error = %v, want errMCPServerToolsRequestFailed", err)
	}
}

func TestAddServersRejectsUnsafeStdioCommand(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)

	_, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"local-shell": {
				Transport: "stdio",
				Command:   "powershell.exe",
				Args:      []string{"-NoProfile", "-Command", "Get-ChildItem"},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("AddServers() error = %v, want unsupported stdio command", err)
	}
	if len(store.servers[project]) != 0 {
		t.Fatalf("stored servers = %#v, want none after rejected stdio command", store.servers[project])
	}
}

// TestAddServersRejectsRemovedPostgresCommand 锁定 SQLite-only 边界，禁止已删除的内置命令被重新保存。
func TestAddServersRejectsRemovedPostgresCommand(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)

	_, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"removed-postgres": {
				Transport: "stdio",
				Command:   "mcp-server-postgres",
				Args:      []string{"postgresql://localhost/removed"},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("AddServers() error = %v, want unsupported stdio command", err)
	}
	if len(store.servers[project]) != 0 {
		t.Fatalf("stored servers = %#v, want none after rejected removed postgres command", store.servers[project])
	}
}

func TestAddServersRejectsNPXArgvBypass(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)

	_, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"playwright-bypass": {
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"--yes", defaultPlaywrightPackage, "--config", "/tmp/attacker.json"},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("AddServers() error = %v, want unsupported stdio command", err)
	}
	if len(store.servers[project]) != 0 {
		t.Fatalf("stored servers = %#v, want none after rejected npx argv bypass", store.servers[project])
	}
}

func TestAddServersRejectsSQLiteDBHubArbitraryDSN(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	productDB := filepath.Join(project, ".super-dolphin", "super-dolphin.db")
	attackerDB := filepath.Join(t.TempDir(), "attacker.db")
	svc := newServiceWithStoreAndSQLitePath(store, productDB)
	t.Chdir(project)

	_, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"sqlite-attacker": {
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"-y", defaultSQLitePackage, "--dsn=" + sqliteDBHubDSN(attackerDB)},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("AddServers() error = %v, want unsupported stdio command", err)
	}
	if len(store.servers[project]) != 0 {
		t.Fatalf("stored servers = %#v, want none after rejected sqlite dbhub dsn", store.servers[project])
	}
}

func TestDeleteServerRemovesProjectTableRow(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})

	got, err := svc.DeleteServer(context.Background(), DeleteServerRequest{ServerName: "my-search"})
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if !got.Deleted || got.ServerName != "my-search" {
		t.Fatalf("DeleteServer() = %#v, want deleted my-search", got)
	}
	if _, ok := store.servers[project]["my-search"]; ok {
		t.Fatalf("server still stored after delete: %#v", store.servers[project])
	}
}

func TestDeleteServerReturnsNotFoundForMissingTableRow(t *testing.T) {
	svc := NewServiceWithStore(newMemoryMCPServerStore())
	t.Chdir(t.TempDir())

	_, err := svc.DeleteServer(context.Background(), DeleteServerRequest{ServerName: "missing"})
	if !errors.Is(err, errServerNotFound) {
		t.Fatalf("DeleteServer() error = %v, want errServerNotFound", err)
	}
}

func TestBackfillMCPServerToolsPreservesManualLifecycleState(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})

	manual, err := svc.SetMCPToolLifecycle(context.Background(), SetMCPToolLifecycleRequest{
		ServerName:      "my-search",
		ToolName:        "search",
		State:           contract.MCPToolLifecycleDisabled,
		Reason:          "manual review",
		ReplacementTool: "search_v2",
	})
	if err != nil {
		t.Fatalf("SetMCPToolLifecycle() error = %v", err)
	}
	if manual.State != contract.MCPToolLifecycleDisabled {
		t.Fatalf("manual lifecycle state = %q, want disabled", manual.State)
	}

	got, err := svc.BackfillMCPServerTools(context.Background(), BackfillMCPServerToolsRequest{
		ServerName: "my-search",
		Tools: []contract.MCPToolLifecycleObservedTool{{
			ManifestName: "remote-manifest",
			Name:         "search",
		}},
	})
	if err != nil {
		t.Fatalf("BackfillMCPServerTools() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("BackfillMCPServerTools() len = %d, want 1", len(got))
	}
	decision := got[0]
	if decision.State != contract.MCPToolLifecycleDisabled {
		t.Fatalf("backfilled state = %q, want disabled", decision.State)
	}
	if decision.Reason != "manual review" || decision.ReplacementTool != "search_v2" {
		t.Fatalf("backfilled decision = %#v, want manual reason/replacement preserved", decision)
	}
	if decision.ManifestName != "remote-manifest" {
		t.Fatalf("backfilled manifest = %q, want remote-manifest", decision.ManifestName)
	}
}

func TestBackfillMCPServerToolsAcceptsManagedPeerWithoutSavedConfig(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)

	got, err := svc.BackfillMCPServerTools(context.Background(), BackfillMCPServerToolsRequest{
		ServerName:   mcpdto.ClientKindLSP,
		ManifestName: mcpdto.ClientKindLSP,
		Tools: []contract.MCPToolLifecycleObservedTool{{
			Name: "grep",
		}},
	})
	if err != nil {
		t.Fatalf("BackfillMCPServerTools() error = %v", err)
	}
	if len(got) != 1 || got[0].ServerName != mcpdto.ClientKindLSP || got[0].ToolName != "grep" || got[0].State != contract.MCPToolLifecycleEnabled {
		t.Fatalf("BackfillMCPServerTools() = %#v, want enabled lsp/grep", got)
	}

	listed, err := svc.ListMCPToolLifecycle(context.Background(), ListMCPToolLifecycleRequest{ServerName: mcpdto.ClientKindLSP})
	if err != nil {
		t.Fatalf("ListMCPToolLifecycle() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ToolName != "grep" {
		t.Fatalf("ListMCPToolLifecycle() = %#v, want grep", listed)
	}
}

func TestResolveMCPToolLifecycleReturnsServerDisabledDecision(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
		Enabled:   boolPtr(false),
	})

	got, err := svc.ResolveMCPToolLifecycle(context.Background(), contract.MCPToolLifecyclePolicyRequest{
		ServerName: "my-search",
		ToolName:   "search",
	})
	if err != nil {
		t.Fatalf("ResolveMCPToolLifecycle() error = %v", err)
	}
	if !got.ServerDisabled || got.DenyCode != contract.MCPToolLifecycleDenyCodeServerDisabled {
		t.Fatalf("ResolveMCPToolLifecycle() = %#v, want server disabled denial", got)
	}
	if got.State != contract.MCPToolLifecycleDisabled {
		t.Fatalf("ResolveMCPToolLifecycle() state = %q, want disabled", got.State)
	}
}

func TestResolveMCPToolLifecycleFailsClosedWhenOwnerMissing(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})

	_, err := svc.ResolveMCPToolLifecycle(context.Background(), contract.MCPToolLifecyclePolicyRequest{
		ServerName: "my-search",
		ToolName:   "search",
	})
	if !errors.Is(err, errToolLifecycleNotFound) {
		t.Fatalf("ResolveMCPToolLifecycle() error = %v, want errToolLifecycleNotFound", err)
	}
}

func TestSetMCPToolLifecycleRejectsInvalidState(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})

	_, err := svc.SetMCPToolLifecycle(context.Background(), SetMCPToolLifecycleRequest{
		ServerName: "my-search",
		ToolName:   "search",
		State:      contract.MCPToolLifecycleState("unknown"),
	})
	if !errors.Is(err, errInvalidToolLifecycleState) {
		t.Fatalf("SetMCPToolLifecycle() error = %v, want errInvalidToolLifecycleState", err)
	}
}

func TestMCPServerConfigProviderReadsProjectTableRowsForNestedCWD(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	nested := filepath.Join(project, "pkg", "api")
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer YOUR_API_KEY",
		},
	})

	provider := AsMCPServerConfigProvider(svc)
	got, err := provider.ListMCPServerConfigs(context.Background(), nested)
	if err != nil {
		t.Fatalf("ListMCPServerConfigs() error = %v", err)
	}
	want := contract.MCPServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
		Headers:   map[string]string{"Authorization": "Bearer YOUR_API_KEY"},
	}
	if got["my-search"].Transport != want.Transport ||
		got["my-search"].URL != want.URL ||
		got["my-search"].Headers["Authorization"] != want.Headers["Authorization"] {
		t.Fatalf("ListMCPServerConfigs() = %#v, want my-search %#v", got, want)
	}
}

func TestMCPServerConfigProviderSkipsDisabledRows(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	nested := filepath.Join(project, "pkg", "api")
	store.seed(project, "enabled-search", ServerConfig{
		Transport: "http",
		URL:       "https://enabled.example/mcp",
		Enabled:   boolPtr(true),
	})
	store.seed(project, "disabled-sqlite", ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@bytebase/dbhub", "--dsn=sqlite:///" + filepath.ToSlash(filepath.Join(project, "super-dolphin.db"))},
		Enabled:   boolPtr(false),
	})

	provider := AsMCPServerConfigProvider(svc)
	got, err := provider.ListMCPServerConfigs(context.Background(), nested)
	if err != nil {
		t.Fatalf("ListMCPServerConfigs() error = %v", err)
	}
	if _, ok := got["enabled-search"]; !ok {
		t.Fatalf("enabled server missing from %#v", got)
	}
	if _, ok := got["disabled-sqlite"]; ok {
		t.Fatalf("disabled server leaked into provider configs: %#v", got)
	}
}

type memoryMCPServerStore struct {
	servers   map[string]map[string]ServerConfig
	lifecycle map[memoryMCPToolLifecycleKey]contract.MCPToolLifecycleDecision
}

type memoryMCPToolLifecycleKey struct {
	workspaceRoot string
	serverName    string
	toolName      string
}

func newMemoryMCPServerStore() *memoryMCPServerStore {
	return &memoryMCPServerStore{
		servers:   map[string]map[string]ServerConfig{},
		lifecycle: map[memoryMCPToolLifecycleKey]contract.MCPToolLifecycleDecision{},
	}
}

func (s *memoryMCPServerStore) InsertServer(_ context.Context, params StoreMCPServerConfigParams) (bool, error) {
	if s.servers == nil {
		s.servers = map[string]map[string]ServerConfig{}
	}
	if s.servers[params.WorkspaceRoot] == nil {
		s.servers[params.WorkspaceRoot] = map[string]ServerConfig{}
	}
	if _, exists := s.servers[params.WorkspaceRoot][params.Name]; exists {
		return false, nil
	}
	s.servers[params.WorkspaceRoot][params.Name] = cloneSingleMCPServerConfig(params.Config)
	return true, nil
}

func (s *memoryMCPServerStore) ListServers(_ context.Context, workspaceRoot string) (map[string]ServerConfig, error) {
	return cloneMCPServers(s.servers[workspaceRoot]), nil
}

func (s *memoryMCPServerStore) DeleteServer(_ context.Context, workspaceRoot, name string) (bool, error) {
	if s.servers[workspaceRoot] == nil {
		return false, nil
	}
	if _, exists := s.servers[workspaceRoot][name]; !exists {
		return false, nil
	}
	delete(s.servers[workspaceRoot], name)
	return true, nil
}

func (s *memoryMCPServerStore) SetServerEnabled(_ context.Context, workspaceRoot, name string, enabled bool) (bool, error) {
	if s.servers[workspaceRoot] == nil {
		return false, nil
	}
	config, exists := s.servers[workspaceRoot][name]
	if !exists {
		return false, nil
	}
	config.Enabled = boolPtr(enabled)
	s.servers[workspaceRoot][name] = config
	return true, nil
}

func (s *memoryMCPServerStore) GetToolLifecycle(
	_ context.Context,
	workspaceRoot string,
	serverName string,
	toolName string,
) (contract.MCPToolLifecycleDecision, error) {
	if s.lifecycle == nil {
		return contract.MCPToolLifecycleDecision{}, platformdb.ErrNotFound
	}
	decision, ok := s.lifecycle[memoryMCPToolLifecycleKey{workspaceRoot: workspaceRoot, serverName: serverName, toolName: toolName}]
	if !ok {
		return contract.MCPToolLifecycleDecision{}, platformdb.ErrNotFound
	}
	return cloneMCPToolLifecycleDecision(decision), nil
}

func (s *memoryMCPServerStore) ListToolLifecycle(
	_ context.Context,
	workspaceRoot string,
	serverName string,
) ([]contract.MCPToolLifecycleDecision, error) {
	out := []contract.MCPToolLifecycleDecision{}
	for key, decision := range s.lifecycle {
		if key.workspaceRoot == workspaceRoot && key.serverName == serverName {
			out = append(out, cloneMCPToolLifecycleDecision(decision))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ToolName < out[j].ToolName
	})
	return out, nil
}

func (s *memoryMCPServerStore) ExportToolLifecycle(
	_ context.Context,
	workspaceRoot string,
) ([]contract.MCPToolLifecycleDecision, error) {
	out := []contract.MCPToolLifecycleDecision{}
	for key, decision := range s.lifecycle {
		if key.workspaceRoot == workspaceRoot {
			out = append(out, cloneMCPToolLifecycleDecision(decision))
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

func (s *memoryMCPServerStore) UpsertToolLifecycle(
	_ context.Context,
	params contract.StoreMCPToolLifecycleParams,
) (contract.MCPToolLifecycleDecision, error) {
	if s.lifecycle == nil {
		s.lifecycle = map[memoryMCPToolLifecycleKey]contract.MCPToolLifecycleDecision{}
	}
	key := memoryMCPToolLifecycleKey{
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
	return cloneMCPToolLifecycleDecision(decision), nil
}

func (s *memoryMCPServerStore) BackfillToolLifecycle(
	_ context.Context,
	params contract.BackfillMCPToolLifecycleParams,
) (contract.MCPToolLifecycleDecision, error) {
	if s.lifecycle == nil {
		s.lifecycle = map[memoryMCPToolLifecycleKey]contract.MCPToolLifecycleDecision{}
	}
	key := memoryMCPToolLifecycleKey{
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
	return cloneMCPToolLifecycleDecision(decision), nil
}

func (s *memoryMCPServerStore) seed(workspaceRoot, name string, config ServerConfig) {
	if s.servers == nil {
		s.servers = map[string]map[string]ServerConfig{}
	}
	if s.servers[workspaceRoot] == nil {
		s.servers[workspaceRoot] = map[string]ServerConfig{}
	}
	s.servers[workspaceRoot][name] = cloneSingleMCPServerConfig(config)
}

func cloneSingleMCPServerConfig(config ServerConfig) ServerConfig {
	return cloneMCPServers(map[string]ServerConfig{"server": config})["server"]
}

func cloneMCPToolLifecycleDecision(decision contract.MCPToolLifecycleDecision) contract.MCPToolLifecycleDecision {
	return decision
}
