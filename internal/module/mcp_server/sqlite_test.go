package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestStartSQLiteServerAddsDefaultNPXConfigAndEnablesIt(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	dbPath := filepath.Join(project, ".super-dolphin", "super-dolphin.db")
	writeSQLiteFixture(t, dbPath)
	svc := newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, dbPath)
	t.Chdir(project)

	got, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{})
	if err != nil {
		t.Fatalf("StartSQLiteServer() error = %v", err)
	}
	if !got.Added || got.ServerName != DefaultSQLiteServerName || !got.Enabled {
		t.Fatalf("StartSQLiteServer() = %#v, want added enabled sqlite", got)
	}
	assertStartedSQLiteServerConfig(t, store.servers[project][DefaultSQLiteServerName], dbPath)
}

func TestStopSQLiteServerDisablesDefaultConfigWithoutDeletingIt(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	dbPath := filepath.Join(project, "super-dolphin.db")
	t.Chdir(project)
	store.seed(project, DefaultSQLiteServerName, defaultSQLiteServerConfig(dbPath))
	svc := newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, dbPath)

	got, err := svc.StopSQLiteServer(context.Background(), StopSQLiteServerRequest{})
	if err != nil {
		t.Fatalf("StopSQLiteServer() error = %v", err)
	}
	if got.ServerName != DefaultSQLiteServerName || got.Enabled {
		t.Fatalf("StopSQLiteServer() = %#v, want disabled sqlite", got)
	}
	server, ok := store.servers[project][DefaultSQLiteServerName]
	if !ok {
		t.Fatalf("sqlite server deleted, want disabled row retained")
	}
	if server.Enabled == nil || *server.Enabled {
		t.Fatalf("stored sqlite enabled = %#v, want false", server.Enabled)
	}
}

func TestStartSQLiteServerResolvesDatabasePathFromSuperDolphinHome(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	home := filepath.Join(project, ".super-dolphin")
	t.Setenv(contract.SQLitePathEnvKey, "")
	t.Setenv(contract.InternalSQLitePathEnvKey, "")
	t.Setenv("SUPER_DOLPHIN_HOME", home)
	t.Chdir(project)
	svc := newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, "")

	got, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{})
	if err != nil {
		t.Fatalf("StartSQLiteServer() error = %v", err)
	}
	if !got.Added || got.ServerName != DefaultSQLiteServerName {
		t.Fatalf("StartSQLiteServer() = %#v, want added sqlite from dynamic home", got)
	}
	assertStartedSQLiteServerConfig(t, store.servers[project][DefaultSQLiteServerName], filepath.Join(home, "super-dolphin.db"))
}

func TestStartSQLiteServerMigratesLegacyNPXPackageConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{
			name: "missing npm package",
			args: []string{"-y", legacyDefaultSQLitePackage, filepath.Join("old", "missing-package.db")},
		},
		{
			name: "stdout-polluting package",
			args: []string{"-y", brokenSQLitePackage, "--db", filepath.Join("old", "polluting-package.db")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryMCPServerStore()
			project := t.TempDir()
			newDBPath := filepath.Join(project, "super-dolphin.db")
			t.Chdir(project)
			store.seed(project, DefaultSQLiteServerName, ServerConfig{
				Transport: "stdio",
				Command:   "npx",
				Args:      tc.args,
				Enabled:   boolPtr(false),
			})
			svc := newServiceWithStoreInstallerAndSQLitePath(store, &recordingPostgresInstaller{}, newDBPath)

			got, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{})
			if err != nil {
				t.Fatalf("StartSQLiteServer() error = %v", err)
			}
			if got.Added || got.ServerName != DefaultSQLiteServerName || !got.Enabled {
				t.Fatalf("StartSQLiteServer() = %#v, want migrated enabled sqlite", got)
			}
			assertStartedSQLiteServerConfig(t, store.servers[project][DefaultSQLiteServerName], newDBPath)
		})
	}
}

func TestMCPServerConfigProviderMigratesLegacySQLitePackageForChat(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		storedIndex int
		storedValue string
	}{
		{
			name:        "missing npm package",
			args:        []string{"-y", legacyDefaultSQLitePackage, ""},
			storedIndex: 1,
			storedValue: legacyDefaultSQLitePackage,
		},
		{
			name:        "stdout-polluting package",
			args:        []string{"-y", brokenSQLitePackage, "--db", ""},
			storedIndex: 1,
			storedValue: brokenSQLitePackage,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryMCPServerStore()
			svc := NewServiceWithStore(store)
			project := t.TempDir()
			dbPath := filepath.Join(project, "super-dolphin.db")
			args := append([]string(nil), tc.args...)
			args[len(args)-1] = dbPath
			store.seed(project, DefaultSQLiteServerName, ServerConfig{
				Transport: "stdio",
				Command:   "npx",
				Args:      args,
				Enabled:   boolPtr(true),
			})

			got, err := AsMCPServerConfigProvider(svc).ListMCPServerConfigs(context.Background(), project)
			if err != nil {
				t.Fatalf("ListMCPServerConfigs() error = %v", err)
			}
			assertStartedSQLiteServerConfig(t, got[DefaultSQLiteServerName], dbPath)
			if store.servers[project][DefaultSQLiteServerName].Args[tc.storedIndex] != tc.storedValue {
				t.Fatalf("provider mutated stored config: %#v", store.servers[project][DefaultSQLiteServerName])
			}
		})
	}
}

func writeSQLiteFixture(t *testing.T, dbPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir sqlite dir: %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("sqlite"), 0o600); err != nil {
		t.Fatalf("write sqlite fixture: %v", err)
	}
}

func assertStartedSQLiteServerConfig(t *testing.T, server ServerConfig, dbPath string) {
	t.Helper()
	if server.Transport != "stdio" || server.Command != "npx" {
		t.Fatalf("stored sqlite server = %#v, want stdio npx", server)
	}
	wantArgs := []string{"-y", "@bytebase/dbhub", "--dsn=" + sqliteDBHubDSN(dbPath)}
	if !slices.Equal(server.Args, wantArgs) {
		t.Fatalf("stored sqlite args = %#v, want %#v", server.Args, wantArgs)
	}
	if server.Enabled == nil || !*server.Enabled {
		t.Fatalf("stored sqlite enabled = %#v, want true", server.Enabled)
	}
}
