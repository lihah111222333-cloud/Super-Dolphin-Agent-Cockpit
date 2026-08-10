package config

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const testInternalSQLitePathEnvKey = "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"

func TestNew_PrefersCanonicalRPCAddr(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "127.0.0.1:9200")
	t.Setenv("RPC_ADDR", "127.0.0.1:9300")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := mustNewConfig(t)
	if cfg.RPCAddr != "127.0.0.1:9200" {
		t.Fatalf("RPCAddr = %q", cfg.RPCAddr)
	}
	if logs := buf.String(); logs != "" {
		t.Fatalf("logs = %q, want empty", logs)
	}
}

func TestNew_UsesLegacyRPCAddrWithDeprecationWarning(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("RPC_ADDR", "127.0.0.1:9100")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := mustNewConfig(t)
	if cfg.RPCAddr != "127.0.0.1:9100" {
		t.Fatalf("RPCAddr = %q", cfg.RPCAddr)
	}
	logs := buf.String()
	if !strings.Contains(logs, "config env deprecated") ||
		!strings.Contains(logs, "legacy=RPC_ADDR") ||
		!strings.Contains(logs, "canonical=GO_AGENT_CTL_RPC_ADDR") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestNew_DefaultsSQLitePathUnderProjectHomeInDev(t *testing.T) {
	isolateConfigTestEnv(t)

	cfg := mustNewConfig(t)
	want := filepath.Join(cfg.ProjectRoot, ".super-dolphin", "super-dolphin.db")
	if cfg.SQLitePath != want {
		t.Fatalf("SQLitePath = %q, want %q", cfg.SQLitePath, want)
	}
	if got := os.Getenv("DATABASE_URL"); got != "" {
		t.Fatalf("DATABASE_URL = %q, want empty", got)
	}
}

func TestNew_UsesExplicitSQLitePath(t *testing.T) {
	isolateConfigTestEnv(t)
	explicit := filepath.Join(t.TempDir(), "state", "custom.db")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", explicit)

	cfg := mustNewConfig(t)
	if cfg.SQLitePath != explicit {
		t.Fatalf("SQLitePath = %q, want %q", cfg.SQLitePath, explicit)
	}
	if got := os.Getenv("DATABASE_URL"); got != "" {
		t.Fatalf("DATABASE_URL = %q, want no DB env writeback", got)
	}
}

func TestNew_NormalizesRelativeExplicitSQLitePathToAbsolute(t *testing.T) {
	isolateConfigTestEnv(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", filepath.Join("state", "custom.db"))

	cfg := mustNewConfig(t)
	want := filepath.Join(root, "state", "custom.db")
	if cfg.SQLitePath != want {
		t.Fatalf("SQLitePath = %q, want absolute normalized path %q", cfg.SQLitePath, want)
	}
}

func TestNew_NormalizesRelativeSuperDolphinHomeToAbsoluteSQLitePath(t *testing.T) {
	isolateConfigTestEnv(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("SUPER_DOLPHIN_HOME", "home")

	cfg := mustNewConfig(t)
	want := filepath.Join(root, "home", "super-dolphin.db")
	if cfg.SQLitePath != want {
		t.Fatalf("SQLitePath = %q, want absolute SUPER_DOLPHIN_HOME path %q", cfg.SQLitePath, want)
	}
}

func TestNew_UsesInternalSQLitePathChannelWhenPublicKeyAbsent(t *testing.T) {
	isolateConfigTestEnv(t)
	explicit := filepath.Join(t.TempDir(), "state", "internal.db")
	t.Setenv(testInternalSQLitePathEnvKey, explicit)

	cfg := mustNewConfig(t)
	if cfg.SQLitePath != explicit {
		t.Fatalf("SQLitePath = %q, want internal SQLite path %q", cfg.SQLitePath, explicit)
	}
}

func TestNew_RejectsConflictingPublicAndInternalSQLitePath(t *testing.T) {
	isolateConfigTestEnv(t)
	publicPath := filepath.Join(t.TempDir(), "state", "public.db")
	internalPath := filepath.Join(t.TempDir(), "state", "internal.db")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", publicPath)
	t.Setenv(testInternalSQLitePathEnvKey, internalPath)

	_, err := New()
	if err == nil {
		t.Fatal("New() error = nil, want conflicting SQLite path env failure")
	}
	for _, want := range []string{"conflicting SQLite path", "SUPER_DOLPHIN_SQLITE_PATH", testInternalSQLitePathEnvKey} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("New() error = %v, want substring %q", err, want)
		}
	}
}

func TestNew_IgnoresPostgresEnvForDatabaseConfig(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("DATABASE_URL", "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@127.0.0.1:54320/compat_db?sslmode=disable")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := mustNewConfig(t)
	if cfg.SQLitePath == "" {
		t.Fatal("SQLitePath is empty")
	}
	if got := os.Getenv("DATABASE_URL"); got != "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable" {
		t.Fatalf("DATABASE_URL = %q, want preserved but ignored", got)
	}
	if logs := buf.String(); strings.Contains(logs, "POSTGRES_CONNECTION_STRING") || strings.Contains(logs, "DATABASE_URL") {
		t.Fatalf("logs = %q, want no PG env warning or DB DSN logging", logs)
	}
}

func TestNew_PostgresEnvAndOldDataDirDoNotOverrideSQLitePath(t *testing.T) {
	isolateConfigTestEnv(t)
	projectRoot := t.TempDir()
	t.Setenv("PROJECT_ROOT", projectRoot)
	t.Setenv("DATABASE_URL", "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@127.0.0.1:54320/compat_db?sslmode=disable")
	explicitSQLite := filepath.Join(t.TempDir(), "state", "winner.db")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", explicitSQLite)

	oldPGDataDir := filepath.Join(projectRoot, ".tmp", "pgdata")
	if err := os.MkdirAll(oldPGDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pgVersion := filepath.Join(oldPGDataDir, "PG_VERSION")
	if err := os.WriteFile(pgVersion, []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(pgVersion)
	if err != nil {
		t.Fatal(err)
	}

	cfg := mustNewConfig(t)
	if cfg.SQLitePath != explicitSQLite {
		t.Fatalf("SQLitePath = %q, want explicit SQLite path %q", cfg.SQLitePath, explicitSQLite)
	}
	after, err := os.Stat(pgVersion)
	if err != nil {
		t.Fatalf("old PG data dir was touched or removed: %v", err)
	}
	assertFileStatUnchanged(t, before, after)
	if got := os.Getenv("DATABASE_URL"); got == "" {
		t.Fatal("DATABASE_URL was cleared; want preserved in parent env but ignored")
	}
}

func assertFileStatUnchanged(t *testing.T, before, after os.FileInfo) {
	t.Helper()
	if after.ModTime() != before.ModTime() || after.Size() != before.Size() {
		t.Fatalf("old PG data file changed: before=%v/%d after=%v/%d", before.ModTime(), before.Size(), after.ModTime(), after.Size())
	}
}

func TestNew_LoadsDotEnvFromProjectRoot(t *testing.T) {
	declareTestDependencyBootstrap(t)
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", "")
	t.Setenv("LOG_LEVEL", "")

	sqlitePath := filepath.Join(root, "state", "dotenv.db")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SUPER_DOLPHIN_SQLITE_PATH="+sqlitePath+"\nLOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := mustNewConfig(t)
	if cfg.ProjectRoot != root {
		t.Fatalf("ProjectRoot = %q, want %q", cfg.ProjectRoot, root)
	}
	if cfg.SQLitePath != sqlitePath {
		t.Fatalf("SQLitePath = %q, want %q", cfg.SQLitePath, sqlitePath)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if got := os.Getenv("DATABASE_URL"); got != "" {
		t.Fatalf("DATABASE_URL = %q, want no DB env writeback", got)
	}
}

func TestNew_RejectsEmptyExplicitSQLitePath(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", "   ")

	_, err := New()
	if err == nil {
		t.Fatal("New() error = nil, want empty SUPER_DOLPHIN_SQLITE_PATH fail-fast")
	}
	if !strings.Contains(err.Error(), "SUPER_DOLPHIN_SQLITE_PATH") {
		t.Fatalf("New() error = %v, want SUPER_DOLPHIN_SQLITE_PATH", err)
	}
}

func TestResolveSQLitePathRejectsDirectoryWithRedactedPath(t *testing.T) {
	isolateConfigTestEnv(t)
	dir := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", dir)

	_, err := New()
	if err == nil {
		t.Fatal("New() error = nil, want directory path fail-fast")
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("New() error leaked full path: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted:") {
		t.Fatalf("New() error = %v, want redacted path", err)
	}
}

func TestResolveSQLitePathRejectsParentFileWithRedactedPath(t *testing.T) {
	isolateConfigTestEnv(t)
	parentFile := filepath.Join(t.TempDir(), "private-parent")
	if err := os.WriteFile(parentFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(parentFile, "secret.db")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", dbPath)

	_, err := New()
	if err == nil {
		t.Fatal("New() error = nil, want parent-file fail-fast")
	}
	if strings.Contains(err.Error(), parentFile) || strings.Contains(err.Error(), dbPath) {
		t.Fatalf("New() error leaked full path: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted:private-parent>") {
		t.Fatalf("New() error = %v, want redacted parent path", err)
	}
}

func TestResolvePackagedSQLiteHomeByOS(t *testing.T) {
	tests := []struct {
		name string
		goos string
		env  map[string]string
		home string
		want string
	}{
		{
			name: "windows appdata",
			goos: "windows",
			env:  map[string]string{"APPDATA": filepath.FromSlash("C:/Users/test/AppData/Roaming")},
			want: filepath.FromSlash("C:/Users/test/AppData/Roaming/Super Dolphin"),
		},
		{
			name: "macos application support",
			goos: "darwin",
			home: filepath.FromSlash("/Users/test"),
			want: filepath.FromSlash("/Users/test/Library/Application Support/Super Dolphin"),
		},
		{
			name: "linux xdg",
			goos: "linux",
			env:  map[string]string{"XDG_DATA_HOME": filepath.FromSlash("/home/test/.local/state")},
			home: filepath.FromSlash("/home/test"),
			want: filepath.FromSlash("/home/test/.local/state/Super Dolphin"),
		},
		{
			name: "linux home fallback",
			goos: "linux",
			home: filepath.FromSlash("/home/test"),
			want: filepath.FromSlash("/home/test/.local/share/Super Dolphin"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			home := func() (string, error) { return tt.home, nil }

			got, err := resolvePackagedSQLiteHome(tt.goos, getenv, home)
			if err != nil {
				t.Fatalf("resolvePackagedSQLiteHome() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolvePackagedSQLiteHome() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolvePackagedProjectRootUsesMacOSResources(t *testing.T) {
	got := resolvePackagedProjectRoot(filepath.FromSlash("/Applications/Super Dolphin.app/Contents/MacOS/agent-terminal"))
	want := filepath.FromSlash("/Applications/Super Dolphin.app/Contents/Resources")
	if got != want {
		t.Fatalf("resolvePackagedProjectRoot() = %q, want %q", got, want)
	}
}

func TestPackagedProjectRootMigrationsDirUsesSQLiteLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !hasPackagedProjectRootMigrationsDir(root) {
		t.Fatal("hasPackagedProjectRootMigrationsDir() = false, want true for SQLite migrations layout")
	}
}

func TestPackagedProjectRootMigrationsDirRejectsLegacyTopLevelMigrations(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}

	if hasPackagedProjectRootMigrationsDir(root) {
		t.Fatal("hasPackagedProjectRootMigrationsDir() = true, want false for legacy top-level migrations")
	}
}

func TestSharedFileRootUsesProjectRootOutsidePackagedRuntime(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "")
	projectRoot := filepath.Join(t.TempDir(), "project")

	got, err := SharedFileRoot(&Config{ProjectRoot: projectRoot})
	if err != nil {
		t.Fatalf("SharedFileRoot() error = %v", err)
	}
	if got != projectRoot {
		t.Fatalf("SharedFileRoot() = %q, want project root %q", got, projectRoot)
	}
}

func TestSharedFileRootFailsFastForPackagedRuntimeWithoutHome(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", "")

	_, err := SharedFileRoot(&Config{ProjectRoot: filepath.Join(t.TempDir(), "Super Dolphin.app", "Contents", "Resources")})
	if err == nil {
		t.Fatal("SharedFileRoot() error = nil, want missing SUPER_DOLPHIN_HOME failure")
	}
	if !strings.Contains(err.Error(), "SUPER_DOLPHIN_HOME") {
		t.Fatalf("SharedFileRoot() error = %v, want SUPER_DOLPHIN_HOME", err)
	}
}

func TestNew_DefaultsPersistentSubagentDefaultOn(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "")
	cfg := mustNewConfig(t)
	if !cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = false, want true")
	}
}

func TestNew_AllowsEnablingPersistentSubagentDefault(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "true")
	cfg := mustNewConfig(t)
	if !cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = false, want true")
	}
}

func TestNew_AllowsDisablingPersistentSubagentDefault(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "false")
	cfg := mustNewConfig(t)
	if cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = true, want false")
	}
}

func TestNew_RejectsInvalidPresentEnvValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "skill bool", key: "SKILL_PROGRESSIVE_DISCLOSURE", value: "sometimes"},
		{name: "skill token budget", key: "SKILL_TOKEN_BUDGET", value: "0"},
		{name: "agent bool", key: "PERSISTENT_SUBAGENT_DEFAULT", value: "enabled"},
		{name: "notify bool", key: "NOTIFY_ALLOW_PRIVATE_CIDR", value: "not-bool"},
		{name: "notify timeout", key: "NOTIFY_TIMEOUT_SECONDS", value: "-1"},
		{name: "notify queue", key: "NOTIFY_QUEUE_CAPACITY", value: "nope"},
		{name: "notify drain", key: "NOTIFY_DRAIN_SECONDS", value: "0"},
		{name: "lsp bool", key: "LSP_DISABLE_INITIAL_WORKSPACE_BOOTSTRAP", value: "maybe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfigTestEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := New()
			if err == nil {
				t.Fatalf("New() error = nil, want invalid %s to fail fast", tt.key)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("New() error = %v, want env key %s", err, tt.key)
			}
		})
	}
}

