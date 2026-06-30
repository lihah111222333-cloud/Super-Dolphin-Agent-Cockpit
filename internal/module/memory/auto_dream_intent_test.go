package memory

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestAutoDreamIntentRoundTrip(t *testing.T) {
	root := t.TempDir()

	assertAutoDreamIntentMissing(t, root)
	writeAutoDreamIntent(t, root, true)
	assertAutoDreamIntentValue(t, root, true, "after true")
	writeAutoDreamIntent(t, root, false)
	assertAutoDreamIntentValue(t, root, false, "after false")
}

func TestAutoDreamIntentEmptyRootDir(t *testing.T) {
	if got, err := ReadAutoDreamIntent(""); err != nil || got != nil {
		t.Fatalf("ReadAutoDreamIntent(\"\") = %v err=%v, want nil nil", got, err)
	}
	if err := WriteAutoDreamIntent("", true); err == nil {
		t.Fatal("WriteAutoDreamIntent(\"\") error = nil, want non-nil")
	}
}

func TestAutoDreamIntentMissingIsNotDiagnostic(t *testing.T) {
	root := t.TempDir()
	projectRoot := newTestGitProjectRoot(t)
	t.Setenv(envMemoryRoot, root)
	t.Setenv(envClaudeRemoteMemoryDir, "")

	got, err := ReadAutoDreamIntent(root)
	if err != nil || got != nil {
		t.Fatalf("ReadAutoDreamIntent(missing) = %v err=%v, want nil nil", got, err)
	}
	cfg := NewConfig(&contract.Config{ProjectRoot: projectRoot})
	if cfg.AutoDreamIntentError != "" {
		t.Fatalf("AutoDreamIntentError = %q, want empty for missing intent", cfg.AutoDreamIntentError)
	}
	overview := buildAutoDreamIntentOverviewForTest(t, cfg, projectRoot)
	if overview.AutoDreamIntent != nil {
		t.Fatalf("Overview.AutoDreamIntent = %v, want nil for missing intent", *overview.AutoDreamIntent)
	}
	if overview.AutoDreamIntentError != "" {
		t.Fatalf("Overview.AutoDreamIntentError = %q, want empty for missing intent", overview.AutoDreamIntentError)
	}
}

func TestAutoDreamIntentBadJSONSurfacesDiagnostics(t *testing.T) {
	root := t.TempDir()
	projectRoot := newTestGitProjectRoot(t)
	t.Setenv(envMemoryRoot, root)
	t.Setenv(envClaudeRemoteMemoryDir, "")
	t.Setenv(envMemoryExtractOnStop, "1")
	writeRawAutoDreamIntent(t, root, "{")

	got, err := ReadAutoDreamIntent(root)
	if err == nil || got != nil {
		t.Fatalf("ReadAutoDreamIntent(bad JSON) = %v err=%v, want nil non-nil error", got, err)
	}
	cfg := NewConfig(&contract.Config{ProjectRoot: projectRoot})
	if cfg.AutoDreamIntentError == "" {
		t.Fatal("AutoDreamIntentError = empty, want bad JSON diagnostic")
	}
	if cfg.ExtractOnStop {
		t.Fatal("ExtractOnStop = true after malformed intent, want fail-closed false")
	}
	overview := buildAutoDreamIntentOverviewForTest(t, cfg, projectRoot)
	if overview.AutoDreamIntent != nil {
		t.Fatalf("Overview.AutoDreamIntent = %v, want nil when intent is invalid", *overview.AutoDreamIntent)
	}
	if overview.AutoDreamIntentError == "" {
		t.Fatal("Overview.AutoDreamIntentError = empty, want bad JSON diagnostic")
	}
}

