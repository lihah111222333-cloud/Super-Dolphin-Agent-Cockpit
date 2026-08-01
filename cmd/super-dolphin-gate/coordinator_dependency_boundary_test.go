package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneGateDependencyBoundary(t *testing.T) {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	const module = "github.com/lihah111222333-cloud/super-dolphin-agent"
	dependencies := gatePackageDependencies(t, root, "./cmd/super-dolphin-gate")
	for _, dependency := range dependencies {
		if standaloneGateDependencyAllowed(module, dependency) {
			continue
		}
		t.Fatalf("standalone super-dolphin-gate depends on repository package outside its dedicated roots: %q", dependency)
	}
}

func standaloneGateDependencyAllowed(module, dependency string) bool {
	if !strings.HasPrefix(dependency, module+"/") {
		return true
	}
	for _, root := range []string{
		module + "/cmd/super-dolphin-gate",
		module + "/internal/devtools",
		module + "/internal/archtest",
		module + "/build/gate/closure",
	} {
		if dependency == root || strings.HasPrefix(dependency, root+"/") {
			return true
		}
	}
	return false
}

func TestStandaloneGateDependencyBoundaryRejectsProductionBackend(t *testing.T) {
	t.Parallel()
	const module = "github.com/lihah111222333-cloud/super-dolphin-agent"
	for _, dependency := range []string{
		module + "/internal/module/task",
		module + "/internal/platform/database",
		module + "/pkg/toolbridge",
	} {
		if standaloneGateDependencyAllowed(module, dependency) {
			t.Fatalf("production backend dependency unexpectedly allowed: %q", dependency)
		}
	}
}

func gatePackageDependencies(t *testing.T, root string, pkg string) []string {
	t.Helper()
	return gateList(t, root, "-deps", pkg)
}

func gateList(t *testing.T, root string, args ...string) []string {
	t.Helper()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate go toolchain: %v", err)
	}
	command := exec.Command(goBinary, "list")
	command.Args = append(command.Args, args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\\n%s", strings.Join(args, " "), err, output)
	}
	return strings.Fields(string(output))
}
