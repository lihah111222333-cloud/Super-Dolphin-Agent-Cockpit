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
		if !strings.HasPrefix(dependency, module+"/") {
			continue
		}
		if dependency == module+"/cmd/super-dolphin-gate" ||
			dependency == module+"/internal/devtools" ||
			strings.HasPrefix(dependency, module+"/internal/devtools/") ||
			dependency == module+"/build/gate/closure" ||
			strings.HasPrefix(dependency, module+"/build/gate/closure/") {
			continue
		}
		t.Fatalf("standalone super-dolphin-gate depends on repository package outside its dedicated roots: %q", dependency)
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