func TestNew_DefaultsLSPConfig(t *testing.T) {
	isolateConfigTestEnv(t)
	cfg := mustNewConfig(t)

	jsts := cfg.LSP.ProjectAdapters[contract.LSPServiceJSTS]
	requireStringSliceContains(t, "LSP jsts root markers", jsts.RootMarkers, "package.json")
	markdown := requireLSPProjectAdapter(t, cfg, contract.LSPServiceMarkdown)
	requireStringSliceContains(t, "LSP markdown root markers", markdown.RootMarkers, "readme.md")
	requireStringSliceContains(t, "LSP noise dirs", cfg.LSP.NoiseDirNames, "docs")
	requireStringSliceContains(t, "LSP gopls directory filters", cfg.LSP.GoDirectoryFilters, "-**/docs")
	requireStringSliceContains(t, "LSP gopls directory filters", cfg.LSP.GoDirectoryFilters, "-docs")
	if !cfg.LSP.DisableInitialWorkspaceBootstrap {
		t.Fatal("LSP disable initial workspace bootstrap default = false, want true")
	}
	if cfg.LSP.IdleTimeout != lspDefaultIdleTimeout {
		t.Fatalf("LSP idle timeout default = %s, want %s", cfg.LSP.IdleTimeout, lspDefaultIdleTimeout)
	}
	shell := requireLSPProjectAdapter(t, cfg, contract.LSPServiceShell)
	for _, ext := range []string{".sh", ".bash", ".zsh", ".ksh", ".bats"} {
		requireStringSliceContains(t, "LSP shell first source extensions", shell.FirstSourceExtensions, ext)
	}
	sql := requireLSPProjectAdapter(t, cfg, contract.LSPServiceSQL)
	requireStringSliceContains(t, "LSP sql root markers", sql.RootMarkers, "sqlc.yaml")
	requireStringSliceContains(t, "LSP sql first source extensions", sql.FirstSourceExtensions, ".sql")
	proto := requireLSPProjectAdapter(t, cfg, contract.LSPServiceProto)
	for _, marker := range []string{"buf.yaml", "buf.work.yaml", ".git"} {
		requireStringSliceContains(t, "LSP proto root markers", proto.RootMarkers, marker)
	}
	requireStringSliceContains(t, "LSP proto ignored dirs", proto.IgnoredDirNames, ".buf")
	requireStringSliceContains(t, "LSP proto first source extensions", proto.FirstSourceExtensions, ".proto")
}

