package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureCodexBootstrapWritesRelayConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex-home")
	cfg := CodexBootstrapConfig{
		Home:                home,
		RelayBaseURL:        "https://relay.example.test/v1",
		RelayBootstrapToken: "test-key",
		ModelProvider:       "super-dolphin-relay",
	}
	if err := EnsureCodexBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("EnsureCodexBootstrap() error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`model_provider = "super-dolphin-relay"`,
		`[model_providers."super-dolphin-relay"]`,
		`name = "Super Dolphin Relay"`,
		`base_url = "https://relay.example.test/v1"`,
		`env_key = "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "api_key") || strings.Contains(text, "test-key") {
		t.Fatalf("config.toml must not persist relay bootstrap token:\n%s", text)
	}
	assertCodexBootstrapMode(t, home, 0o700)
	assertCodexBootstrapMode(t, filepath.Join(home, "config.toml"), 0o600)
}

func TestEnsureCodexBootstrapDefaultsRelayModelProvider(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex-home")
	cfg := CodexBootstrapConfig{
		Home:                home,
		RelayBaseURL:        "https://relay.example.test/v1",
		RelayBootstrapToken: "test-key",
	}
	if err := EnsureCodexBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("EnsureCodexBootstrap() error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(raw), `model_provider = "super-dolphin-relay"`) {
		t.Fatalf("config.toml did not default model provider:\n%s", string(raw))
	}
}

func TestEnsureCodexBootstrapEscapesTOMLStrings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex-home")
	cfg := CodexBootstrapConfig{
		Home:                home,
		RelayBaseURL:        `https://relay.example.test/v1/"quoted"\path`,
		RelayBootstrapToken: `key-"quoted"-\path`,
		ModelProvider:       `relay-"quoted"-\path`,
	}
	if err := EnsureCodexBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("EnsureCodexBootstrap() error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`model_provider = "relay-\"quoted\"-\\path"`,
		`[model_providers."relay-\"quoted\"-\\path"]`,
		`base_url = "https://relay.example.test/v1/\"quoted\"\\path"`,
		`env_key = "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config.toml missing escaped %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, cfg.RelayBootstrapToken) {
		t.Fatalf("config.toml must not persist relay bootstrap token:\n%s", text)
	}
}

func TestEnsureCodexBootstrapRequiresHomeRelayURLAndKey(t *testing.T) {
	err := EnsureCodexBootstrap(context.Background(), CodexBootstrapConfig{})
	if err == nil {
		t.Fatal("EnsureCodexBootstrap() error = nil, want missing bootstrap config error")
	}
	msg := err.Error()
	for _, want := range []string{
		"Codex home is required",
		"Codex relay base URL is required",
		"Codex relay bootstrap token is required",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
}

func assertCodexBootstrapMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %o, want %o", path, got, want)
	}
}
