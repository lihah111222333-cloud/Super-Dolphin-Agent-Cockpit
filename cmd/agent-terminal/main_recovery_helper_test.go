package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

func prepareAgentTerminalProductionTestHelper() (func() error, error) {
	_, sourcePath, _, ok := goruntime.Caller(0)
	if !ok {
		return nil, errors.New("resolve source path for production helper")
	}
	moduleRoot, err := agentTerminalModuleRoot(sourcePath)
	if err != nil {
		return nil, err
	}
	tmpRoot := filepath.Join(moduleRoot, ".tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create production helper temp root: %w", err)
	}
	dir, err := os.MkdirTemp(tmpRoot, "agent-terminal-production-helper-")
	if err != nil {
		return nil, fmt.Errorf("create production helper directory: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(agentTerminalReleaseFilesystemHelperSource), 0o600); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	artifact := filepath.Join(dir, "release-helper")
	command := exec.Command("go", "build", "-trimpath", "-o", artifact, source)
	command.Dir = moduleRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("build production helper: %w: %s", err, output), cleanup())
	}
	previous, hadPrevious := os.LookupEnv(agentTerminalFilesystemHelperExecutableEnv)
	if err := os.Setenv(agentTerminalFilesystemHelperExecutableEnv, artifact); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	return func() error {
		return errors.Join(
			restoreAgentTerminalTestEnv(agentTerminalFilesystemHelperExecutableEnv, previous, hadPrevious),
			cleanup(),
		)
	}, nil
}

func agentTerminalModuleRoot(sourcePath string) (string, error) {
	if sourcePath == "" {
		return "", errors.New("production helper source path is required")
	}
	dir := filepath.Dir(sourcePath)
	if !filepath.IsAbs(sourcePath) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory for trimmed source path: %w", err)
		}
		dir = cwd
	}
	for {
		goMod := filepath.Join(dir, "go.mod")
		info, err := os.Stat(goMod)
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return "", fmt.Errorf("module go.mod is not a non-empty regular file: %s", goMod)
			}
			return dir, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect module go.mod: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("module go.mod was not found from production helper source %q", sourcePath)
		}
		dir = parent
	}
}

func TestAgentTerminalModuleRootRequiresValidGoMod(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "nested", "main.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := agentTerminalModuleRoot(sourcePath); err == nil {
		t.Fatal("module root resolver accepted missing go.mod")
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := agentTerminalModuleRoot(sourcePath)
	if err != nil || got != root {
		t.Fatalf("agentTerminalModuleRoot() = %q, %v; want %q", got, err, root)
	}
}

func restoreAgentTerminalTestEnv(key, value string, present bool) error {
	if present {
		return os.Setenv(key, value)
	}
	return os.Unsetenv(key)
}

const agentTerminalReleaseFilesystemHelperSource = `package main

import (
	"os"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func main() {
	handled, err := recovery.RunReleaseFilesystemHelperIfRequested(os.Stdin, os.Stdout)
	if !handled || err != nil {
		os.Exit(2)
	}
}
`
