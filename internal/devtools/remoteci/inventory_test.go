package remoteci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func inventoryExpectedSelectors() ([]string, []gate.GoTestTarget, []gate.GoTestTarget) {
	packages := []string{"./build/gate", "./build/gate/closure", "./cmd/agent-runtime", "./cmd/agent-terminal", "./cmd/mcp-lsp", "./cmd/mcp-orch/store/taskdag", "./cmd/super-dolphin-updater", "./internal/alpha", "./internal/app", "./internal/archtest", "./internal/devtools/gate", "./internal/devtools/remoteci", "./internal/platform/db/sqlite", "./internal/provider/codexapp", "./new-root/tool"}
	tests := []gate.GoTestTarget{
		{Package: "./internal/archtest", Name: "TestCommon"}, {Package: "./internal/archtest", Name: "TestNormal"},
		{Package: "./internal/provider/codexapp", Name: "TestTransportCommon"}, {Package: "./internal/provider/codexapp", Name: "TestTransportNormal"},
		{Package: "./cmd/agent-runtime", Name: "TestAgentRuntimeMain"}, {Package: "./cmd/agent-runtime", Name: "TestAgentRuntimeRace"},
		{Package: "./cmd/agent-terminal", Name: "TestAgentTerminalMain"}, {Package: "./cmd/agent-terminal", Name: "TestAgentTerminalRecovery"},
		{Package: "./internal/app", Name: "TestAppModuleGraphIsClosed"}, {Package: "./internal/app", Name: "TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch"}, {Package: "./internal/app", Name: "TestRunDesktopPreDrain"},
		{Package: "./cmd/super-dolphin-updater", Name: "TestUpdaterCandidateCleanup"}, {Package: "./cmd/super-dolphin-updater", Name: "TestUpdaterRollbackEntries"},
		{Package: "./cmd/mcp-orch/store/taskdag", Name: "TestTaskDAGStore"}, {Package: "./cmd/mcp-orch/store/taskdag", Name: "TestTaskDAGWakeup"},
		{Package: "./internal/platform/db/sqlite", Name: "TestSQLiteCommon"}, {Package: "./internal/platform/db/sqlite", Name: "TestSQLiteNormal"},
		{Package: "./internal/devtools/gate", Name: "TestGateAtomicCommon"}, {Package: "./internal/devtools/gate", Name: "TestGateAtomicNormal"},
		{Package: "./internal/devtools/remoteci", Name: "TestRemoteCIAtomicCommon"}, {Package: "./internal/devtools/remoteci", Name: "TestRemoteCIAtomicNormal"},
		{Package: "./cmd/mcp-lsp", Name: "TestMcpLSPCommon"}, {Package: "./cmd/mcp-lsp", Name: "TestMcpLSPProcess"},
	}
	raceTests := []gate.GoTestTarget{
		{Package: "./internal/archtest", Name: "TestCommon"}, {Package: "./internal/archtest", Name: "TestRace"},
		{Package: "./internal/provider/codexapp", Name: "TestTransportCommon"}, {Package: "./internal/provider/codexapp", Name: "TestTransportRace"},
		{Package: "./cmd/agent-runtime", Name: "TestAgentRuntimeMain"}, {Package: "./cmd/agent-runtime", Name: "TestAgentRuntimeRace"},
		{Package: "./cmd/agent-terminal", Name: "TestAgentTerminalMain"}, {Package: "./cmd/agent-terminal", Name: "TestAgentTerminalRecovery"},
		{Package: "./internal/app", Name: "TestAppModuleGraphIsClosed"}, {Package: "./internal/app", Name: "TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch"}, {Package: "./internal/app", Name: "TestRunDesktopPreDrain"},
		{Package: "./cmd/super-dolphin-updater", Name: "TestUpdaterCandidateCleanup"}, {Package: "./cmd/super-dolphin-updater", Name: "TestUpdaterRollbackEntries"},
		{Package: "./cmd/mcp-orch/store/taskdag", Name: "TestTaskDAGStore"}, {Package: "./cmd/mcp-orch/store/taskdag", Name: "TestTaskDAGWakeup"},
		{Package: "./internal/platform/db/sqlite", Name: "TestSQLiteCommon"}, {Package: "./internal/platform/db/sqlite", Name: "TestSQLiteRace"},
		{Package: "./internal/devtools/gate", Name: "TestGateAtomicCommon"}, {Package: "./internal/devtools/gate", Name: "TestGateAtomicRace"},
		{Package: "./internal/devtools/remoteci", Name: "TestRemoteCIAtomicCommon"}, {Package: "./internal/devtools/remoteci", Name: "TestRemoteCIAtomicRace"},
		{Package: "./cmd/mcp-lsp", Name: "TestMcpLSPCommon"}, {Package: "./cmd/mcp-lsp", Name: "TestMcpLSPRace"},
	}
	return packages, tests, raceTests
}

