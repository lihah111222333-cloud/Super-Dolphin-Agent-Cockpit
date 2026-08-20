//go:build windows

package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsGoSQLSCommandRunnerWritesReadableFailureTail(t *testing.T) {
	stage := filepath.Join(t.TempDir(), ".staging")
	if err := os.MkdirAll(filepath.Dir(stage), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := windowsGoSQLSCommandRunner(stage)
	err := runner(context.Background(), os.Getenv("ComSpec"), filepath.Dir(stage), []string{"/C", "echo SQLS-COMPILER-ERROR & exit /b 1"}, []string{"GOOS=windows", "GOARCH=arm64", "CGO_ENABLED=0"})
	if err == nil || !strings.Contains(err.Error(), "SQLS-COMPILER-ERROR") || !strings.Contains(err.Error(), "goarch=arm64") {
		t.Fatalf("runner error = %v, want readable compiler tail and target metadata", err)
	}
	logPath := filepath.Join(filepath.Dir(stage), filepath.Base(stage)+".sqls-build.log")
	contents, readErr := os.ReadFile(logPath)
	if readErr != nil || !strings.Contains(string(contents), "SQLS-COMPILER-ERROR") {
		t.Fatalf("diagnostic log = %q, err=%v", contents, readErr)
	}
}

func TestPruneWindowsGoSQLSBuildInputsQuarantinesBeforeBoundedCleanup(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, ".staging-1")
	sourceRoot := filepath.Join(stage, "github.com", "sql-server")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	goRoot := filepath.Join(stage, "go", "bin")
	if err := os.MkdirAll(goRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goRoot, "go.exe"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalRemoveAll := removeWindowsInstallerAll
	started := make(chan struct{})
	release := make(chan struct{})
	removeWindowsInstallerAll = func(path string) error {
		close(started)
		<-release
		return originalRemoveAll(path)
	}
	t.Cleanup(func() {
		removeWindowsInstallerAll = originalRemoveAll
		select {
		case <-release:
		default:
			close(release)
		}
	})

	startedAt := time.Now()
	if err := pruneWindowsGoSQLSBuildInputs(stage, sourceRoot); err != nil {
		t.Fatalf("pruneWindowsGoSQLSBuildInputs() error = %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("pruneWindowsGoSQLSBuildInputs() blocked for %s", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("deferred cleanup did not start")
	}
	if _, err := os.Stat(filepath.Join(stage, "go")); !os.IsNotExist(err) {
		t.Fatalf("Go build input still present after quarantine, err=%v", err)
	}
	close(release)
}

func TestWindowsGoSQLSBuildRootRejectsTrimmedRuntimeCohort(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "staging")
	trimmed := filepath.Join(stage, "go", "bin")
	if err := os.MkdirAll(trimmed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trimmed, "go.exe"), []byte("runtime-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := windowsGoSQLSBuildRoot(stage, WindowsHostArchARM64); err == nil || !strings.Contains(err.Error(), "src/context") {
		t.Fatalf("trimmed runtime cohort accepted: %v", err)
	}
	for _, relative := range []string{"src/context", "pkg/tool"} {
		if err := os.MkdirAll(filepath.Join(stage, "go", filepath.FromSlash(relative)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := windowsGoSQLSBuildRoot(stage, WindowsHostArchARM64)
	if err != nil || root != filepath.Join(stage, "go") {
		t.Fatalf("full staged SDK rejected: root=%q err=%v", root, err)
	}
}

func TestWindowsGoSQLSDiscoverFullGoSDKRejectsWrongVersionAndAcceptsManagedSDK(t *testing.T) {
	managed := os.Getenv("SUPER_DOLPHIN_GO_SDK_ROOT")
	if managed == "" {
		t.Skip("managed SDK root is supplied by the Windows product environment")
	}
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty-path"))
	t.Setenv("SUPER_DOLPHIN_GO_SDK_ROOT", filepath.Join(t.TempDir(), "missing"))
	if _, err := windowsGoSQLSDiscoverFullGoSDK(WindowsHostArchARM64); err == nil {
		t.Fatal("missing explicit SDK unexpectedly accepted")
	}
	t.Setenv("SUPER_DOLPHIN_GO_SDK_ROOT", managed)
	root, err := windowsGoSQLSDiscoverFullGoSDK(WindowsHostArchARM64)
	if err != nil || filepath.Clean(root) != filepath.Clean(managed) {
		t.Fatalf("managed full SDK rejected: root=%q err=%v", root, err)
	}
}