func requireLSPProjectAdapter(t *testing.T, cfg *Config, service string) contract.LSPProjectAdapterConfig {
	t.Helper()
	adapter, ok := cfg.LSP.ProjectAdapters[service]
	if !ok {
		t.Fatalf("LSP project adapters missing %s", service)
	}
	return adapter
}

func requireStringSliceContains(t *testing.T, label string, got []string, want string) {
	t.Helper()
	if !slices.Contains(got, want) {
		t.Fatalf("%s = %#v, missing %s", label, got, want)
	}
}

func TestNew_LoadsLSPConfigFromDotEnv(t *testing.T) {
	declareTestDependencyBootstrap(t)
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	t.Setenv("LOG_LEVEL", "")
	clearLSPConfigEnv(t)

	body := strings.Join([]string{
		"LSP_NOISE_DIRS=docs,.agent",
		"LSP_GO_DIRECTORY_FILTERS=-**/docs,-**/.agent",
		"LSP_JSTS_ROOT_MARKERS=custom.workspace,package.json",
		"LSP_PYTHON_ROOT_MARKERS=pyproject.toml,requirements.txt",
		"LSP_SQL_ROOT_MARKERS=.sqllsrc.json,sqlc.yaml",
		"LSP_PROTO_ROOT_MARKERS=buf.workspace,buf.yaml",
		"LSP_PROTO_IGNORED_DIRS=.buf,third_party",
		"LSP_PROTO_FIRST_SOURCE_EXTENSIONS=.proto,.protodevel",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := mustNewConfig(t)
	if got := cfg.LSP.NoiseDirNames; !slices.Equal(got, []string{"docs", ".agent"}) {
		t.Fatalf("LSP noise dirs = %#v", got)
	}
	if got := cfg.LSP.ProjectAdapters[contract.LSPServiceJSTS].RootMarkers; !slices.Equal(got, []string{"custom.workspace", "package.json"}) {
		t.Fatalf("LSP jsts root markers = %#v", got)
	}
	if got := cfg.LSP.ProjectAdapters[contract.LSPServicePython].RootMarkers; !slices.Equal(got, []string{"pyproject.toml", "requirements.txt"}) {
		t.Fatalf("LSP python root markers = %#v", got)
	}
	if got := cfg.LSP.ProjectAdapters[contract.LSPServiceSQL].RootMarkers; !slices.Equal(got, []string{".sqllsrc.json", "sqlc.yaml"}) {
		t.Fatalf("LSP sql root markers = %#v", got)
	}
	if got := cfg.LSP.ProjectAdapters[contract.LSPServiceProto].RootMarkers; !slices.Equal(got, []string{"buf.workspace", "buf.yaml"}) {
		t.Fatalf("LSP proto root markers = %#v", got)
	}
	if got := cfg.LSP.ProjectAdapters[contract.LSPServiceProto].IgnoredDirNames; !slices.Equal(got, []string{".buf", "third_party"}) {
		t.Fatalf("LSP proto ignored dirs = %#v", got)
	}
	if got := cfg.LSP.ProjectAdapters[contract.LSPServiceProto].FirstSourceExtensions; !slices.Equal(got, []string{".proto", ".protodevel"}) {
		t.Fatalf("LSP proto first source extensions = %#v", got)
	}
}

func isolateConfigTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PROJECT_ROOT", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	unsetEnvForTest(t, "SUPER_DOLPHIN_SQLITE_PATH")
	unsetEnvForTest(t, testInternalSQLitePathEnvKey)
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("RPC_ADDR", "")
	declareTestDependencyBootstrap(t)
	clearLSPConfigEnv(t)
}

func declareTestDependencyBootstrap(t *testing.T) {
	t.Helper()
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "")
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%s): %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func clearLSPConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LSP_NOISE_DIRS",
		"LSP_GO_DIRECTORY_FILTERS",
		"LSP_JSTS_ROOT_MARKERS",
		"LSP_PYTHON_ROOT_MARKERS",
		"LSP_RUST_ROOT_MARKERS",
		"LSP_JAVA_ROOT_MARKERS",
		"LSP_CSS_ROOT_MARKERS",
		"LSP_SHELL_ROOT_MARKERS",
		"LSP_SQL_ROOT_MARKERS",
		"LSP_PROTO_ROOT_MARKERS",
		"LSP_JSTS_IGNORED_DIRS",
		"LSP_PYTHON_IGNORED_DIRS",
		"LSP_RUST_IGNORED_DIRS",
		"LSP_JAVA_IGNORED_DIRS",
		"LSP_CSS_IGNORED_DIRS",
		"LSP_SHELL_IGNORED_DIRS",
		"LSP_SQL_IGNORED_DIRS",
		"LSP_PROTO_IGNORED_DIRS",
		"LSP_SHELL_FIRST_SOURCE_EXTENSIONS",
		"LSP_SQL_FIRST_SOURCE_EXTENSIONS",
		"LSP_PROTO_FIRST_SOURCE_EXTENSIONS",
		"LSP_DISABLE_INITIAL_WORKSPACE_BOOTSTRAP",
	} {
		t.Setenv(key, "")
	}
	unsetEnvForTest(t, lspIdleTimeoutEnv)
	unsetEnvForTest(t, lspIdleTimeoutLegacyEnv)
}

