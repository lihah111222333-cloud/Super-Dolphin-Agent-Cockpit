package embeddedpg

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestResolveConfigUsesExternalDatabaseURL(t *testing.T) {
	input := ResolveInput{
		GOOS: "darwin",
		Env: map[string]string{
			"DATABASE_URL": "postgres://external@127.0.0.1:5432/app?sslmode=disable",
		},
		UserHome: "/Users/tester",
	}

	cfg, databaseURL := ResolveConfig(input)
	if cfg.Enabled {
		t.Fatalf("EmbeddedPostgres.Enabled = true, want false when DATABASE_URL is set")
	}
	if databaseURL != "postgres://external@127.0.0.1:5432/app?sslmode=disable" {
		t.Fatalf("databaseURL = %q", databaseURL)
	}
}

func TestResolveConfigDefaultsToDarwinApplicationSupport(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(app, "Contents", "Resources")
	writePackagedRuntimeFixture(t, resources, "darwin-arm64")
	input := ResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		Env:            map[string]string{"SUPER_DOLPHIN_PROCESS_ROLE": "desktop"},
		ExecutablePath: filepath.Join(app, "Contents", "MacOS", "agent-terminal"),
		UserHome:       "/Users/tester",
	}

	cfg, databaseURL := ResolveConfig(input)
	if !cfg.Enabled {
		t.Fatal("EmbeddedPostgres.Enabled = false, want true when no external database URL is set")
	}
	wantBase := filepath.Join("/Users/tester", "Library", "Application Support", "Super Dolphin")
	if cfg.DataDir != filepath.Join(wantBase, "postgres", "data") {
		t.Fatalf("DataDir = %q", cfg.DataDir)
	}
	wantRuntime := filepath.Join("/tmp", "sd-pg-"+strconv.Itoa(os.Getuid())+"-55432")
	if cfg.RuntimeDir != wantRuntime {
		t.Fatalf("RuntimeDir = %q", cfg.RuntimeDir)
	}
	if cfg.BinDir != filepath.Join(resources, "postgres", "darwin-arm64", "bin") {
		t.Fatalf("BinDir = %q", cfg.BinDir)
	}
	if !cfg.RecoverRunningDataDir {
		t.Fatal("RecoverRunningDataDir = false, want true for packaged desktop")
	}
	if cfg.ShareDir != filepath.Join(resources, "postgres", "darwin-arm64", "share", "postgresql@16") {
		t.Fatalf("ShareDir = %q", cfg.ShareDir)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse databaseURL: %v", err)
	}
	if parsed.Query().Get("host") != cfg.RuntimeDir {
		t.Fatalf("databaseURL host query = %q, want runtime dir %q", parsed.Query().Get("host"), cfg.RuntimeDir)
	}
	if parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("databaseURL sslmode = %q", parsed.Query().Get("sslmode"))
	}
}

func TestResolveConfigWindowsDatabaseURLUsesTCP(t *testing.T) {
	cfg, databaseURL := ResolveConfig(ResolveInput{
		GOOS:   "windows",
		GOARCH: "amd64",
		Env: map[string]string{
			"SUPER_DOLPHIN_EMBEDDED_POSTGRES": "true",
			"SUPER_DOLPHIN_POSTGRES_BIN_DIR":  `C:\SuperDolphin\postgres\windows-amd64\bin`,
		},
		UserHome: `C:\Users\tester`,
	})
	if !cfg.Enabled {
		t.Fatal("EmbeddedPostgres.Enabled = false, want true")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse databaseURL: %v", err)
	}
	if parsed.Host != "127.0.0.1:55432" {
		t.Fatalf("databaseURL host = %q, want TCP localhost", parsed.Host)
	}
	if got := parsed.Query().Get("host"); got != "" {
		t.Fatalf("databaseURL unix-socket host query = %q, want empty on windows", got)
	}
}

func TestResolveConfigMarksDesktopAsEmbeddedPostgresOwner(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(app, "Contents", "Resources")
	writePackagedRuntimeFixture(t, resources, "darwin-arm64")
	cfg, dsn := ResolveConfig(ResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		Env:            map[string]string{"SUPER_DOLPHIN_PROCESS_ROLE": "desktop"},
		ExecutablePath: filepath.Join(app, "Contents", "MacOS", "agent-terminal"),
		ProjectRoot:    resources,
		UserHome:       "/Users/alice",
	})
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if !cfg.Owner {
		t.Fatal("Owner = false, want true for desktop role")
	}
	if !strings.Contains(dsn, "super_dolphin") {
		t.Fatalf("dsn = %q, want super_dolphin database", dsn)
	}
}

func TestResolveConfigMarksSidecarAsClientOnly(t *testing.T) {
	cfg, databaseURL := ResolveConfig(ResolveInput{
		GOOS:   "darwin",
		GOARCH: "arm64",
		Env: map[string]string{
			"DATABASE_URL":               "postgres://owner@localhost/super_dolphin?sslmode=disable",
			"SUPER_DOLPHIN_PROCESS_ROLE": "sidecar",
		},
		ExecutablePath: "/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch",
		ProjectRoot:    "/Applications/Super Dolphin.app/Contents/Resources",
		UserHome:       "/Users/alice",
	})
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false when owner desktop DATABASE_URL is inherited")
	}
	if cfg.Owner {
		t.Fatal("Owner = true, want false for sidecar role")
	}
	if cfg.RecoverRunningDataDir {
		t.Fatal("RecoverRunningDataDir = true, want false for sidecar role")
	}
	if databaseURL != "postgres://owner@localhost/super_dolphin?sslmode=disable" {
		t.Fatalf("databaseURL = %q, want inherited owner DATABASE_URL", databaseURL)
	}
}

