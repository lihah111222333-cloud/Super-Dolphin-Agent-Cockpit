package skill

import (
	"path/filepath"
	"testing"
)

func TestSkillMirrorReconcilerDetectsExternalPersonalProjectSameName(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "claude:user-global:owner", Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: filepath.Join(t.TempDir(), ".claude", "skills"), CanonicalRootID: "sd_owner:owner"}
	writeSkillWithSupportFiles(t, filepath.Join(target.Root, "build"), "build")
	writeFileWithMode(t, filepath.Join(target.Root, "build", "references", "guide.md"), "external personal edit\n", 0o644)

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	conflict := findMirrorConflict(t, conflicts, "external_personal_project_same_name", "build", skillScopePersonal)
	if conflict.CanonicalID != "project/build" {
		t.Fatalf("canonical_id = %q, want project/build; conflict=%+v", conflict.CanonicalID, conflict)
	}
	assertMirrorConflictActions(t, conflict, "view_diff", "use_project_shared_skill", "use_external_provider_skill", "save_as_new_personal_skill")
}

func TestSkillMirrorReconcilerIgnoresOrphanExternalPersonalProviderSkill(t *testing.T) {
	project := t.TempDir()
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "claude:user-global:owner", Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: filepath.Join(t.TempDir(), ".claude", "skills"), CanonicalRootID: "sd_owner:owner"}
	writeSkillWithSupportFiles(t, filepath.Join(target.Root, "scratch"), "scratch")

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	for _, conflict := range conflicts {
		if conflict.Kind == "unmanaged_provider_skill" && conflict.Name == "scratch" && conflict.Scope == skillScopePersonal {
			t.Fatalf("conflicts = %+v, want orphan external personal provider skill ignored", conflicts)
		}
	}
}
