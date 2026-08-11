package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareCompileGroupWorkRootIsUniqueAcrossCanonicalGroups(t *testing.T) {
	baseRoot := filepath.Join(canonicalCompileGroupTestTempDir(t), "unique-work")
	firstID := digestPlanLog([]byte("unique-first"))
	secondID := digestPlanLog([]byte("unique-second"))
	firstRoot, err := prepareCompileGroupWorkRoot(baseRoot, 0, firstID)
	if err != nil {
		t.Fatalf("prepare first compile group work root: %v", err)
	}
	secondRoot, err := prepareCompileGroupWorkRoot(baseRoot, 1, secondID)
	if err != nil {
		t.Fatalf("prepare second compile group work root: %v", err)
	}
	if firstRoot == secondRoot {
		t.Fatalf("compile group work roots collided: %q", firstRoot)
	}
	if filepath.Base(firstRoot) == filepath.Base(secondRoot) {
		t.Fatalf("compile group work root basenames collided: %q", filepath.Base(firstRoot))
	}
	if _, err := trustedDirectory(firstRoot, true, os.Geteuid()); err != nil {
		t.Fatalf("first compile group work root is not trusted: %v", err)
	}
	if _, err := trustedDirectory(secondRoot, true, os.Geteuid()); err != nil {
		t.Fatalf("second compile group work root is not trusted: %v", err)
	}
}

func TestCleanupCompiledGroupArtifactsPropagatesWorkspaceFailure(t *testing.T) {
	groupID := digestPlanLog([]byte("cleanup-failure-group"))
	artifacts := map[GateID]compiledGroupArtifact{
		GateID("cleanup-failure-selector"): {group: CompileGroup{GroupID: groupID}},
	}
	err := cleanupCompiledGroupArtifacts(artifacts)
	if err == nil || !strings.Contains(err.Error(), "workspace cleanup") {
		t.Fatalf("cleanup failure = %v, want propagated workspace cleanup error", err)
	}
}
