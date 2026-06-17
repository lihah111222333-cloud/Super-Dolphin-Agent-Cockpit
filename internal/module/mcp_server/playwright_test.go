package mcpserver

import (
	"context"
	"slices"
	"testing"
)

func TestStartPlaywrightServerAddsDefaultNPXConfigAndEnablesIt(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	t.Chdir(project)
	svc := newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, "")

	got, err := svc.StartPlaywrightServer(context.Background(), StartPlaywrightServerRequest{})
	if err != nil {
		t.Fatalf("StartPlaywrightServer() error = %v", err)
	}
	if !got.Added || got.ServerName != DefaultPlaywrightServerName || !got.Enabled {
		t.Fatalf("StartPlaywrightServer() = %#v, want added enabled playwright", got)
	}
	assertStartedPlaywrightServerConfig(t, store.servers[project][DefaultPlaywrightServerName])
}

func TestStartPlaywrightServerReenablesExistingConfigWithoutDeletingIt(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, DefaultPlaywrightServerName, ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"@playwright/mcp@latest"},
		Enabled:   boolPtr(false),
	})
	svc := newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, "")

	got, err := svc.StartPlaywrightServer(context.Background(), StartPlaywrightServerRequest{})
	if err != nil {
		t.Fatalf("StartPlaywrightServer() error = %v", err)
	}
	if got.Added || got.ServerName != DefaultPlaywrightServerName || !got.Enabled {
		t.Fatalf("StartPlaywrightServer() = %#v, want re-enabled existing playwright", got)
	}
	assertStartedPlaywrightServerConfig(t, store.servers[project][DefaultPlaywrightServerName])
}

func TestStopPlaywrightServerDisablesDefaultConfigWithoutDeletingIt(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, DefaultPlaywrightServerName, ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"@playwright/mcp@latest"},
		Enabled:   boolPtr(true),
	})
	svc := newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, "")

	got, err := svc.StopPlaywrightServer(context.Background(), StopPlaywrightServerRequest{})
	if err != nil {
		t.Fatalf("StopPlaywrightServer() error = %v", err)
	}
	if got.ServerName != DefaultPlaywrightServerName || got.Enabled {
		t.Fatalf("StopPlaywrightServer() = %#v, want disabled playwright", got)
	}
	server, ok := store.servers[project][DefaultPlaywrightServerName]
	if !ok {
		t.Fatalf("playwright server deleted, want disabled row retained")
	}
	if server.Enabled == nil || *server.Enabled {
		t.Fatalf("stored playwright enabled = %#v, want false", server.Enabled)
	}
}

func assertStartedPlaywrightServerConfig(t *testing.T, server ServerConfig) {
	t.Helper()
	if server.Transport != "stdio" || server.Command != "npx" {
		t.Fatalf("stored playwright server = %#v, want stdio npx", server)
	}
	wantArgs := []string{"@playwright/mcp@latest"}
	if !slices.Equal(server.Args, wantArgs) {
		t.Fatalf("stored playwright args = %#v, want %#v", server.Args, wantArgs)
	}
	if server.Enabled == nil || !*server.Enabled {
		t.Fatalf("stored playwright enabled = %#v, want true", server.Enabled)
	}
}