func restoreConfigLogger(t *testing.T, dst *bytes.Buffer) {
	t.Helper()
	original := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewTextHandler(dst, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { pkglogger.SetForTest(original) })
}

func TestPrimeProcessEnvironmentAllowsMissingDotEnvOutsidePackagedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)

	got, err := PrimeProcessEnvironment()
	if err != nil {
		t.Fatalf("PrimeProcessEnvironment() error = %v, want nil for dev runtime without .env", err)
	}
	if got != root {
		t.Fatalf("PrimeProcessEnvironment() = %q, want %q", got, root)
	}
}

func TestPrimeProcessEnvironmentFailsFastForMalformedDotEnvOutsidePackagedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("LOG_LEVEL\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := PrimeProcessEnvironment()
	if err == nil {
		t.Fatal("PrimeProcessEnvironment() error = nil, want malformed .env failure")
	}
	for _, want := range []string{"parse .env", "line 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("PrimeProcessEnvironment() error = %v, want substring %q", err, want)
		}
	}
}

func TestPrimeProcessEnvironmentFailsFastForPackagedDotEnvErrors(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, root string)
		want    []string
	}{
		{
			name: "missing",
			want: []string{"load packaged .env", ".env"},
		},
		{
			name: "unreadable",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, ".env"), 0o755); err != nil {
					t.Fatalf("mkdir .env: %v", err)
				}
			},
			want: []string{"load packaged .env", ".env"},
		},
		{
			name: "malformed",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL\n"), 0o600); err != nil {
					t.Fatalf("write .env: %v", err)
				}
			},
			want: []string{"parse packaged .env", "line 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PROJECT_ROOT", root)
			if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
				t.Fatalf("write runtime manifest marker: %v", err)
			}
			if tt.prepare != nil {
				tt.prepare(t, root)
			}

			_, err := PrimeProcessEnvironment()
			if err == nil {
				t.Fatal("PrimeProcessEnvironment() error = nil, want packaged .env failure")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("PrimeProcessEnvironment() error = %v, want substring %q", err, want)
				}
			}
		})
	}
}

func TestPrimeProcessEnvironmentReturnsDotEnvSetenvError(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	t.Setenv("LOG_LEVEL", "")
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write runtime manifest marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("LOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	err := applyDotEnv(func(key, value string) error {
		if key == "LOG_LEVEL" {
			return errors.New("injected config setenv failure")
		}
		return os.Setenv(key, value)
	}, filepath.Join(root, ".env"), "LOG_LEVEL=debug\n", true)
	if err == nil {
		t.Fatal("applyDotEnv() error = nil, want setenv failure")
	}
	for _, want := range []string{"set environment from .env", "LOG_LEVEL", "injected config setenv failure"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("applyDotEnv() error = %v, want substring %q", err, want)
		}
	}
}

func mustNewConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return cfg
}
