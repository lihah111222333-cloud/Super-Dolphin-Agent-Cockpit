package skill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalStoreListIncludesProjectAndPersonal(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "proj"), "proj")
	writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "user", "mine"), "mine")
	writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "agent", "agentmade"), "agentmade")
	writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "imported", "imported"), "imported")
	writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "hub", "hubskill"), "hubskill")

	store := newTestCanonicalStore(project, home)
	got, conflicts, err := store.EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	assertHasCanonicalSkill(t, got, "proj", skillScopeProject, "")
	assertHasCanonicalSkill(t, got, "mine", skillScopePersonal, personalSkillTypeUser)
	assertHasCanonicalSkill(t, got, "agentmade", skillScopePersonal, personalSkillTypeAgent)
	assertHasCanonicalSkill(t, got, "imported", skillScopePersonal, personalSkillTypeImported)
	assertHasCanonicalSkill(t, got, "hubskill", skillScopePersonal, personalSkillTypeHub)
}

func TestEffectiveSetSameNameIsStrictConflict(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "user", "build"), "build")

	records, conflicts, err := newTestCanonicalStore(project, home).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v, want no selected winner for same-name conflict", records)
	}
	assertSameNameConflict(t, conflicts, "build",
		canonicalSkillConflictSource{Scope: skillScopeProject, PersonalType: "", Name: "build"},
		canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, Name: "build"},
	)
}

func TestEffectiveSetSameNameIsCaseInsensitiveConflict(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "Build"), "Build")
	writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "user", "build"), "build")

	records, conflicts, err := newTestCanonicalStore(project, home).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v, want no selected winner for case-folded conflict", records)
	}
	assertSameNameConflict(t, conflicts, "Build",
		canonicalSkillConflictSource{Scope: skillScopeProject, PersonalType: "", Name: "Build"},
		canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, Name: "build"},
	)
}

func TestEffectiveSetPersonalSameNamePairsAreStrictConflicts(t *testing.T) {
	pairs := []struct {
		name  string
		left  string
		right string
	}{
		{name: "user-agent", left: personalSkillTypeUser, right: personalSkillTypeAgent},
		{name: "user-imported", left: personalSkillTypeUser, right: personalSkillTypeImported},
		{name: "user-hub", left: personalSkillTypeUser, right: personalSkillTypeHub},
		{name: "agent-imported", left: personalSkillTypeAgent, right: personalSkillTypeImported},
		{name: "agent-hub", left: personalSkillTypeAgent, right: personalSkillTypeHub},
		{name: "imported-hub", left: personalSkillTypeImported, right: personalSkillTypeHub},
	}
	for _, tc := range pairs {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			home := t.TempDir()
			writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", tc.left, "build"), "build")
			writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", tc.right, "build"), "build")

			records, conflicts, err := newTestCanonicalStore(project, home).EffectiveSet(context.Background(), project)
			if err != nil {
				t.Fatalf("EffectiveSet: %v", err)
			}
			if len(records) != 0 {
				t.Fatalf("records = %+v, want no selected winner for same-name conflict", records)
			}
			assertSameNameConflict(t, conflicts, "build",
				canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: tc.left, Name: "build"},
				canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: tc.right, Name: "build"},
			)
		})
	}
}

func TestServiceListSkillsIncludesPersonalCanonical(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "proj"), "proj")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "mine"), "mine")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	got, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	assertHasSkillInfo(t, got, "proj", skillScopeProject, "")
	assertHasSkillInfo(t, got, "mine", skillScopePersonal, personalSkillTypeUser)
}

func TestServiceExpandSameNameConflictFailsClosed(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "build"), "build")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	_, err := svc.Expand(skillTestContext(project), skillExpandParams{Name: "build"})
	if !errors.Is(err, ErrSkillSameNameConflict) {
		t.Fatalf("Expand error = %v, want ErrSkillSameNameConflict", err)
	}
}

func TestServiceMatchPreviewSameNameConflictFailsClosed(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "build"), "build")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	_, err := svc.MatchPreview(skillTestContext(project), "agent", "", "build", nil)
	if !errors.Is(err, ErrSkillSameNameConflict) {
		t.Fatalf("MatchPreview error = %v, want ErrSkillSameNameConflict", err)
	}
}

