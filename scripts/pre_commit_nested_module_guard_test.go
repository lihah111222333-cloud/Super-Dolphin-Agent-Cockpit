package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendGateChecksNestedGoModulesFromModuleRoot(t *testing.T) {
	t.Run("valid modules run list and vet from each module root", func(t *testing.T) {
		root := prepareNestedModuleGuardFixture(t, false)
		logPath := filepath.Join(root, "go.log")
		fakeGo := filepath.Join(root, "fake-go")
		writeFixTestGuardFile(t, root, "fake-go", "#!/bin/sh\nprintf '%s|%s\\n' \"$PWD\" \"$*\" >>\"$GO_LOG\"\n")
		if err := os.Chmod(fakeGo, 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := runNestedModuleGuard(t, root, fakeGo, logPath)
		if err != nil {
			t.Fatalf("nested module guard rejected valid modules: %v\n%s", err, out)
		}
		logData, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		log := string(logData)
		for _, module := range []string{"build/gate/runtime-tools", "build/gate/runtime-proxy"} {
			moduleRoot := filepath.Join(root, filepath.FromSlash(module))
			if strings.Count(log, moduleRoot+"|list ./...") != 1 || strings.Count(log, moduleRoot+"|vet ./...") != 1 {
				t.Fatalf("module %q was not checked exactly once from its root:\n%s", module, log)
			}
		}
	})

	t.Run("damaged nested module fails fast", func(t *testing.T) {
		root := prepareNestedModuleGuardFixture(t, true)
		realGo, err := exec.LookPath("go")
		if err != nil {
			t.Fatalf("resolve go executable: %v", err)
		}
		out, err := runNestedModuleGuard(t, root, realGo, filepath.Join(root, "unused.log"))
		if err == nil {
			t.Fatalf("nested module guard accepted damaged go.mod:\n%s", out)
		}
		assertOutputContainsAll(t, out, "nested go list: module=build/gate/runtime-tools", "go.mod")
		assertOutputOmitsAll(t, out, "nested go vet: module=build/gate/runtime-tools")
	})
}

func prepareNestedModuleGuardFixture(t *testing.T, damaged bool) string {
	t.Helper()
	root := t.TempDir()
	copyFixTestGuardRepoFile(t, root, "scripts/check_nested_go_modules.sh", 0o755)
	runFixTestGuardGit(t, root, "init", "-q")
	toolsModule := "module example.com/runtime-tools\n\ngo 1.24\n"
	if damaged {
		toolsModule = "module\n"
	}
	writeFixTestGuardFile(t, root, "build/gate/runtime-tools/go.mod", toolsModule)
	writeFixTestGuardFile(t, root, "build/gate/runtime-tools/tools.go", "package tools\n")
	writeFixTestGuardFile(t, root, "build/gate/runtime-proxy/go.mod", "module example.com/runtime-proxy\n\ngo 1.24\n")
	writeFixTestGuardFile(t, root, "build/gate/runtime-proxy/proxy.go", "package proxy\n")
	runFixTestGuardGit(t, root, "add", ".")
	return root
}

func runNestedModuleGuard(t *testing.T, root, goBinary, logPath string) (string, error) {
	t.Helper()
	command := exec.Command("bash", filepath.Join(root, "scripts/check_nested_go_modules.sh"), goBinary)
	command.Dir = root
	command.Env = append(os.Environ(), "GO_LOG="+logPath)
	output, err := command.CombinedOutput()
	return string(output), err
}