func TestResolveConfigDoesNotSynthesizeDatabaseURLForPackagedSidecar(t *testing.T) {
	cfg, databaseURL := ResolveConfig(ResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		Env:            map[string]string{"SUPER_DOLPHIN_PROCESS_ROLE": "sidecar"},
		ExecutablePath: "/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch",
		ProjectRoot:    "/Applications/Super Dolphin.app/Contents/Resources",
		UserHome:       "/Users/alice",
	})
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false so sidecars must inherit owner desktop DATABASE_URL")
	}
	if strings.TrimSpace(databaseURL) != "" {
		t.Fatalf("databaseURL = %q, want empty so missing owner DATABASE_URL fails fast", databaseURL)
	}
}

func TestResolveConfigDoesNotEnableEmbeddedPostgresInDevByDefault(t *testing.T) {
	cfg, databaseURL := ResolveConfig(ResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		ExecutablePath: "/Users/alice/src/Super-Dolphin/bin/agent-terminal",
		ProjectRoot:    "/Users/alice/src/Super-Dolphin",
		UserHome:       "/Users/alice",
	})
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false without packaged runtime or explicit opt-in")
	}
	if cfg.RecoverRunningDataDir {
		t.Fatal("RecoverRunningDataDir = true, want false outside packaged desktop")
	}
	if strings.TrimSpace(databaseURL) != "" {
		t.Fatalf("databaseURL = %q, want empty without packaged runtime or explicit opt-in", databaseURL)
	}
}

func TestResolveConfigExplicitOptInUsesProjectRootPostgresBeforeDevExecutable(t *testing.T) {
	cfg, _ := ResolveConfig(ResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		Env:            map[string]string{"SUPER_DOLPHIN_EMBEDDED_POSTGRES": "true"},
		ExecutablePath: "/Users/alice/src/Super-Dolphin/bin/agent-terminal",
		ProjectRoot:    "/Users/alice/src/Super-Dolphin",
		UserHome:       "/Users/alice",
	})
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true with explicit embedded postgres opt-in")
	}
	wantBinDir := filepath.Join("/Users/alice/src/Super-Dolphin", "third_party", "postgres", "darwin-arm64", "bin")
	if cfg.BinDir != wantBinDir {
		t.Fatalf("BinDir = %q, want %q", cfg.BinDir, wantBinDir)
	}
}

func TestResolveConfigHonorsExplicitPostgresBinDir(t *testing.T) {
	input := ResolveInput{
		GOOS:     "linux",
		GOARCH:   "amd64",
		UserHome: "/home/tester",
		Env: map[string]string{
			"SUPER_DOLPHIN_POSTGRES_BIN_DIR": "/opt/super-dolphin/postgres/bin",
		},
	}

	cfg, _ := ResolveConfig(input)
	if cfg.BinDir != "/opt/super-dolphin/postgres/bin" {
		t.Fatalf("BinDir = %q", cfg.BinDir)
	}
}

func TestResolveConfigHonorsExplicitPostgresShareDir(t *testing.T) {
	input := ResolveInput{
		GOOS:     "linux",
		GOARCH:   "amd64",
		UserHome: "/home/tester",
		Env: map[string]string{
			"SUPER_DOLPHIN_POSTGRES_SHARE_DIR": "/opt/super-dolphin/postgres/share/postgresql",
		},
	}

	cfg, _ := ResolveConfig(input)
	if cfg.ShareDir != "/opt/super-dolphin/postgres/share/postgresql" {
		t.Fatalf("ShareDir = %q", cfg.ShareDir)
	}
}

func writePackagedRuntimeFixture(t *testing.T, resources, platform string) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(resources, "bin"),
		filepath.Join(resources, "lsp"),
		filepath.Join(resources, "postgres", platform, "bin"),
		filepath.Join(resources, "postgres", platform, "share", "postgresql@16"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir packaged runtime fixture %s: %v", path, err)
		}
	}
	for _, name := range []string{"codex", "gopls"} {
		path := filepath.Join(resources, "bin", name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write packaged runtime executable %s: %v", path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(resources, "models.yaml"), []byte("models: []\n"), 0o644); err != nil {
		t.Fatalf("write model registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resources, "lsp", "lsp-manifest.json"), []byte(`{"servers":{"gopls":{"path":"../bin/gopls","languages":["go"]}}}`), 0o644); err != nil {
		t.Fatalf("write lsp manifest: %v", err)
	}
	manifest := `{
  "bundled_codex_path": "bin/codex",
  "bundled_gopls_path": "bin/gopls",
  "lsp_bundle_path": "lsp",
  "lsp_manifest_path": "lsp/lsp-manifest.json",
  "model_registry_path": "models.yaml",
  "embedded_postgres_resource_path": "postgres/` + platform + `"
}`
	if err := os.WriteFile(filepath.Join(resources, "runtime-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
}