func TestEffectiveSetProjectPolicyDisablesPersonalForOnlyThatProject(t *testing.T) {
	project := t.TempDir()
	otherProject := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(otherProject, ".agent", "skills", "build"), "build")
	personalDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "build")
	writeCanonicalSkill(t, personalDir, "build")
	writeProjectSkillPolicy(t, project, projectSkillPolicy{
		Version: 1,
		DisablePersonalForProject: []projectSkillPolicyDisabledPersonal{{
			Name:         "build",
			PersonalType: personalSkillTypeUser,
		}},
	})

	records, conflicts, err := newCanonicalStore(superDolphinHome).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet(project): %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("project conflicts = %+v, want resolved", conflicts)
	}
	assertHasCanonicalSkill(t, records, "build", skillScopeProject, "")
	assertExists(t, filepath.Join(personalDir, skillMainFile))

	_, conflicts, err = newCanonicalStore(superDolphinHome).EffectiveSet(context.Background(), otherProject)
	if err != nil {
		t.Fatalf("EffectiveSet(otherProject): %v", err)
	}
	assertSameNameConflict(t, conflicts, "build",
		canonicalSkillConflictSource{Scope: skillScopeProject, PersonalType: "", Name: "build"},
		canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, Name: "build"},
	)
}

func TestEffectiveSetKeepSelectedOwnerPolicyResolvesPersonalConflict(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	userDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "build")
	agentDir := filepath.Join(superDolphinHome, "skills", "personal", "agent", "build")
	writeCanonicalSkill(t, userDir, "build")
	writeCanonicalSkill(t, agentDir, "build")
	records, err := newCanonicalStore(superDolphinHome).scan(project)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	userRecord := findCanonicalRecord(t, records, "build", skillScopePersonal, personalSkillTypeUser)
	agentRecord := findCanonicalRecord(t, records, "build", skillScopePersonal, personalSkillTypeAgent)
	owner, err := resolveOwnerIdentity(superDolphinHome, "1001", "profile-a")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	writePersonalSkillPolicy(t, superDolphinHome, personalSkillPolicy{
		Version:  1,
		OwnerKey: owner.OwnerKey,
		KeepSelected: []personalSkillKeepSelected{{
			Name:                 "build",
			SelectedSourceID:     canonicalSourceID(userRecord),
			SelectedPersonalType: personalSkillTypeUser,
			SelectedContentHash:  userRecord.ContentHash,
			ExcludedSourceIDs:    []string{canonicalSourceID(agentRecord)},
			Sources: []personalSkillPolicySource{
				{CanonicalID: canonicalSourceID(userRecord), PersonalType: personalSkillTypeUser, ContentHash: userRecord.ContentHash},
				{CanonicalID: canonicalSourceID(agentRecord), PersonalType: personalSkillTypeAgent, ContentHash: agentRecord.ContentHash},
			},
		}},
	})

	store := newCanonicalStoreForOwner(superDolphinHome, "1001", "profile-a")
	effective, conflicts, err := store.EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet(owner): %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want keep_selected resolution", conflicts)
	}
	assertHasCanonicalSkill(t, effective, "build", skillScopePersonal, personalSkillTypeUser)
	assertNotHasCanonicalSkill(t, effective, "build", skillScopePersonal, personalSkillTypeAgent)

	_, conflicts, err = newCanonicalStoreForOwner(superDolphinHome, "1001", "profile-b").EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet(other owner): %v", err)
	}
	assertSameNameConflict(t, conflicts, "build",
		canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, Name: "build"},
		canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: personalSkillTypeAgent, Name: "build"},
	)
}

func TestEffectiveSetRejectsOwnerPolicyWithBroadPermissions(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "build"), "build")
	owner, err := resolveOwnerIdentity(superDolphinHome, "1001", "profile-a")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	writePersonalSkillPolicy(t, superDolphinHome, personalSkillPolicy{
		Version:  1,
		OwnerKey: owner.OwnerKey,
		KeepSelected: []personalSkillKeepSelected{{
			Name: "build",
		}},
	})
	policyPath := filepath.Join(superDolphinHome, "skills", personalSkillPolicyFile)
	if err := os.Chmod(policyPath, 0o644); err != nil {
		t.Fatalf("Chmod policy: %v", err)
	}

	_, _, err = newCanonicalStoreForOwner(superDolphinHome, "1001", "profile-a").EffectiveSet(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("EffectiveSet error = %v, want permissions rejection", err)
	}
}

