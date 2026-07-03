package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillMirrorReconcilerPropagatesInvalidMirrorRootError(t *testing.T) {
	project := t.TempDir()
	root := testCodexProjectMirrorRoot(project)
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatalf("MkdirAll mirror parent: %v", err)
	}
	if err := os.WriteFile(root, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile mirror root: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"}

	_, err := DetectSkillMirrorConflicts(nil, []SkillMirrorTarget{target})

	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("DetectSkillMirrorConflicts invalid root error = %v, want original root validation error", err)
	}
}

func mustSkillDirContentHash(t *testing.T, dir string) string {
	t.Helper()
	hash, err := skillDirContentHash(dir)
	if err != nil {
		t.Fatalf("skillDirContentHash(%q): %v", dir, err)
	}
	return hash
}
