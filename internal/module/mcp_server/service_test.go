package mcpserver

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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

type memoryMCPServerStore struct {
	servers map[string]map[string]ServerConfig
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
