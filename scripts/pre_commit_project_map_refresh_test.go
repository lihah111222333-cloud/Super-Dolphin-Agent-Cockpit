package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreCommitAutoRefreshesAndStagesTrustedProjectMap 锁定 exact staged tree 漂移会自动刷新并暂存受信项目地图。
func TestPreCommitAutoRefreshesAndStagesTrustedProjectMap(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, "internal/app/project_map.go", "package app\n")
	runFixTestGuardGit(t, root, "add", "internal/app/project_map.go")
	sourceTree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree"))
	marker := filepath.Join(t.TempDir(), "project-map-refreshed")

	out, err := runPreCommitHookWithEnv(t, root, map[string]string{"GATE_PROJECT_MAP_DRIFT_MARKER": marker})
	if err != nil {
		t.Fatalf("pre-commit trusted project-map refresh failed: %v\n%s", err, out)
	}
	refreshedTree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree"))
	if refreshedTree == sourceTree {
		t.Fatal("trusted project-map refresh did not change the staged tree")
	}
	generated := filepath.Join(root, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MAP.md")
	contents, readErr := os.ReadFile(generated)
	if readErr != nil {
		t.Fatalf("read auto-refreshed project map: %v", readErr)
	}
	if !strings.Contains(string(contents), sourceTree) {
		t.Fatalf("auto-refreshed project map does not bind source tree %s: %q", sourceTree, contents)
	}
	stagedPaths := runFixTestGuardGitOutput(t, root, "diff", "--cached", "--name-only")
	assertOutputContainsAll(t, stagedPaths, "internal/app/project_map.go", "docs/doc/codemap/project-map/AI_PROJECT_MAP.md")
	assertOutputContainsAll(t, out,
		"project-map drift detected; refreshing and staging trusted outputs",
		"auto-staged project-map output: docs/doc/codemap/project-map/AI_PROJECT_MAP.md",
		"fixture project-map refresh verified staged tree "+sourceTree,
		"fixture project-map check verified staged tree "+refreshedTree,
		"tree="+refreshedTree,
	)
}

// TestPreCommitProjectMapRefreshPreservesUnstagedManagedOutput 锁定自动刷新不得覆盖受管目录中的未暂存用户工作。
func TestPreCommitProjectMapRefreshPreservesUnstagedManagedOutput(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	mapPath := filepath.Join(root, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MAP.md")
	writeFixTestGuardFile(t, root, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", "tracked project map\n")
	runFixTestGuardGit(t, root, "add", "docs/doc/codemap/project-map/AI_PROJECT_MAP.md")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 添加项目地图基线")
	writeFixTestGuardFile(t, root, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", "user project-map work\n")
	writeFixTestGuardFile(t, root, "internal/app/project_map_conflict.go", "package app\n")
	runFixTestGuardGit(t, root, "add", "internal/app/project_map_conflict.go")
	sourceTree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree"))
	marker := filepath.Join(t.TempDir(), "project-map-refreshed")

	out, err := runPreCommitHookWithEnv(t, root, map[string]string{"GATE_PROJECT_MAP_DRIFT_MARKER": marker})
	if err == nil {
		t.Fatalf("pre-commit overwrote an unstaged managed output:\n%s", out)
	}
	assertOutputContainsAll(t, out, "project-map outputs contain unstaged changes", "preserve or stage them before automatic refresh")
	if got := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree")); got != sourceTree {
		t.Fatalf("blocked project-map refresh changed index tree: got %s want %s", got, sourceTree)
	}
	contents, readErr := os.ReadFile(mapPath)
	if readErr != nil || string(contents) != "user project-map work\n" {
		t.Fatalf("blocked project-map refresh changed user work: contents=%q err=%v", contents, readErr)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("project-map refresh ran despite unstaged conflict: %v", statErr)
	}
}
