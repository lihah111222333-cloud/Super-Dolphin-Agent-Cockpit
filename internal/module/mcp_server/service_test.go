package mcpserver

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

func TestStartPostgresServerAddsDefaultStdioConfigOnExplicitCall(t *testing.T) {
	store := newMemoryMCPServerStore()
	installer := &recordingPostgresInstaller{}
	svc := newServiceWithStoreAndInstaller(store, installer)
	project := t.TempDir()
	t.Chdir(project)

	got, err := svc.StartPostgresServer(context.Background(), StartPostgresServerRequest{})
	if err != nil {
		t.Fatalf("StartPostgresServer() error = %v", err)
	}
	if !got.Added || got.ServerName != DefaultPostgresServerName {
		t.Fatalf("StartPostgresServer() = %#v, want added postgres", got)
	}
	if installer.calls != 1 {
		t.Fatalf("postgres installer calls = %d, want 1", installer.calls)
	}
	server := store.servers[project][DefaultPostgresServerName]
	if server.Transport != "stdio" || server.Command != "mcp-server-postgres" {
		t.Fatalf("stored postgres server = %#v, want stdio mcp-server-postgres", server)
	}
	if len(server.Args) != 1 || server.Args[0] != "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable" {
		t.Fatalf("stored postgres args = %#v, want postgres database url", server.Args)
	}
}

func TestStartPostgresServerDoesNotOverrideExistingConfig(t *testing.T) {
	store := newMemoryMCPServerStore()
	installer := &recordingPostgresInstaller{}
	svc := newServiceWithStoreAndInstaller(store, installer)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, DefaultPostgresServerName, ServerConfig{
		Transport: "stdio",
		Command:   "custom-postgres-mcp",
	})

	got, err := svc.StartPostgresServer(context.Background(), StartPostgresServerRequest{})
	if err != nil {
		t.Fatalf("StartPostgresServer() error = %v", err)
	}
	if got.Added {
		t.Fatalf("StartPostgresServer() Added = true, want false for existing config")
	}
	if got.Config.Command != "custom-postgres-mcp" {
		t.Fatalf("Config = %#v, want existing custom command", got.Config)
	}
	if installer.calls != 0 {
		t.Fatalf("postgres installer calls = %d, want 0 for existing config", installer.calls)
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
	svc := newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, productDB)
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

func TestStartPostgresServerMigratesLegacyDefaultNPXConfig(t *testing.T) {
	store := newMemoryMCPServerStore()
	installer := &recordingPostgresInstaller{}
	svc := newServiceWithStoreAndInstaller(store, installer)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, DefaultPostgresServerName, ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args: []string{
			"-y",
			"@modelcontextprotocol/server-postgres",
			"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
		},
	})

	got, err := svc.StartPostgresServer(context.Background(), StartPostgresServerRequest{})
	if err != nil {
		t.Fatalf("StartPostgresServer() error = %v", err)
	}
	if got.Added {
		t.Fatalf("StartPostgresServer() Added = true, want false for migrated config")
	}
	if installer.calls != 1 {
		t.Fatalf("postgres installer calls = %d, want 1", installer.calls)
	}
	server := store.servers[project][DefaultPostgresServerName]
	if server.Command != "mcp-server-postgres" || len(server.Args) != 1 {
		t.Fatalf("migrated postgres server = %#v, want direct command", server)
	}
}

func TestStartPostgresServerReturnsInstallerErrorBeforeWritingConfig(t *testing.T) {
	store := newMemoryMCPServerStore()
	installer := &recordingPostgresInstaller{err: errors.New("npm unavailable")}
	svc := newServiceWithStoreAndInstaller(store, installer)
	project := t.TempDir()
	t.Chdir(project)

	_, err := svc.StartPostgresServer(context.Background(), StartPostgresServerRequest{})
	if err == nil || err.Error() != "npm unavailable" {
		t.Fatalf("StartPostgresServer() error = %v, want installer error", err)
	}
	if installer.calls != 1 {
		t.Fatalf("postgres installer calls = %d, want 1", installer.calls)
	}
	if len(store.servers[project]) != 0 {
		t.Fatalf("stored servers = %#v, want none after installer failure", store.servers[project])
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
	servers map[string]map[string]ServerConfig
}

type recordingPostgresInstaller struct {
	calls int
	err   error
}

func (i *recordingPostgresInstaller) EnsureInstalled(context.Context) error {
	i.calls++
	return i.err
}

func newMemoryMCPServerStore() *memoryMCPServerStore {
	return &memoryMCPServerStore{servers: map[string]map[string]ServerConfig{}}
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
