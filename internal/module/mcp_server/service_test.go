package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAddServersWritesProjectAgentConfig(t *testing.T) {
	svc := NewService()
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

	doc := readConfigDocument(t, wantPath)
	server, ok := doc.MCPServers["my-search"]
	if !ok {
		t.Fatalf("mcpServers = %#v, want my-search", doc.MCPServers)
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

func TestAddServersMergesWithExistingConfig(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	configPath := filepath.Join(project, ".agent", "mcp_server", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	existing := []byte(`{
  "mcpServers": {
    "existing": {
      "transport": "http",
      "url": "https://existing.example/mcp"
    }
  }
}`)
	if err := os.WriteFile(configPath, existing, 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

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

	doc := readConfigDocument(t, configPath)
	if _, ok := doc.MCPServers["existing"]; !ok {
		t.Fatalf("existing server missing from %#v", doc.MCPServers)
	}
	if _, ok := doc.MCPServers["new-search"]; !ok {
		t.Fatalf("new server missing from %#v", doc.MCPServers)
	}
}

func TestAddServersRejectsDuplicateServer(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	configPath := filepath.Join(project, ".agent", "mcp_server", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	existing := []byte(`{
  "mcpServers": {
    "my-search": {
      "transport": "http",
      "url": "https://existing.example/mcp"
    }
  }
}`)
	if err := os.WriteFile(configPath, existing, 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

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
	svc := NewService()
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

func TestListServersReadsProjectAgentConfig(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	configPath := filepath.Join(project, ".agent", "mcp_server", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	existing := []byte(`{
  "mcpServers": {
    "my-search": {
      "transport": "http",
      "url": "https://your-domain.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_API_KEY"
      }
    }
  }
}`)
	if err := os.WriteFile(configPath, existing, 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	got, err := svc.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if got.ConfigPath != configPath {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, configPath)
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

func TestListServersReturnsEmptyWhenConfigDoesNotExist(t *testing.T) {
	svc := NewService()
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

func readConfigDocument(t *testing.T, path string) ConfigDocument {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var doc ConfigDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return doc
}
