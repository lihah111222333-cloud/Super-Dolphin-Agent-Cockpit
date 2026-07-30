package remoteci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildWorkloadInventoryUsesExactCommitAndRange(t *testing.T) {
	repository := t.TempDir()
	runInventoryGit(t, repository, "init", "--quiet")
	runInventoryGit(t, repository, "config", "user.name", "CI Inventory")
	runInventoryGit(t, repository, "config", "user.email", "ci@example.invalid")
	writeInventoryFile(t, repository, "go.mod", "module example.test/root\n\ngo 1.25\n")
	writeInventoryFile(t, repository, "build/gate/closure_test.go", "package gate_test\n")
	writeInventoryFile(t, repository, "build/gate/closure/runtime_deps_test.go", "package gateclosure\n")
	writeInventoryFile(t, repository, "build/gate/runtime-tools/go.mod", "module example.test/runtime-tools\n\ngo 1.25\n")
	writeInventoryFile(t, repository, "build/gate/runtime-tools/tools.go", "package tools\n")
	writeInventoryFile(t, repository, "internal/alpha/alpha.go", "package alpha\n")
	writeInventoryFile(t, repository, "internal/ignored/tool.go", "//go:build ignore\n\npackage main\n")
	writeInventoryFile(t, repository, "internal/archtest/common_test.go", "package archtest\n\nimport \"testing\"\n\nfunc TestCommon(t *testing.T) {}\n")
	writeInventoryFile(t, repository, "internal/archtest/normal_test.go", "//go:build !race\n\npackage archtest\n\nimport \"testing\"\n\nfunc TestNormal(t *testing.T) {}\n")
	writeInventoryFile(t, repository, "internal/archtest/race_test.go", "//go:build race\n\npackage archtest\n\nimport \"testing\"\n\nfunc TestRace(t *testing.T) {}\n")
	writeInventoryFile(t, repository, "new-root/tool/tool.go", "package tool\n")
	writeInventoryFile(t, repository, "tools/custom-check/go.mod", "module example.test/custom-check\n\ngo 1.25\n")
	writeInventoryFile(t, repository, "tools/custom-check/check.go", "package check\n")
	writeInventoryFile(t, repository, "frontend-app/src/widget.ts", "export const widget = 1\n")
	writeInventoryFile(t, repository, "frontend-app/src/widget.test.ts", "test('widget', () => {})\n")
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "基础")
	base := inventoryGitOutput(t, repository, "rev-parse", "HEAD")

	writeInventoryFile(t, repository, "frontend-app/src/widget.ts", "export const widget = 2\n")
	writeInventoryFile(t, repository, "internal/beta/beta_test.go", "package beta\n")
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "更新")
	commit := inventoryGitOutput(t, repository, "rev-parse", "HEAD")

	inventory, err := BuildWorkloadInventory(context.Background(), repository, commit, base, "linux/amd64")
	if err != nil {
		t.Fatalf("BuildWorkloadInventory() error = %v", err)
	}
	if !slices.Equal(inventory.GoPackages, []string{"./build/gate", "./build/gate/closure", "./internal/alpha", "./internal/archtest", "./internal/beta", "./new-root/tool"}) ||
		!slices.Equal(inventory.NestedGoModules, []string{"build/gate/runtime-tools", "tools/custom-check"}) ||
		!slices.Equal(inventory.FrontendChangedTests, []string{"src/widget.test.ts"}) ||
		!slices.Equal(inventory.FrontendFullTests, []string{"src/widget.test.ts"}) {
		t.Fatalf("inventory = %#v", inventory)
	}
	normalNames := make([]string, len(inventory.GoTests))
	for index, target := range inventory.GoTests {
		normalNames[index] = target.Package + "#" + target.Name
	}
	raceNames := make([]string, len(inventory.GoRaceTests))
	for index, target := range inventory.GoRaceTests {
		raceNames[index] = target.Package + "#" + target.Name
	}
	if !slices.Equal(normalNames, []string{"./internal/archtest#TestCommon", "./internal/archtest#TestNormal"}) ||
		!slices.Equal(raceNames, []string{"./internal/archtest#TestCommon", "./internal/archtest#TestRace"}) {
		t.Fatalf("atomic Go tests normal=%v race=%v", normalNames, raceNames)
	}
}

func runInventoryGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func inventoryGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repository
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func writeInventoryFile(t *testing.T, repository string, relative string, contents string) {
	t.Helper()
	target := filepath.Join(repository, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
