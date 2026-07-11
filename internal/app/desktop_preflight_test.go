package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
)

func TestRunDesktopPreflightDoesNotEnsureCodexCLIAvailable(t *testing.T) {
	t.Setenv("PROJECT_ROOT", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", "")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "")
	var called bool
	previous := deps
	deps.ensureCodexCLIAvailable = func(context.Context) error {
		called = true
		return errors.New("codex CLI check must not run during desktop preflight")
	}
	deps.codexAppManagedHome = func() (string, error) {
		t.Fatal("Codex home must not be resolved when relay config is unset")
		return "", nil
	}
	deps.ensureCodexBootstrap = func(context.Context, codexapp.CodexBootstrapConfig) error {
		t.Fatal("Codex bootstrap must not run when relay config is unset")
		return nil
	}
	t.Cleanup(func() { deps = previous })

	if err := runDesktopPreflight(context.Background()); err != nil {
		t.Fatalf("runDesktopPreflight() error = %v", err)
	}
	if called {
		t.Fatal("runDesktopPreflight() ensured Codex CLI availability; CLI checks belong to Codex start paths")
	}
}

func TestRunDesktopPreflightBootstrapsManagedCodexRelayConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write runtime manifest marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	home := filepath.Join(t.TempDir(), "sd-home", "providers", "codex")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "https://relay.example.test/v1")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", "test-token")

	previous := deps
	deps.ensureCodexCLIAvailable = func(context.Context) error {
		t.Fatal("Codex CLI check must not run during desktop preflight")
		return errors.New("unexpected Codex CLI check")
	}
	deps.codexAppManagedHome = func() (string, error) { return home, nil }
	var got codexapp.CodexBootstrapConfig
	deps.ensureCodexBootstrap = func(_ context.Context, cfg codexapp.CodexBootstrapConfig) error {
		got = cfg
		return nil
	}
	t.Cleanup(func() { deps = previous })

	if err := runDesktopPreflight(context.Background()); err != nil {
		t.Fatalf("runDesktopPreflight() error = %v", err)
	}
	if got.Home != home {
		t.Fatalf("bootstrap home = %q, want %q", got.Home, home)
	}
	if got.RelayBaseURL != "https://relay.example.test/v1" {
		t.Fatalf("bootstrap relay URL = %q", got.RelayBaseURL)
	}
	if got.RelayBootstrapToken != "test-token" {
		t.Fatalf("bootstrap relay bootstrap token = %q", got.RelayBootstrapToken)
	}
	if got.ModelProvider != "" {
		t.Fatalf("bootstrap model provider = %q, want empty for defaulting in codexapp", got.ModelProvider)
	}
}

func TestRunDesktopPreflightReturnsCodexBootstrapError(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write runtime manifest marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "https://relay.example.test/v1")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", "test-token")
	want := errors.New("relay config missing")
	previous := deps
	deps.ensureCodexCLIAvailable = func(context.Context) error {
		t.Fatal("Codex CLI check must not run during desktop preflight")
		return errors.New("unexpected Codex CLI check")
	}
	deps.codexAppManagedHome = func() (string, error) {
		return filepath.Join(t.TempDir(), "codex-home"), nil
	}
	deps.ensureCodexBootstrap = func(context.Context, codexapp.CodexBootstrapConfig) error { return want }
	t.Cleanup(func() { deps = previous })

	err := runDesktopPreflight(context.Background())
	if err == nil {
		t.Fatal("runDesktopPreflight() error = nil, want bootstrap failure")
	}
	if !strings.Contains(err.Error(), "desktop preflight: Codex bootstrap failed") || !errors.Is(err, want) {
		t.Fatalf("runDesktopPreflight() error = %v, want wrapped bootstrap failure", err)
	}
}