func TestBuildWorkloadInventoryUsesExactCommitAndRange(t *testing.T) {
	repository := t.TempDir()
	runInventoryGit(t, repository, "init", "--quiet")
	runInventoryGit(t, repository, "config", "user.name", "CI Inventory")
	runInventoryGit(t, repository, "config", "user.email", "ci@example.invalid")
	writeInventoryFixture(t, repository)
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "基础")
	base := inventoryGitOutput(t, repository, "rev-parse", "HEAD")

	writeInventoryFile(t, repository, "frontend-app/src/widget.ts", "export const widget = 2\n")
	writeInventoryFile(t, repository, "frontend-app/scripts/chat-history-benchmark.test.mjs", "test('changed benchmark', () => {})\n")
	writeInventoryFile(t, repository, "internal/beta/beta_test.go", "package beta\n")
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "更新")
	commit := inventoryGitOutput(t, repository, "rev-parse", "HEAD")

	inventory, err := BuildWorkloadInventory(context.Background(), repository, commit, base, "linux/amd64")
	if err != nil {
		t.Fatalf("BuildWorkloadInventory() error = %v", err)
	}
	if !slices.Equal(inventory.GoPackages, []string{"./build/gate", "./build/gate/closure", "./cmd/agent-runtime", "./cmd/agent-terminal", "./cmd/mcp-lsp", "./cmd/mcp-orch/store/taskdag", "./cmd/super-dolphin-updater", "./internal/alpha", "./internal/app", "./internal/archtest", "./internal/beta", "./internal/devtools/gate", "./internal/devtools/remoteci", "./internal/platform/db/sqlite", "./internal/provider/codexapp", "./new-root/tool"}) ||
		!slices.Equal(inventory.NestedGoModules, []string{"build/gate/runtime-tools", "tools/custom-check"}) ||
		!slices.Equal(inventory.FrontendChangedTests, []string{"src/widget.test.ts"}) ||
		!slices.Equal(inventory.FrontendFullTests, []string{"scripts/runtime.test.mjs", "src/widget.test.ts"}) {
		t.Fatalf("inventory = %#v", inventory)
	}
	assertAtomicInventoryTestNames(t, inventory.GoTests, inventory.GoRaceTests)
}

