package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendGateChecksNestedGoModulesFromModuleRoot(t *testing.T) {
	t.Run("valid modules run list and vet from each module root", testValidNestedModuleRoots)
	t.Run("exact tracked module runs alone", testExactNestedModuleRoot)
	t.Run("untracked exact module fails fast", testUntrackedNestedModuleRoot)
	t.Run("fixed worker git survives PATH reset", testNestedModuleFixedWorkerGit)
	t.Run("damaged nested module fails fast", testDamagedNestedModule)
}

func testExactNestedModuleRoot(t *testing.T) {
	root := prepareNestedModuleGuardFixture(t, false)
	logPath := filepath.Join(root, "go.log")
	fakeGo := filepath.Join(root, "fake-go")
	writeFixTestGuardFile(t, root, "fake-go", "#!/bin/sh\nprintf '%s|%s\\n' \"$PWD\" \"$*\" >>\"$GO_LOG\"\n")
	if err := os.Chmod(fakeGo, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runNestedModuleGuard(t, root, fakeGo, logPath, "build/gate/runtime-proxy")
	if err != nil {
		t.Fatalf("nested module guard rejected exact tracked module: %v\n%s", err, out)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logData)
	proxyRoot := filepath.Join(root, "build/gate/runtime-proxy")
	if strings.Count(log, proxyRoot+"|list ./...") != 1 || strings.Count(log, proxyRoot+"|vet ./...") != 1 || strings.Contains(log, "runtime-tools") {
		t.Fatalf("exact nested module selection drifted:\n%s", log)
	}
}

func testUntrackedNestedModuleRoot(t *testing.T) {
	root := prepareNestedModuleGuardFixture(t, false)
	fakeGo := filepath.Join(root, "fake-go")
	writeFixTestGuardFile(t, root, "fake-go", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(fakeGo, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runNestedModuleGuard(t, root, fakeGo, filepath.Join(root, "unused.log"), "third_party/missing")
	if err == nil || !strings.Contains(out, "requested nested Go module is not tracked") {
		t.Fatalf("nested module guard accepted untracked exact module: %v\n%s", err, out)
	}
}

func testValidNestedModuleRoots(t *testing.T) {
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
	assertNestedModuleCommands(t, root, string(logData))
}

func assertNestedModuleCommands(t *testing.T, root string, log string) {
	t.Helper()
	for _, module := range []string{"build/gate/runtime-tools", "build/gate/runtime-proxy"} {
		moduleRoot := filepath.Join(root, filepath.FromSlash(module))
		if strings.Count(log, moduleRoot+"|list ./...") != 1 || strings.Count(log, moduleRoot+"|vet ./...") != 1 {
			t.Fatalf("module %q was not checked exactly once from its root:\n%s", module, log)
		}
	}
}

func testNestedModuleFixedWorkerGit(t *testing.T) {
	root := prepareNestedModuleGuardFixture(t, false)
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("resolve git executable: %v", err)
	}
	fakeGo := filepath.Join(root, "fake-go")
	writeFixTestGuardFile(t, root, "fake-go", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(fakeGo, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", filepath.Join(root, "scripts/check_nested_go_modules.sh"), fakeGo)
	command.Dir = root
	command.Env = []string{"PATH=/usr/bin:/bin", "SUPER_DOLPHIN_GATE_GIT=" + gitBinary}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("nested module guard did not use fixed worker git: %v\n%s", err, output)
	}
}

func testDamagedNestedModule(t *testing.T) {
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

func runNestedModuleGuard(t *testing.T, root, goBinary, logPath string, modules ...string) (string, error) {
	t.Helper()
	args := append([]string{filepath.Join(root, "scripts/check_nested_go_modules.sh"), goBinary}, modules...)
	command := exec.Command("bash", args...)
	command.Dir = root
	command.Env = append(os.Environ(), "GO_LOG="+logPath)
	output, err := command.CombinedOutput()
	return string(output), err
}