func TestRunDesktopPreflightSkipsCodexBootstrapWhenRelayUnset(t *testing.T) {
	t.Setenv("PROJECT_ROOT", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", "")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "")
	previous := deps
	deps.ensureCodexCLIAvailable = func(context.Context) error {
		t.Fatal("Codex CLI check must not run during desktop preflight")
		return errors.New("unexpected Codex CLI check")
	}
	deps.codexAppManagedHome = func() (string, error) {
		t.Fatal("Codex home must not be resolved when relay config is unset")
		return "", nil
	}
	deps.ensureCodexBootstrap = func(context.Context, codexapp.CodexBootstrapConfig) error {
		t.Fatal("Codex bootstrap must not run when relay config is unset")
		return nil
	}
	t.Cleanup(func() { deps = previous })

	if err := runDesktopPreflight(context.Background()); err != nil {
		t.Fatalf("runDesktopPreflight() error = %v", err)
	}
}

func TestRunDesktopPreflightFailsFastInPackagedRuntimeWhenRelayUnset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", "")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "")
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write runtime manifest marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	previous := deps
	deps.codexAppManagedHome = func() (string, error) {
		t.Fatal("Codex home must not be resolved when packaged relay config is missing")
		return "", nil
	}
	deps.ensureCodexBootstrap = func(context.Context, codexapp.CodexBootstrapConfig) error {
		t.Fatal("Codex bootstrap must not run when packaged relay config is missing")
		return nil
	}
	t.Cleanup(func() { deps = previous })

	err := runDesktopPreflight(context.Background())
	if err == nil {
		t.Fatal("runDesktopPreflight() error = nil, want packaged relay config failure")
	}
	for _, want := range []string{
		"desktop preflight: Codex bootstrap failed",
		"packaged Codex relay config missing",
		"SUPER_DOLPHIN_CODEX_RELAY_BASE_URL",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN",
		filepath.Join(root, ".env"),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("runDesktopPreflight() error = %v, want substring %q", err, want)
		}
	}
}

func TestRunDesktopPreflightReturnsPackagedDotEnvErrors(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, root string)
		want    []string
	}{
		{
			name: "missing",
			want: []string{"desktop preflight: load environment", "load packaged .env", ".env"},
		},
		{
			name: "unreadable",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, ".env"), 0o755); err != nil {
					t.Fatalf("mkdir .env: %v", err)
				}
			},
			want: []string{"desktop preflight: load environment", "load packaged .env", ".env"},
		},
		{
			name: "malformed",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL\n"), 0o600); err != nil {
					t.Fatalf("write .env: %v", err)
				}
			},
			want: []string{"desktop preflight: load environment", "parse packaged .env", "line 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PROJECT_ROOT", root)
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "")
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", "")
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "")
			if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
				t.Fatalf("write runtime manifest marker: %v", err)
			}
			if tt.prepare != nil {
				tt.prepare(t, root)
			}
			previous := deps
			deps.codexAppManagedHome = func() (string, error) {
				t.Fatal("Codex home must not be resolved when packaged .env is invalid")
				return "", nil
			}
			deps.ensureCodexBootstrap = func(context.Context, codexapp.CodexBootstrapConfig) error {
				t.Fatal("Codex bootstrap must not run when packaged .env is invalid")
				return nil
			}
			t.Cleanup(func() { deps = previous })

			err := runDesktopPreflight(context.Background())
			if err == nil {
				t.Fatal("runDesktopPreflight() error = nil, want packaged .env failure")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("runDesktopPreflight() error = %v, want substring %q", err, want)
				}
			}
		})
	}
}

func TestRunDesktopPreflightIgnoresResidualRelayEnvOutsidePackagedRuntime(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		token   string
		apiKey  string
	}{
		{name: "missing token", baseURL: "https://relay.example.test/v1"},
		{name: "missing base URL", token: "test-key"},
		{name: "privileged api key", baseURL: "https://relay.example.test/v1", apiKey: "privileged-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PROJECT_ROOT", t.TempDir())
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", tt.baseURL)
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", tt.token)
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", tt.apiKey)
			previous := deps
			deps.codexAppManagedHome = func() (string, error) {
				t.Fatal("Codex home must not be resolved outside packaged desktop preflight")
				return "", nil
			}
			deps.ensureCodexBootstrap = func(context.Context, codexapp.CodexBootstrapConfig) error {
				t.Fatal("Codex bootstrap must not run outside packaged desktop preflight")
				return nil
			}
			t.Cleanup(func() { deps = previous })

			if err := runDesktopPreflight(context.Background()); err != nil {
				t.Fatalf("runDesktopPreflight() error = %v, want nil outside packaged runtime", err)
			}
		})
	}
}

