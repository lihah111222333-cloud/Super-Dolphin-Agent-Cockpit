package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestStartSQLiteServerAddsDefaultNPXConfigAndEnablesIt(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	dbPath := filepath.Join(project, ".super-dolphin", "super-dolphin.db")
	writeSQLiteFixture(t, dbPath)
	svc := newServiceWithStoreAndSQLitePath(store, dbPath)
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

func TestStartSQLiteServerRejectsRequestDatabasePathOverride(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	productDB := filepath.Join(project, ".super-dolphin", "super-dolphin.db")
	attackerDB := filepath.Join(t.TempDir(), "attacker.db")
	writeSQLiteFixture(t, productDB)
	writeSQLiteFixture(t, attackerDB)
	svc := newServiceWithStoreAndSQLitePath(store, productDB)
	t.Chdir(project)

	_, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{DatabasePath: attackerDB})
	if err == nil || !strings.Contains(err.Error(), "databasePath") {
		t.Fatalf("StartSQLiteServer() error = %v, want request databasePath rejection", err)
	}
	if len(store.servers[project]) != 0 {
		t.Fatalf("stored servers = %#v, want no sqlite config after rejected override", store.servers[project])
	}
}

func TestStopSQLiteServerDisablesDefaultConfigWithoutDeletingIt(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	dbPath := filepath.Join(project, "super-dolphin.db")
	t.Chdir(project)
	store.seed(project, DefaultSQLiteServerName, defaultSQLiteServerConfig(dbPath))
	svc := newServiceWithStoreAndSQLitePath(store, dbPath)

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
	svc := newServiceWithStoreAndSQLitePath(store, "")

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
			svc := newServiceWithStoreAndSQLitePath(store, newDBPath)

			got, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{})
			if err != nil {
				t.Fatalf("StartSQLiteServer() error = %v", err)
			}
			if got.Added || got.ServerName != DefaultSQLiteServerName || !got.Enabled {
				t.Fatalf("StartSQLiteServer() = %#v, want migrated enabled sqlite", got)
			}
			assertStartedSQLiteServerConfig(t, store.servers[project][DefaultSQLiteServerName], newDBPath)
			if svc.configRevision != 1 {
				t.Fatalf("config revision = %d, want 1 after successful replacement", svc.configRevision)
			}
		})
	}
}

func TestStartSQLiteServerMigratesExactUnpinnedDBHubDefault(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	dbPath := filepath.Join(project, "super-dolphin.db")
	t.Chdir(project)
	store.seed(project, DefaultSQLiteServerName, ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@bytebase/dbhub", "--dsn=" + sqliteDBHubDSN(dbPath)},
		Enabled:   boolPtr(false),
	})
	svc := newServiceWithStoreAndSQLitePath(store, dbPath)

	got, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{})
	if err != nil {
		t.Fatalf("StartSQLiteServer() error = %v", err)
	}
	if got.Added || !got.Enabled {
		t.Fatalf("StartSQLiteServer() = %#v, want migrated enabled sqlite", got)
	}
	assertStartedSQLiteServerConfig(t, store.servers[project][DefaultSQLiteServerName], dbPath)
	if svc.configRevision != 1 {
		t.Fatalf("config revision = %d, want 1 after atomic replacement", svc.configRevision)
	}
}

func TestStartSQLiteServerDoesNotMigrateCustomizedDBHubConfig(t *testing.T) {
	project := t.TempDir()
	dbPath := filepath.Join(project, "super-dolphin.db")
	otherDBPath := filepath.Join(project, "other.db")
	tests := []struct {
		name   string
		config ServerConfig
	}{
		{name: "different dsn", config: ServerConfig{Transport: "stdio", Command: "npx", Args: []string{"-y", "@bytebase/dbhub", "--dsn=" + sqliteDBHubDSN(otherDBPath)}}},
		{name: "extra env", config: ServerConfig{Transport: "stdio", Command: "npx", Args: []string{"-y", "@bytebase/dbhub", "--dsn=" + sqliteDBHubDSN(dbPath)}, Env: map[string]string{"CUSTOM": "1"}}},
		{name: "extra arg", config: ServerConfig{Transport: "stdio", Command: "npx", Args: []string{"-y", "@bytebase/dbhub", "--dsn=" + sqliteDBHubDSN(dbPath), "--custom"}}},
		{name: "latest", config: ServerConfig{Transport: "stdio", Command: "npx", Args: []string{"-y", "@bytebase/dbhub@latest", "--dsn=" + sqliteDBHubDSN(dbPath)}}},
		{name: "other version", config: ServerConfig{Transport: "stdio", Command: "npx", Args: []string{"-y", "@bytebase/dbhub@0.22.0", "--dsn=" + sqliteDBHubDSN(dbPath)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryMCPServerStore()
			original := tc.config
			original.Enabled = boolPtr(false)
			store.seed(project, DefaultSQLiteServerName, original)
			svc := newServiceWithStoreAndSQLitePath(store, dbPath)
			t.Chdir(project)

			got, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{})
			if err != nil {
				t.Fatalf("StartSQLiteServer() error = %v", err)
			}
			stored := store.servers[project][DefaultSQLiteServerName]
			if !slices.Equal(stored.Args, original.Args) || !reflect.DeepEqual(stored.Env, original.Env) {
				t.Fatalf("custom config migrated: got %#v, want args/env from %#v", stored, original)
			}
			if got.Config.Args[1] != original.Args[1] {
				t.Fatalf("returned custom package = %q, want %q", got.Config.Args[1], original.Args[1])
			}
		})
	}
}