func TestBuildWorkloadInventorySharesExactTreeSnapshot(t *testing.T) {
	repository := t.TempDir()
	runInventoryGit(t, repository, "init", "--quiet")
	runInventoryGit(t, repository, "config", "user.name", "CI Inventory")
	runInventoryGit(t, repository, "config", "user.email", "ci@example.invalid")
	writeInventoryFixture(t, repository)
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "基础")
	commit := inventoryGitOutput(t, repository, "rev-parse", "HEAD")

	tracePath := installInventoryGitCounter(t)
	inventory, err := BuildWorkloadInventory(context.Background(), repository, commit, "", "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	expectedPackages, expectedTests, expectedRaceTests := inventoryExpectedSelectors()
	if !slices.Equal(inventory.GoPackages, expectedPackages) ||
		!slices.Equal(inventory.GoTests, expectedTests) ||
		!slices.Equal(inventory.GoRaceTests, expectedRaceTests) {
		t.Fatalf("inventory selectors changed: got=%#v wantPackages=%v wantTests=%v wantRaceTests=%v", inventory, expectedPackages, expectedTests, expectedRaceTests)
	}
	counts := readInventoryGitCounterCounts(t, tracePath)
	if counts.snapshotTree != 1 || counts.blobBatch != 1 {
		t.Fatalf("shared snapshot git calls = %#v, want one snapshot tree and one blob batch", counts)
	}
}