func TestRunDesktopPreflightFailsFastInPackagedRuntimeWhenRelayPartiallySet(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		token   string
		want    string
	}{
		{
			name:    "missing bootstrap token",
			baseURL: "https://relay.example.test/v1",
			want:    "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is required when SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is set",
		},
		{
			name:  "missing base URL",
			token: "test-token",
			want:  "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is required when SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PROJECT_ROOT", root)
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", tt.baseURL)
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", tt.token)
			t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "")
			if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
				t.Fatalf("write runtime manifest marker: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, ".env"), []byte(""), 0o600); err != nil {
				t.Fatalf("write .env: %v", err)
			}
			previous := deps
			deps.codexAppManagedHome = func() (string, error) {
				t.Fatal("Codex home must not be resolved when packaged relay config is incomplete")
				return "", nil
			}
			deps.ensureCodexBootstrap = func(context.Context, codexapp.CodexBootstrapConfig) error {
				t.Fatal("Codex bootstrap must not run when packaged relay config is incomplete")
				return nil
			}
			t.Cleanup(func() { deps = previous })

			err := runDesktopPreflight(context.Background())
			if err == nil {
				t.Fatal("runDesktopPreflight() error = nil, want incomplete relay config error")
			}
			if !strings.Contains(err.Error(), "desktop preflight: Codex bootstrap failed") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("runDesktopPreflight() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunDesktopPreflightLoadsDotEnvBeforeCodexBootstrap(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write runtime manifest marker: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", "")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.test/v1\nSUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=test-token\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	previous := deps
	deps.ensureCodexCLIAvailable = func(context.Context) error {
		t.Fatal("Codex CLI check must not run during desktop preflight")
		return errors.New("unexpected Codex CLI check")
	}
	deps.codexAppManagedHome = func() (string, error) {
		return filepath.Join(t.TempDir(), "codex-home"), nil
	}
	var got codexapp.CodexBootstrapConfig
	deps.ensureCodexBootstrap = func(_ context.Context, cfg codexapp.CodexBootstrapConfig) error {
		got = cfg
		return nil
	}
	t.Cleanup(func() { deps = previous })

	if err := runDesktopPreflight(context.Background()); err != nil {
		t.Fatalf("runDesktopPreflight() error = %v", err)
	}
	if got.RelayBaseURL != "https://relay.example.test/v1" || got.RelayBootstrapToken != "test-token" {
		t.Fatalf("bootstrap config = %#v, want relay values loaded from .env", got)
	}
}

func TestRunDesktopPreflightRejectsPrivilegedRelayAPIKeyEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write runtime manifest marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "https://relay.example.test/v1")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "privileged-key")
	previous := deps
	deps.codexAppManagedHome = func() (string, error) {
		t.Fatal("Codex home must not be resolved when privileged relay API key env is set")
		return "", nil
	}
	deps.ensureCodexBootstrap = func(context.Context, codexapp.CodexBootstrapConfig) error {
		t.Fatal("Codex bootstrap must not run when privileged relay API key env is set")
		return nil
	}
	t.Cleanup(func() { deps = previous })

	err := runDesktopPreflight(context.Background())
	if err == nil {
		t.Fatal("runDesktopPreflight() error = nil, want privileged relay API key env rejection")
	}
	if !strings.Contains(err.Error(), "SUPER_DOLPHIN_CODEX_RELAY_API_KEY is a privileged relay API key env and must not be packaged") {
		t.Fatalf("runDesktopPreflight() error = %v, want privileged relay API key env rejection", err)
	}
}
