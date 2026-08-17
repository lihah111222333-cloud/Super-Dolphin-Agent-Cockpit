package logger

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsurePrivateLogDirCreatesAndTightensPermissions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "logs")
	if err := ensurePrivateLogDir(dir); err != nil {
		t.Fatalf("ensure private log dir: %v", err)
	}
	assertPrivateFileMode(t, dir, 0o700)

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("widen log dir permissions: %v", err)
	}
	if err := ensurePrivateLogDir(dir); err != nil {
		t.Fatalf("tighten private log dir: %v", err)
	}
	assertPrivateFileMode(t, dir, 0o700)
}

func TestOpenPrivateAppendFileCreatesAndTightensPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := ensurePrivateLogDir(dir); err != nil {
		t.Fatalf("ensure private log dir: %v", err)
	}
	path := filepath.Join(dir, "app.log")
	f, err := openPrivateAppendFile(path)
	if err != nil {
		t.Fatalf("open private append file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close private append file: %v", err)
	}
	assertPrivateFileMode(t, path, 0o600)

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("widen log file permissions: %v", err)
	}
	f, err = openPrivateAppendFile(path)
	if err != nil {
		t.Fatalf("tighten private append file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close tightened private append file: %v", err)
	}
	assertPrivateFileMode(t, path, 0o600)
}

func TestOpenPrivateAppendFilePropagatesOpenFailure(t *testing.T) {
	_, err := openPrivateAppendFile(t.TempDir())
	if err == nil {
		t.Fatal("open private append file error = nil, want directory open failure")
	}
}

func TestInitAndAgentLogFilesUsePrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	runtime := NewRuntime(RuntimeConfig{Mode: Production, Level: slog.LevelInfo})
	if err := runtime.InitWithFile(dir); err != nil {
		t.Fatalf("init file logger: %v", err)
	}
	t.Cleanup(runtime.ShutdownFileHandler)

	assertPrivateFileMode(t, dir, 0o700)
	assertPrivateFileMode(t, runtime.logFilePath, 0o600)

	agentLogger, err := runtime.NewAgentLogger("private-agent")
	if err != nil {
		t.Fatalf("new private agent logger: %v", err)
	}
	agentLogger.Info("private agent log")
	agentPath := filepath.Join(dir, "agent-private-agent.log")
	assertPrivateFileMode(t, agentPath, 0o600)
}

func TestNewAgentLoggerWithoutFileModeSucceeds(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Mode: Production, Level: slog.LevelInfo})
	if _, err := runtime.NewAgentLogger("memory-only-agent"); err != nil {
		t.Fatalf("new agent logger without file mode: %v", err)
	}
}

func TestNewAgentLoggerPropagatesReusedFileChmodFailure(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Mode: Production, Level: slog.LevelInfo})
	if err := runtime.InitWithFile(t.TempDir()); err != nil {
		t.Fatalf("init file logger: %v", err)
	}
	t.Cleanup(runtime.ShutdownFileHandler)
	if _, err := runtime.NewAgentLogger("closed-agent"); err != nil {
		t.Fatalf("new initial agent logger: %v", err)
	}
	if err := runtime.agentFiles["closed-agent"].Close(); err != nil {
		t.Fatalf("close agent file: %v", err)
	}
	if _, err := runtime.NewAgentLogger("closed-agent"); err == nil {
		t.Fatal("new agent logger error = nil, want chmod failure from closed file")
	}
}

func TestReopenLogFileRebuildsPrivateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	runtime := NewRuntime(RuntimeConfig{Mode: Production, Level: slog.LevelInfo})
	path := filepath.Join(dir, "watchdog.log")
	if err := runtime.reopenLogFile(path); err != nil {
		t.Fatalf("reopen log file: %v", err)
	}
	t.Cleanup(runtime.ShutdownFileHandler)
	assertPrivateFileMode(t, dir, 0o700)
	assertPrivateFileMode(t, path, 0o600)
}

func TestReopenLogFilePropagatesOpenFailure(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Mode: Production, Level: slog.LevelInfo})
	if err := runtime.reopenLogFile(t.TempDir()); err == nil {
		t.Fatal("reopen log file error = nil, want directory open failure")
	}
}

func assertPrivateFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