func writeInventoryFixture(t *testing.T, repository string) {
	t.Helper()
	files := map[string]string{
		"go.mod":                     "module example.test/root\n\ngo 1.25\n",
		"build/gate/closure_test.go": "package gate_test\n",
		"build/gate/closure/runtime_deps_test.go":                                 "package gateclosure\n",
		"build/gate/runtime-tools/go.mod":                                         "module example.test/runtime-tools\n\ngo 1.25\n",
		"build/gate/runtime-tools/tools.go":                                       "package tools\n",
		"internal/alpha/alpha.go":                                                 "package alpha\n",
		"internal/ignored/tool.go":                                                "//go:build ignore\n\npackage main\n",
		"internal/archtest/common_test.go":                                        "package archtest\n\nimport \"testing\"\n\nfunc TestCommon(t *testing.T) {}\n",
		"internal/archtest/normal_test.go":                                        "//go:build !race\n\npackage archtest\n\nimport \"testing\"\n\nfunc TestNormal(t *testing.T) {}\n",
		"internal/archtest/race_test.go":                                          "//go:build race\n\npackage archtest\n\nimport \"testing\"\n\nfunc TestRace(t *testing.T) {}\n",
		"internal/provider/codexapp/common_test.go":                               "package codexapp\n\nimport \"testing\"\n\nfunc TestTransportCommon(t *testing.T) {}\n",
		"internal/provider/codexapp/normal_test.go":                               "//go:build !race\n\npackage codexapp\n\nimport \"testing\"\n\nfunc TestTransportNormal(t *testing.T) {}\n",
		"internal/provider/codexapp/race_test.go":                                 "//go:build race\n\npackage codexapp\n\nimport \"testing\"\n\nfunc TestTransportRace(t *testing.T) {}\n",
		"cmd/agent-runtime/main.go":                                               "package main\n",
		"cmd/agent-runtime/main_test.go":                                          "package main\n\nimport \"testing\"\n\nfunc TestAgentRuntimeMain(t *testing.T) {}\nfunc TestAgentRuntimeRace(t *testing.T) {}\n",
		"cmd/agent-terminal/main.go":                                              "package main\n",
		"cmd/agent-terminal/main_test.go":                                         "package main\n\nimport \"testing\"\n\nfunc TestAgentTerminalMain(t *testing.T) {}\nfunc TestAgentTerminalRecovery(t *testing.T) {}\n",
		"cmd/mcp-lsp/mcp.go":                                                      "package main\n",
		"cmd/mcp-lsp/common_test.go":                                              "package main\n\nimport \"testing\"\n\nfunc TestMcpLSPCommon(t *testing.T) {}\n",
		"cmd/mcp-lsp/normal_test.go":                                              "//go:build !race\n\npackage main\n\nimport \"testing\"\n\nfunc TestMcpLSPProcess(t *testing.T) {}\n",
		"cmd/mcp-lsp/race_test.go":                                                "//go:build race\n\npackage main\n\nimport \"testing\"\n\nfunc TestMcpLSPRace(t *testing.T) {}\n",
		"cmd/mcp-lsp/helper_test.go":                                              "package main\n\nimport \"testing\"\n\n// super-dolphin-ci: helper\nfunc TestMcpLSPChildProcess(t *testing.T) {}\n",
		"cmd/mcp-orch/store/taskdag/store.go":                                     "package taskdag\n",
		"cmd/mcp-orch/store/taskdag/store_test.go":                                "package taskdag\n\nimport \"testing\"\n\nfunc TestTaskDAGStore(t *testing.T) {}\nfunc TestTaskDAGWakeup(t *testing.T) {}\n\n// super-dolphin-ci: helper\nfunc TestTaskDAGChildProcess(t *testing.T) {}\n",
		"cmd/super-dolphin-updater/main.go":                                       "package main\n",
		"cmd/super-dolphin-updater/updater_test.go":                               "package main\n\nimport \"testing\"\n\nfunc TestUpdaterCandidateCleanup(t *testing.T) {}\nfunc TestUpdaterRollbackEntries(t *testing.T) {}\n\n// super-dolphin-ci: helper\nfunc TestUpdaterCandidateProcess(t *testing.T) {}\n",
		"internal/app/app.go":                                                     "package app\n",
		"internal/app/app_test.go":                                                "package app\n\nimport \"testing\"\n\nfunc TestAppModuleGraphIsClosed(t *testing.T) {}\nfunc TestRunDesktopPreDrain(t *testing.T) {}\nfunc TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch(t *testing.T) {}\n",
		"internal/platform/db/sqlite/sqlite.go":                                   "package sqlite\n",
		"internal/platform/db/sqlite/common_test.go":                              "package sqlite\n\nimport \"testing\"\n\nfunc TestSQLiteCommon(t *testing.T) {}\n",
		"internal/platform/db/sqlite/normal_test.go":                              "//go:build !race\n\npackage sqlite\n\nimport \"testing\"\n\nfunc TestSQLiteNormal(t *testing.T) {}\n",
		"internal/platform/db/sqlite/race_test.go":                                "//go:build race\n\npackage sqlite\n\nimport \"testing\"\n\nfunc TestSQLiteRace(t *testing.T) {}\n",
		"internal/devtools/gate/gate.go":                                          "package gate\n",
		"internal/devtools/gate/common_test.go":                                   "package gate\n\nimport \"testing\"\n\nfunc TestGateAtomicCommon(t *testing.T) {}\n",
		"internal/devtools/gate/normal_test.go":                                   "//go:build !race\n\npackage gate\n\nimport \"testing\"\n\nfunc TestGateAtomicNormal(t *testing.T) {}\n",
		"internal/devtools/gate/race_test.go":                                     "//go:build race\n\npackage gate\n\nimport \"testing\"\n\nfunc TestGateAtomicRace(t *testing.T) {}\n",
		"internal/devtools/remoteci/remoteci.go":                                  "package remoteci\n",
		"internal/devtools/remoteci/common_test.go":                               "package remoteci\n\nimport \"testing\"\n\nfunc TestRemoteCIAtomicCommon(t *testing.T) {}\n",
		"internal/devtools/remoteci/normal_test.go":                               "//go:build !race\n\npackage remoteci\n\nimport \"testing\"\n\nfunc TestRemoteCIAtomicNormal(t *testing.T) {}\n",
		"internal/devtools/remoteci/race_test.go":                                 "//go:build race\n\npackage remoteci\n\nimport \"testing\"\n\nfunc TestRemoteCIAtomicRace(t *testing.T) {}\n",
		"new-root/tool/tool.go":                                                   "package tool\n",
		"tools/custom-check/go.mod":                                               "module example.test/custom-check\n\ngo 1.25\n",
		"tools/custom-check/check.go":                                             "package check\n",
		"frontend-app/src/widget.ts":                                              "export const widget = 1\n",
		"frontend-app/src/widget.test.ts":                                         "test('widget', () => {})\n",
		"frontend-app/scripts/runtime.test.mjs":                                   "test('runtime', () => {})\n",
		"frontend-app/scripts/remote-preflight-carriers/critical-guards.test.mjs": "// protocol carrier\n",
		"frontend-app/scripts/remote-suite-carriers/changed.test.mjs":             "// protocol carrier\n",
		inventoryVitestSuitePolicyPath: `{
  "schemaVersion": 1,
  "defaultExcludes": ["**/scripts/**/*benchmark.test.*", "**/scripts/**/performance-*.test.*", "**/scripts/remote-preflight-carriers/*.test.mjs", "**/scripts/remote-suite-carriers/*.test.mjs"]
}`,
		"frontend-app/scripts/chat-history-benchmark.test.mjs": "test('benchmark', () => {})\n",
		"frontend-app/scripts/performance-budget.test.mjs":     "test('performance', () => {})\n",
	}
	for filePath, body := range files {
		writeInventoryFile(t, repository, filePath, body)
	}
}