func TestServiceListSkillsAppliesOwnerKeepSelectedPolicy(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	userDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "build")
	agentDir := filepath.Join(superDolphinHome, "skills", "personal", "agent", "build")
	writeCanonicalSkill(t, userDir, "build")
	writeCanonicalSkill(t, agentDir, "build")
	records, err := newCanonicalStore(superDolphinHome).scan(project)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	userRecord := findCanonicalRecord(t, records, "build", skillScopePersonal, personalSkillTypeUser)
	agentRecord := findCanonicalRecord(t, records, "build", skillScopePersonal, personalSkillTypeAgent)
	owner, err := resolveOwnerIdentity(superDolphinHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	writePersonalSkillPolicy(t, superDolphinHome, personalSkillPolicy{
		Version:  1,
		OwnerKey: owner.OwnerKey,
		KeepSelected: []personalSkillKeepSelected{{
			Name:                 "build",
			SelectedSourceID:     canonicalSourceID(agentRecord),
			SelectedPersonalType: personalSkillTypeAgent,
			SelectedContentHash:  agentRecord.ContentHash,
			ExcludedSourceIDs:    []string{canonicalSourceID(userRecord)},
		}},
	})
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	infos, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	assertHasSkillInfo(t, infos, "build", skillScopePersonal, personalSkillTypeAgent)
}

func newTestCanonicalStore(project, home string) *canonicalStore {
	return newCanonicalStore(filepath.Join(home, ".super-dolphin"))
}

func writeCanonicalSkill(t *testing.T, dir, name string) {
	t.Helper()
	writeSkillContent(t, dir, name, "# "+name+"\n")
}

func writeSkillContent(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	content := "---\nname: " + name + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, skillMainFile), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", dir, err)
	}
}

func writeProjectSkillPolicy(t *testing.T, project string, policy projectSkillPolicy) {
	t.Helper()
	writeJSONFile(t, filepath.Join(project, ".agent", "skills", projectSkillPolicyFile), policy, 0o644)
}

func writePersonalSkillPolicy(t *testing.T, superDolphinHome string, policy personalSkillPolicy) {
	t.Helper()
	writeJSONFile(t, filepath.Join(superDolphinHome, "skills", personalSkillPolicyFile), policy, 0o600)
}

func writeJSONFile(t *testing.T, path string, value any, mode os.FileMode) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func findCanonicalRecord(t *testing.T, records []canonicalSkillRecord, name, scope, personalType string) canonicalSkillRecord {
	t.Helper()
	for _, record := range records {
		if record.Name == name && record.Scope == scope && record.PersonalType == personalType {
			return record
		}
	}
	t.Fatalf("missing canonical record name=%q scope=%q personal_type=%q in %+v", name, scope, personalType, records)
	return canonicalSkillRecord{}
}

func assertHasCanonicalSkill(t *testing.T, records []canonicalSkillRecord, name, scope, personalType string) {
	t.Helper()
	for _, record := range records {
		if record.Name != name || record.Scope != scope || record.PersonalType != personalType {
			continue
		}
		if record.Dir == "" || record.SkillFile == "" || record.ContentHash == "" || record.DirHash == "" {
			t.Fatalf("record for %s has missing provenance: %+v", name, record)
		}
		return
	}
	t.Fatalf("missing canonical skill name=%q scope=%q personal_type=%q in %+v", name, scope, personalType, records)
}

func assertNotHasCanonicalSkill(t *testing.T, records []canonicalSkillRecord, name, scope, personalType string) {
	t.Helper()
	for _, record := range records {
		if record.Name == name && record.Scope == scope && record.PersonalType == personalType {
			t.Fatalf("unexpected canonical skill name=%q scope=%q personal_type=%q in %+v", name, scope, personalType, records)
		}
	}
}

func assertSameNameConflict(t *testing.T, conflicts []canonicalSkillConflict, name string, want ...canonicalSkillConflictSource) {
	t.Helper()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want exactly one conflict", conflicts)
	}
	conflict := conflicts[0]
	if conflict.Kind != "same_name" || conflict.Name != name {
		t.Fatalf("conflict = %+v, want same_name for %q", conflict, name)
	}
	for _, source := range want {
		if !conflictHasSource(conflict, source) {
			t.Fatalf("conflict sources = %+v, missing %+v", conflict.Sources, source)
		}
	}
}

func conflictHasSource(conflict canonicalSkillConflict, want canonicalSkillConflictSource) bool {
	for _, source := range conflict.Sources {
		if source.Name == want.Name && source.Scope == want.Scope && source.PersonalType == want.PersonalType {
			return true
		}
	}
	return false
}

func assertHasSkillInfo(t *testing.T, infos []SkillInfo, name, scope, personalType string) {
	t.Helper()
	for _, info := range infos {
		if info.Name == name && info.Scope == scope && info.PersonalType == personalType {
			return
		}
	}
	t.Fatalf("missing skill info name=%q scope=%q personal_type=%q in %+v", name, scope, personalType, infos)
}
