package skill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/ownerperms"
)

func TestCanonicalStoreListIncludesProjectAndActivePersonal(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "proj"), "proj")
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
	assertNotHasCanonicalSkill(t, got, "hubskill", skillScopePersonal, personalSkillTypeHub)
}

func TestEffectiveSetSameNameIsStrictConflict(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "build"), "build")
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

func TestEffectiveSetIgnoresInactiveHubProjectDuplicate(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "hub", "build"), "build")

	records, conflicts, err := newTestCanonicalStore(project, home).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want inactive hub ignored", conflicts)
	}
	assertHasCanonicalSkill(t, records, "build", skillScopeProject, "")
	assertNotHasCanonicalSkill(t, records, "build", skillScopePersonal, personalSkillTypeHub)
}

func TestCanonicalStoreConvertsSafeLegacyDisplayName(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "Docker 容器化部署"), "Docker 容器化部署", "# legacy display name\n")

	records, conflicts, err := newTestCanonicalStore(project, home).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1: %+v", len(records), records)
	}
	if records[0].Name != "docker-容器化部署" {
		t.Fatalf("name = %q", records[0].Name)
	}
	if records[0].info.DisplayName != "Docker 容器化部署" {
		t.Fatalf("display name = %q", records[0].info.DisplayName)
	}
}

func TestCanonicalStoreRejectsUnsafeLegacyDisplayName(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "bad"), "../bad", "# unsafe name\n")

	_, _, err := newTestCanonicalStore(project, home).EffectiveSet(context.Background(), project)
	if err == nil {
		t.Fatalf("EffectiveSet error = nil, want invalid skill name")
	}
	if !errors.Is(err, ErrInvalidSkillName) {
		t.Fatalf("EffectiveSet error = %v, want ErrInvalidSkillName", err)
	}
}

func TestCanonicalStoreRejectsSymlinkedSkillMainFile(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), skillMainFile)
	if err := os.WriteFile(outside, []byte("---\nname: demo\n---\noutside"), 0o644); err != nil {
		t.Fatalf("write outside skill file: %v", err)
	}
	demoDir := filepath.Join(project, ".agents", "skills", "demo")
	if err := os.MkdirAll(demoDir, 0o755); err != nil {
		t.Fatalf("mkdir demo skill: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(demoDir, skillMainFile)); err != nil {
		t.Fatalf("symlink SKILL.md: %v", err)
	}

	_, _, err := newTestCanonicalStore(project, home).EffectiveSet(context.Background(), project)
	if err == nil {
		t.Fatal("EffectiveSet error = nil, want symlinked SKILL.md rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("EffectiveSet error = %v, want symlink rejection", err)
	}
}

func TestEffectiveSetSameNameIsCaseInsensitiveConflict(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "Build"), "Build")
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
		{name: "agent-imported", left: personalSkillTypeAgent, right: personalSkillTypeImported},
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
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "proj"), "proj")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "mine"), "mine")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	got, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	assertHasSkillInfo(t, got, "proj", skillScopeProject, "")
	assertHasSkillInfo(t, got, "mine", skillScopePersonal, personalSkillTypeUser)
}

func TestServiceListSkillsIgnoresInactiveHubDuplicates(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "safe"), "safe")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "hub", "build"), "build")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	got, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	assertHasSkillInfo(t, got, "safe", skillScopeProject, "")
	assertHasSkillInfo(t, got, "build", skillScopePersonal, personalSkillTypeUser)
	if hasSkillInfo(got, "build", skillScopePersonal, personalSkillTypeHub) {
		t.Fatalf("ListSkills returned inactive hub skill: %+v", got)
	}
}

func TestServiceMatchPreviewSameNameConflictFailsClosed(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "build"), "build")
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
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(otherProject, ".agents", "skills", "build"), "build")
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

func TestEffectiveSetProjectPolicyAcceptsLegacyDisplayName(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "Docker 容器化部署"), "Docker 容器化部署", "# project\n")
	writeSkillContent(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "Docker 容器化部署"), "Docker 容器化部署", "# personal\n")
	writeProjectSkillPolicy(t, project, projectSkillPolicy{
		Version: 1,
		DisablePersonalForProject: []projectSkillPolicyDisabledPersonal{{
			Name:         "Docker 容器化部署",
			PersonalType: personalSkillTypeUser,
		}},
	})

	records, conflicts, err := newCanonicalStore(superDolphinHome).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want legacy display policy to resolve same-name conflict", conflicts)
	}
	assertHasCanonicalSkill(t, records, "docker-容器化部署", skillScopeProject, "")
	assertNotHasCanonicalSkill(t, records, "docker-容器化部署", skillScopePersonal, personalSkillTypeUser)
}