func TestMCPServerConfigProviderAtomicallyMigratesExactUnpinnedDBHubDefault(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	dbPath := filepath.Join(project, "super-dolphin.db")
	store.seed(project, DefaultSQLiteServerName, ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@bytebase/dbhub", "--dsn=" + sqliteDBHubDSN(dbPath)},
		Enabled:   boolPtr(true),
	})
	svc := newServiceWithStoreAndSQLitePath(store, dbPath)

	got, err := AsMCPServerConfigProvider(svc).ListMCPServerConfigs(context.Background(), project)
	if err != nil {
		t.Fatalf("ListMCPServerConfigs() error = %v", err)
	}
	assertStartedSQLiteServerConfig(t, got[DefaultSQLiteServerName], dbPath)
	assertStartedSQLiteServerConfig(t, store.servers[project][DefaultSQLiteServerName], dbPath)
	if svc.configRevision != 1 {
		t.Fatalf("config revision = %d, want 1 after provider migration", svc.configRevision)
	}
}

func TestMCPServerConfigProviderFailedUnpinnedMigrationPreservesConfigAndRevision(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	dbPath := filepath.Join(project, "super-dolphin.db")
	unpinned := ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@bytebase/dbhub", "--dsn=" + sqliteDBHubDSN(dbPath)},
		Enabled:   boolPtr(true),
	}
	store.seed(project, DefaultSQLiteServerName, unpinned)
	injectedErr := errors.New("injected provider replace failure")
	store.replaceErr = injectedErr
	svc := newServiceWithStoreAndSQLitePath(store, dbPath)
	svc.configRevision = 9

	_, err := AsMCPServerConfigProvider(svc).ListMCPServerConfigs(context.Background(), project)
	if !errors.Is(err, injectedErr) {
		t.Fatalf("ListMCPServerConfigs() error = %v, want injected replacement failure", err)
	}
	if svc.configRevision != 9 {
		t.Fatalf("config revision = %d, want unchanged 9", svc.configRevision)
	}
	if got := store.servers[project][DefaultSQLiteServerName]; !reflect.DeepEqual(got, unpinned) {
		t.Fatalf("stored config = %#v, want unchanged %#v", got, unpinned)
	}
}

func TestStartSQLiteServerFailedLegacyReplacementPreservesConfigAndRevision(t *testing.T) {
	store := newMemoryMCPServerStore()
	project := t.TempDir()
	newDBPath := filepath.Join(project, "super-dolphin.db")
	legacy := ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", legacyDefaultSQLitePackage, filepath.Join("old", "legacy.db")},
		Enabled:   boolPtr(false),
	}
	store.seed(project, DefaultSQLiteServerName, legacy)
	injectedErr := errors.New("injected replace failure")
	store.replaceErr = injectedErr
	svc := newServiceWithStoreAndSQLitePath(store, newDBPath)
	svc.configRevision = 7
	t.Chdir(project)

	_, err := svc.StartSQLiteServer(context.Background(), StartSQLiteServerRequest{})
	if !errors.Is(err, injectedErr) {
		t.Fatalf("StartSQLiteServer() error = %v, want injected replace failure", err)
	}
	if svc.configRevision != 7 {
		t.Fatalf("config revision = %d, want unchanged 7", svc.configRevision)
	}
	got := store.servers[project][DefaultSQLiteServerName]
	if !reflect.DeepEqual(got, legacy) {
		t.Fatalf("stored legacy config = %#v, want unchanged %#v", got, legacy)
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
	wantArgs := []string{"-y", "@bytebase/dbhub@0.23.0", "--dsn=" + sqliteDBHubDSN(dbPath)}
	if !slices.Equal(server.Args, wantArgs) {
		t.Fatalf("stored sqlite args = %#v, want %#v", server.Args, wantArgs)
	}
	if server.Enabled == nil || !*server.Enabled {
		t.Fatalf("stored sqlite enabled = %#v, want true", server.Enabled)
	}
}
