package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestServeOptionsContract(t *testing.T) {
	root := t.TempDir()
	config, ready := filepath.Join(root, "config.json"), filepath.Join(root, "ready.json")
	options, err := parseServeOptions([]string{"--config", config, "--ready-file", ready})
	if err != nil || options.configPath != config || options.readyPath != ready {
		t.Fatalf("parse valid options = %#v, %v", options, err)
	}
	for _, args := range [][]string{
		{}, {"--unknown"}, {"--config", "relative", "--ready-file", ready},
		{"--config", config}, {"--config", config, "--ready-file", ready, "extra"},
		{"--config", config, "--config", config, "--ready-file", ready},
	} {
		if _, err := parseServeOptions(args); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestMonitorInstanceStateStopsOnReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows lock handle intentionally prevents state-root rename")
	}
	state := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(state, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := standalone.AcquireInstance(state)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	guardErr := monitorInstanceState(ctx, lock, time.Millisecond)
	moved := filepath.Join(t.TempDir(), "moved-state")
	if err := os.Rename(state, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-guardErr:
		if err == nil || !strings.Contains(err.Error(), "identity changed") {
			t.Fatalf("monitor error = %v, want state identity failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("state monitor did not detect replacement")
	}
}

// TestServeMigrationFailureReleasesResources 连续两次失败均须到达迁移错误，而非遗留实例锁。
func TestServeMigrationFailureReleasesResources(t *testing.T) {
	state, options := corruptStandaloneFixture(t)
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		err := runServe(ctx, options)
		cancel()
		if err == nil || !strings.Contains(err.Error(), "not a database") {
			t.Fatalf("attempt %d: want corrupt database failure, got %v", attempt, err)
		}
		lock, err := standalone.AcquireInstance(state)
		if err != nil {
			t.Fatalf("instance lock leaked after startup failure: %v", err)
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(options.readyPath); !os.IsNotExist(err) {
			t.Fatalf("failed startup published readiness: %v", err)
		}
	}
	// Windows 拒绝删除仍打开的 SQLite 文件，补充句柄释放的原生平台回归。
	if err := os.Remove(filepath.Join(state, "orch.sqlite")); err != nil {
		t.Fatalf("database file remained open after failed startup: %v", err)
	}
}

// TestServeIdentityFailsBeforeCredentialAndInstanceOwner 锁定真实启动的最早身份屏障。
func TestServeIdentityFailsBeforeCredentialAndInstanceOwner(t *testing.T) {
	root, options := invalidIdentityServeFixture(t)
	err := runServe(t.Context(), options)
	if err == nil || !strings.Contains(err.Error(), "principal_ref") || strings.Contains(err.Error(), "credential") {
		t.Fatalf("runServe(invalid identity)=%v, want principal failure before credential", err)
	}
	assertInvalidIdentityLeftNoOwner(t, root, options.readyPath)
}

func invalidIdentityServeFixture(t *testing.T) (string, serveOptions) {
	t.Helper()
	root := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(root, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := standalone.Config{
		SchemaVersion: 1, WorkspaceRoot: t.TempDir(), StateDir: root,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(root, "missing-token"),
		PrincipalRef: "", ScopeRef: "workspace/test",
		Providers: []standalone.Provider{{Name: "codex", Executable: executable, Home: t.TempDir(), Models: []string{"test-model"}, Sandbox: "read-only"}},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	options := serveOptions{configPath: filepath.Join(root, "config.json"), readyPath: filepath.Join(root, "ready.json")}
	if err := os.WriteFile(options.configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, options
}

func assertInvalidIdentityLeftNoOwner(t *testing.T, root, readyPath string) {
	t.Helper()
	lock, lockErr := standalone.AcquireInstance(root)
	if lockErr != nil {
		t.Fatalf("invalid identity acquired instance owner: %v", lockErr)
	}
	if closeErr := lock.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, statErr := os.Stat(readyPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid identity published ready: %v", statErr)
	}
}

// corruptStandaloneFixture 仅在测试私有目录构造损坏数据库，不触碰用户配置。
func corruptStandaloneFixture(t *testing.T) (string, serveOptions) {
	t.Helper()
	state := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(state, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(state, "token")
	if err := os.WriteFile(credentialPath, []byte("test-only-operator-token-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(credentialPath, 0o600); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(state, "orch.sqlite")
	if err := os.WriteFile(databasePath, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := standalone.Config{SchemaVersion: 1, WorkspaceRoot: t.TempDir(), StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: credentialPath,
		PrincipalRef: "operator/test", ScopeRef: "workspace/test",
		Providers: []standalone.Provider{{Name: "codex", Executable: executable, Home: t.TempDir(), Models: []string{"test-model"}, Sandbox: "read-only"}}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	options := serveOptions{configPath: filepath.Join(state, "config.json"), readyPath: filepath.Join(state, "ready.json")}
	if err := os.WriteFile(options.configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return state, options
}