func assertAtomicInventoryTestNames(t *testing.T, normal, race []gate.GoTestTarget) {
	t.Helper()
	toNames := func(targets []gate.GoTestTarget) []string {
		names := make([]string, len(targets))
		for index, target := range targets {
			names[index] = target.Package + "#" + target.Name
		}
		return names
	}
	normalNames, raceNames := toNames(normal), toNames(race)
	if !slices.Equal(normalNames, []string{
		"./internal/archtest#TestCommon",
		"./internal/archtest#TestNormal",
		"./internal/provider/codexapp#TestTransportCommon",
		"./internal/provider/codexapp#TestTransportNormal",
		"./cmd/agent-runtime#TestAgentRuntimeMain",
		"./cmd/agent-runtime#TestAgentRuntimeRace",
		"./cmd/agent-terminal#TestAgentTerminalMain",
		"./cmd/agent-terminal#TestAgentTerminalRecovery",
		"./internal/app#TestAppModuleGraphIsClosed",
		"./internal/app#TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch",
		"./internal/app#TestRunDesktopPreDrain",
		"./cmd/super-dolphin-updater#TestUpdaterCandidateCleanup",
		"./cmd/super-dolphin-updater#TestUpdaterRollbackEntries",
		"./cmd/mcp-orch/store/taskdag#TestTaskDAGStore",
		"./cmd/mcp-orch/store/taskdag#TestTaskDAGWakeup",
		"./internal/platform/db/sqlite#TestSQLiteCommon",
		"./internal/platform/db/sqlite#TestSQLiteNormal",
		"./internal/devtools/gate#TestGateAtomicCommon",
		"./internal/devtools/gate#TestGateAtomicNormal",
		"./internal/devtools/remoteci#TestRemoteCIAtomicCommon",
		"./internal/devtools/remoteci#TestRemoteCIAtomicNormal",
		"./cmd/mcp-lsp#TestMcpLSPCommon",
		"./cmd/mcp-lsp#TestMcpLSPProcess",
	}) || !slices.Equal(raceNames, []string{
		"./internal/archtest#TestCommon",
		"./internal/archtest#TestRace",
		"./internal/provider/codexapp#TestTransportCommon",
		"./internal/provider/codexapp#TestTransportRace",
		"./cmd/agent-runtime#TestAgentRuntimeMain",
		"./cmd/agent-runtime#TestAgentRuntimeRace",
		"./cmd/agent-terminal#TestAgentTerminalMain",
		"./cmd/agent-terminal#TestAgentTerminalRecovery",
		"./internal/app#TestAppModuleGraphIsClosed",
		"./internal/app#TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch",
		"./internal/app#TestRunDesktopPreDrain",
		"./cmd/super-dolphin-updater#TestUpdaterCandidateCleanup",
		"./cmd/super-dolphin-updater#TestUpdaterRollbackEntries",
		"./cmd/mcp-orch/store/taskdag#TestTaskDAGStore",
		"./cmd/mcp-orch/store/taskdag#TestTaskDAGWakeup",
		"./internal/platform/db/sqlite#TestSQLiteCommon",
		"./internal/platform/db/sqlite#TestSQLiteRace",
		"./internal/devtools/gate#TestGateAtomicCommon",
		"./internal/devtools/gate#TestGateAtomicRace",
		"./internal/devtools/remoteci#TestRemoteCIAtomicCommon",
		"./internal/devtools/remoteci#TestRemoteCIAtomicRace",
		"./cmd/mcp-lsp#TestMcpLSPCommon",
		"./cmd/mcp-lsp#TestMcpLSPRace",
	}) {
		t.Fatalf("atomic Go tests normal=%v race=%v", normalNames, raceNames)
	}
}

func TestLoadInventoryVitestSuitePolicyFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		policy string
	}{
		{name: "missing excludes", policy: `{"schemaVersion":1}`},
		{name: "unknown field", policy: `{"schemaVersion":1,"defaultExcludes":["scripts/*.test.mjs"],"extra":true}`},
		{name: "duplicate", policy: `{"schemaVersion":1,"defaultExcludes":["scripts/*.test.mjs","scripts/*.test.mjs"]}`},
		{name: "invalid globstar", policy: `{"schemaVersion":1,"defaultExcludes":["scripts/**bad/*.test.mjs"]}`},
		{name: "trailing value", policy: `{"schemaVersion":1,"defaultExcludes":["scripts/*.test.mjs"]} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			runInventoryGit(t, repository, "init", "--quiet")
			runInventoryGit(t, repository, "config", "user.name", "CI Inventory")
			runInventoryGit(t, repository, "config", "user.email", "ci@example.invalid")
			writeInventoryFile(t, repository, inventoryVitestSuitePolicyPath, test.policy)
			runInventoryGit(t, repository, "add", ".")
			runInventoryGit(t, repository, "commit", "--quiet", "-m", "策略")
			commit := inventoryGitOutput(t, repository, "rev-parse", "HEAD")
			if _, err := loadInventoryVitestSuitePolicy(context.Background(), repository, commit); err == nil {
				t.Fatal("loadInventoryVitestSuitePolicy() error = nil")
			}
		})
	}
}

func TestInventoryVitestGlobMatches(t *testing.T) {
	for _, test := range []struct {
		pattern string
		target  string
		want    bool
	}{
		{pattern: "**/scripts/**/*benchmark.test.*", target: "scripts/chat-history-benchmark.test.mjs", want: true},
		{pattern: "**/scripts/**/*benchmark.test.*", target: "scripts/nested/stop-feedback-benchmark.test.mjs", want: true},
		{pattern: "**/scripts/**/performance-*.test.*", target: "scripts/performance-budget.test.mjs", want: true},
		{pattern: "**/scripts/**/performance-*.test.*", target: "src/performance-budget.test.mjs", want: false},
	} {
		if got := inventoryVitestGlobMatches(test.pattern, test.target); got != test.want {
			t.Errorf("inventoryVitestGlobMatches(%q, %q) = %t, want %t", test.pattern, test.target, got, test.want)
		}
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

type inventoryGitCounterCounts struct {
	snapshotTree int
	blobBatch    int
}

func installInventoryGitCounter(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	tracePath := filepath.Join(binDir, "git.trace")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	gitShim := filepath.Join(binDir, "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + quote(tracePath) + "\nexec " + quote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(gitShim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return tracePath
}

func readInventoryGitCounterCounts(t *testing.T, tracePath string) inventoryGitCounterCounts {
	t.Helper()
	contents, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	var counts inventoryGitCounterCounts
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		switch {
		case strings.HasPrefix(line, "ls-tree -r -z --full-tree"):
			counts.snapshotTree++
		case strings.HasPrefix(line, "cat-file --batch"):
			counts.blobBatch++
		}
	}
	return counts
}
