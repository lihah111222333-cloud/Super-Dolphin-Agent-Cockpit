package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestNew_PrefersCanonicalRPCAddr(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "127.0.0.1:9200")
	t.Setenv("RPC_ADDR", "127.0.0.1:9300")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := New()
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

	cfg := New()
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

func TestNew_ExportsResolvedDatabaseURLWhenEnvMissing(t *testing.T) {
	isolateConfigTestEnv(t)

	cfg := New()
	if got := strings.TrimSpace(cfg.DatabaseURL); got == "" {
		t.Fatal("DatabaseURL is empty")
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
}

func TestNew_PreservesDatabaseURLFromEnv(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("DATABASE_URL", "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable")

	cfg := New()
	if cfg.DatabaseURL != "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
}

func TestNew_UsesPostgresConnectionStringCompat(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@127.0.0.1:54320/compat_db?sslmode=disable")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := New()
	if cfg.DatabaseURL != "postgres://compat@127.0.0.1:54320/compat_db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
	if logs := buf.String(); !strings.Contains(logs, "POSTGRES_CONNECTION_STRING is deprecated; use DATABASE_URL instead") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestNew_LoadsDotEnvFromProjectRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	t.Setenv("LOG_LEVEL", "")

	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("POSTGRES_CONNECTION_STRING=postgres://dotenv@127.0.0.1:54320/dotenv_db?sslmode=disable\nLOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := New()
	if cfg.ProjectRoot != root {
		t.Fatalf("ProjectRoot = %q, want %q", cfg.ProjectRoot, root)
	}
	if cfg.DatabaseURL != "postgres://dotenv@127.0.0.1:54320/dotenv_db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
}

func TestNew_DefaultsPersistentSubagentDefaultOff(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "")
	cfg := New()
	if cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = true, want false")
	}
}

func TestNew_AllowsEnablingPersistentSubagentDefault(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "true")
	cfg := New()
	if !cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = false, want true")
	}
}

func TestNew_DefaultsLSPConfig(t *testing.T) {
	isolateConfigTestEnv(t)
	cfg := New()

	jsts := cfg.LSP.ProjectAdapters[contract.LSPServiceJSTS]
	if !slices.Contains(jsts.RootMarkers, "package.json") {
		t.Fatalf("LSP jsts root markers = %#v, missing package.json", jsts.RootMarkers)
	}
	if !slices.Contains(cfg.LSP.NoiseDirNames, "docs") {
		t.Fatalf("LSP noise dirs = %#v, missing docs", cfg.LSP.NoiseDirNames)
	}
	if !slices.Contains(cfg.LSP.GoDirectoryFilters, "-**/docs") {
		t.Fatalf("LSP gopls directory filters = %#v, missing -**/docs", cfg.LSP.GoDirectoryFilters)
	}
	if !slices.Contains(cfg.LSP.GoDirectoryFilters, "-docs") {
		t.Fatalf("LSP gopls directory filters = %#v, missing -docs", cfg.LSP.GoDirectoryFilters)
	}
}

func TestNew_LoadsLSPConfigFromDotEnv(t *testing.T) {
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
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := New()
	if got := cfg.LSP.NoiseDirNames; !slices.Equal(got, []string{"docs", ".agent"}) {
		t.Fatalf("LSP noise dirs = %#v", got)
	}
	if got := cfg.LSP.ProjectAdapters[contract.LSPServiceJSTS].RootMarkers; !slices.Equal(got, []string{"custom.workspace", "package.json"}) {
		t.Fatalf("LSP jsts root markers = %#v", got)
	}
	if got := cfg.LSP.ProjectAdapters[contract.LSPServicePython].RootMarkers; !slices.Equal(got, []string{"pyproject.toml", "requirements.txt"}) {
		t.Fatalf("LSP python root markers = %#v", got)
	}
}

func isolateConfigTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PROJECT_ROOT", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("RPC_ADDR", "")
	clearLSPConfigEnv(t)
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
		"LSP_JSTS_IGNORED_DIRS",
		"LSP_PYTHON_IGNORED_DIRS",
		"LSP_RUST_IGNORED_DIRS",
		"LSP_JAVA_IGNORED_DIRS",
		"LSP_CSS_IGNORED_DIRS",
	} {
		t.Setenv(key, "")
	}
}

func restoreConfigLogger(t *testing.T, dst *bytes.Buffer) {
	t.Helper()
	original := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewTextHandler(dst, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { pkglogger.SetForTest(original) })
}