func TestAutoDreamIntentUnreadableFileSurfacesDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based unreadable file setup is not reliable on windows")
	}
	root := t.TempDir()
	projectRoot := newTestGitProjectRoot(t)
	t.Setenv(envMemoryRoot, root)
	t.Setenv(envClaudeRemoteMemoryDir, "")
	writeRawAutoDreamIntent(t, root, `{"enabled":true}`)
	path := autoDreamIntentPath(root)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("Chmod(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0o644)
	})

	got, err := ReadAutoDreamIntent(root)
	if err == nil {
		t.Skipf("platform allowed reading chmod 000 intent file; got %v", got)
	}
	if got != nil {
		t.Fatalf("ReadAutoDreamIntent(unreadable) = %v err=%v, want nil non-nil error", got, err)
	}
	cfg := NewConfig(&contract.Config{ProjectRoot: projectRoot})
	if cfg.AutoDreamIntentError == "" {
		t.Fatal("AutoDreamIntentError = empty, want unreadable-file diagnostic")
	}
	overview := buildAutoDreamIntentOverviewForTest(t, cfg, projectRoot)
	if overview.AutoDreamIntent != nil {
		t.Fatalf("Overview.AutoDreamIntent = %v, want nil when intent is unreadable", *overview.AutoDreamIntent)
	}
	if overview.AutoDreamIntentError == "" {
		t.Fatal("Overview.AutoDreamIntentError = empty, want unreadable-file diagnostic")
	}
}

func TestSetAutoDreamIntentRPCPersists(t *testing.T) {
	root := t.TempDir()
	projectRoot := newTestGitProjectRoot(t)
	cfg := &Config{
		Enabled:     true,
		EnableTools: true,
		RootDir:     root,
		ProjectRoot: projectRoot,
	}
	deps := memoryHandlerDeps{
		Service: newServiceWithConsolidator(cfg, nil, nil, nil),
	}
	intentRoot, err := resolvedStoreRoot(root, projectRoot, "")
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}

	resp, err := setAutoDreamIntent(context.Background(), deps, uiAutoDreamIntentParams{CWD: projectRoot, Enabled: true})
	if err != nil {
		t.Fatalf("setAutoDreamIntent(true) error = %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("setAutoDreamIntent(true) resp = %#v, want ok=true", resp)
	}
	got, err := ReadAutoDreamIntent(intentRoot)
	if err != nil || got == nil || *got != true {
		t.Fatalf("ReadAutoDreamIntent after RPC = %v err=%v, want *true", got, err)
	}

	if _, err := setAutoDreamIntent(context.Background(), deps, uiAutoDreamIntentParams{CWD: projectRoot, Enabled: false}); err != nil {
		t.Fatalf("setAutoDreamIntent(false) error = %v", err)
	}
	got, _ = ReadAutoDreamIntent(intentRoot)
	if got == nil || *got != false {
		t.Fatalf("ReadAutoDreamIntent after RPC(false) = %v, want *false", got)
	}
}

func TestSetAutoDreamIntentRPCRequiresCWD(t *testing.T) {
	root := t.TempDir()
	deps := memoryHandlerDeps{
		Service: newServiceWithConsolidator(&Config{Enabled: true, RootDir: root}, nil, nil, nil),
	}

	_, err := setAutoDreamIntent(context.Background(), deps, uiAutoDreamIntentParams{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("setAutoDreamIntent(empty cwd) error = %v, want cwd required", err)
	}
}

func writeAutoDreamIntent(t *testing.T, root string, enabled bool) {
	t.Helper()
	if err := WriteAutoDreamIntent(root, enabled); err != nil {
		t.Fatalf("WriteAutoDreamIntent(%v) error = %v", enabled, err)
	}
}

func assertAutoDreamIntentMissing(t *testing.T, root string) {
	t.Helper()
	if got, err := ReadAutoDreamIntent(root); err != nil || got != nil {
		t.Fatalf("ReadAutoDreamIntent(missing) = %v err=%v, want nil nil", got, err)
	}
}

func assertAutoDreamIntentValue(t *testing.T, root string, want bool, label string) {
	t.Helper()
	got, err := ReadAutoDreamIntent(root)
	if err != nil || got == nil || *got != want {
		t.Fatalf("ReadAutoDreamIntent(%s) = %v err=%v, want *%v", label, got, err, want)
	}
}

func writeRawAutoDreamIntent(t *testing.T, root string, raw string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	if err := os.WriteFile(autoDreamIntentPath(root), []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(auto dream intent) error = %v", err)
	}
}

func buildAutoDreamIntentOverviewForTest(t *testing.T, cfg *Config, projectRoot string) UIMemoryOverview {
	t.Helper()
	privateRoot := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	cfg.AutoMemPathOverride = privateRoot
	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	return snapshot.Overview
}