func TestEffectiveSetProjectKeepSelectedSuppressesFuturePersonalDuplicate(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	userDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "build")
	writeCanonicalSkill(t, userDir, "build")
	writeProjectSkillPolicy(t, project, projectKeepSelectedPolicy("build", "project/build", "personal/user/build"))
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "imported", "build"), "build")

	effective, conflicts, err := newCanonicalStore(superDolphinHome).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want previous project selection to cover later personal duplicates", conflicts)
	}
	assertHasCanonicalSkill(t, effective, "build", skillScopeProject, "")
	assertNotHasCanonicalSkill(t, effective, "build", skillScopePersonal, personalSkillTypeUser)
	assertNotHasCanonicalSkill(t, effective, "build", skillScopePersonal, personalSkillTypeImported)
}

func TestEffectiveSetIgnoresStaleProjectKeepSelectedWhenSelectedSourceIsMissing(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "build"), "build")
	writeProjectSkillPolicy(t, project, projectSkillPolicy{
		Version: 1,
		KeepSelected: []projectSkillKeepSelected{{
			Name:             "build",
			SelectedSourceID: "project/build",
			ExcludedSourceIDs: []string{
				"personal/user/build",
			},
			Sources: []projectSkillPolicySource{{
				CanonicalID:  "personal/user/build",
				Scope:        skillScopePersonal,
				PersonalType: personalSkillTypeUser,
			}},
		}},
	})

	effective, conflicts, err := newCanonicalStore(superDolphinHome).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want stale project selection ignored", conflicts)
	}
	assertHasCanonicalSkill(t, effective, "build", skillScopePersonal, personalSkillTypeUser)
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

func TestEffectiveSetIgnoresStalePersonalKeepSelectedWhenSelectedSourceIsMissing(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	projectDir := filepath.Join(project, ".agents", "skills", "build")
	writeCanonicalSkill(t, projectDir, "build")
	projectRecord := findCanonicalRecord(
		t,
		mustScanCanonicalRecords(t, newCanonicalStore(superDolphinHome), project),
		"build",
		skillScopeProject,
		"",
	)
	owner, err := resolveOwnerIdentity(superDolphinHome, "1001", "profile-a")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	writePersonalSkillPolicy(t, superDolphinHome, personalSkillPolicy{
		Version:  1,
		OwnerKey: owner.OwnerKey,
		KeepSelected: []personalSkillKeepSelected{{
			Name:                 "build",
			SelectedSourceID:     "personal/imported/build",
			SelectedPersonalType: personalSkillTypeImported,
			SelectedContentHash:  "missing-selected-source",
			ExcludedSourceIDs:    []string{canonicalSourceID(projectRecord)},
			Sources: []personalSkillPolicySource{{
				CanonicalID: canonicalSourceID(projectRecord),
				ContentHash: projectRecord.ContentHash,
				Path:        projectDir,
			}},
		}},
	})

	effective, conflicts, err := newCanonicalStoreForOwner(superDolphinHome, "1001", "profile-a").EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet(owner): %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want stale personal selection ignored", conflicts)
	}
	assertHasCanonicalSkill(t, effective, "build", skillScopeProject, "")
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
	makeOwnerOnlyFileBroadForTest(t, policyPath)

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

func newTestCanonicalStore(_ string, home string) *canonicalStore {
	return newCanonicalStore(filepath.Join(home, ".super-dolphin"))
}

func mustScanCanonicalRecords(t *testing.T, store *canonicalStore, project string) []canonicalSkillRecord {
	t.Helper()
	records, err := store.scan(project)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return records
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
	writeJSONFile(t, filepath.Join(project, ".agents", "skills", projectSkillPolicyFile), policy, 0o644)
}

func projectKeepSelectedPolicy(name, selectedSourceID string, excludedSourceIDs ...string) projectSkillPolicy {
	return projectSkillPolicy{Version: 1, KeepSelected: []projectSkillKeepSelected{projectKeepSelected(name, selectedSourceID, excludedSourceIDs...)}}
}

func projectKeepSelected(name, selectedSourceID string, excludedSourceIDs ...string) projectSkillKeepSelected {
	return projectSkillKeepSelected{Name: name, SelectedSourceID: selectedSourceID, ExcludedSourceIDs: excludedSourceIDs}
}

func writePersonalSkillPolicy(t *testing.T, superDolphinHome string, policy personalSkillPolicy) {
	t.Helper()
	path := filepath.Join(superDolphinHome, "skills", personalSkillPolicyFile)
	writeJSONFile(t, path, policy, 0o600)
	if err := ownerperms.SecureOwnerOnlyFilePermissions(path); err != nil {
		t.Fatalf("secure personal skill policy permissions: %v", err)
	}
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
	if hasSkillInfo(infos, name, scope, personalType) {
		return
	}
	t.Fatalf("missing skill info name=%q scope=%q personal_type=%q in %+v", name, scope, personalType, infos)
}

func hasSkillInfo(infos []SkillInfo, name, scope, personalType string) bool {
	for _, info := range infos {
		if info.Name == name && info.Scope == scope && info.PersonalType == personalType {
			return true
		}
	}
	return false
}
