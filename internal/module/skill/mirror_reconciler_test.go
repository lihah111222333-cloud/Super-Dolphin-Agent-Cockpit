package skill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
)

func TestSkillMirrorReconcilerDetectsProjectAndPersonalDriftActions(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superHome := filepath.Join(home, ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	projectTarget := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: "repo"}
	personalTarget := SkillMirrorTarget{TargetID: "claude:app-managed:owner", Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: filepath.Join(superHome, "providers", "claude", "skills"), CanonicalRootID: "sd_owner:owner"}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{projectTarget, personalTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	writeFileWithMode(t, filepath.Join(projectTarget.Root, "build", "references", "guide.md"), "project drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(personalTarget.Root, "notes", "references", "guide.md"), "personal drift\n", 0o644)

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{projectTarget, personalTarget})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	projectConflict := findMirrorConflict(t, conflicts, "mirror_drift", "build", skillScopeProject)
	assertMirrorConflictActions(t, projectConflict, "sync_back_to_canonical", "canonical_overwrite_mirror", "save_as_new_skill")
	personalConflict := findMirrorConflict(t, conflicts, "mirror_drift", "notes", skillScopePersonal)
	if personalConflict.PersonalType != personalSkillTypeUser {
		t.Fatalf("personal conflict personal_type = %q, want user; conflict=%+v", personalConflict.PersonalType, personalConflict)
	}
	assertMirrorConflictActions(t, personalConflict, "sync_back_to_personal", "personal_overwrite_mirror", "save_as_new_personal_skill")
}

func TestSkillMirrorReconcilerDetectsUnmanagedSameNamePrimitives(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: "repo"}
	writeSkillWithSupportFiles(t, filepath.Join(target.Root, "build"), "build")

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	conflict := findMirrorConflict(t, conflicts, "unmanaged_same_name", "build", skillScopeProject)
	assertMirrorConflictActions(t, conflict, "view_unmanaged", "import_to_personal_imported", "import_to_project", "takeover_provider_skill")
	if conflict.PreviewHash == "" {
		t.Fatalf("preview_hash is empty: %+v", conflict)
	}
}

func TestSkillMirrorReconcilerDetectsCanonicalDeletedAndMultiMirrorDriftKinds(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	claudeTarget := SkillMirrorTarget{TargetID: "claude:project:repo", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: filepath.Join(project, ".claude", "skills"), CanonicalRootID: "repo"}
	codexTarget := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{claudeTarget, codexTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "build", "references", "guide.md"), "claude drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(codexTarget.Root, "build", "references", "guide.md"), "codex drift\n", 0o644)

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{claudeTarget, codexTarget})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts multi: %v", err)
	}
	findMirrorConflict(t, conflicts, "multi_mirror_drift", "build", skillScopeProject)

	if err := os.RemoveAll(filepath.Join(project, ".agent", "skills", "build")); err != nil {
		t.Fatalf("RemoveAll canonical: %v", err)
	}
	records = nil
	conflicts, err = DetectSkillMirrorConflicts(records, []SkillMirrorTarget{claudeTarget})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts deleted: %v", err)
	}
	findMirrorConflict(t, conflicts, "canonical_deleted_with_drift", "build", skillScopeProject)
}

func TestSkillMirrorReconcilerConvertsSameNameConflicts(t *testing.T) {
	conflicts := MirrorConflictsFromCanonical([]canonicalSkillConflict{{
		Kind: "same_name",
		Name: "build",
		Sources: []canonicalSkillConflictSource{
			{Name: "build", Scope: skillScopeProject, ContentHash: "a"},
			{Name: "build", Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, ContentHash: "b"},
		},
	}})

	conflict := findMirrorConflict(t, conflicts, "same_name", "build", "")
	if len(conflict.Sources) != 2 {
		t.Fatalf("same_name sources = %+v, want two", conflict.Sources)
	}
}

func TestSkillMirrorReconcilerSyncBackFailsClosedWithoutAudit(t *testing.T) {
	project := t.TempDir()
	skillDir := filepath.Join(project, ".agent", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	writeFileWithMode(t, filepath.Join(target.Root, "build", "references", "guide.md"), "mirror edit\n", 0o644)
	beforeHash := skillDirContentHash(skillDir)
	previewHash, err := stableMirrorDirectoryHash(filepath.Join(target.Root, "build"))
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project)}

	_, err = ResolveSkillMirrorDrift(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "sync_back_to_canonical",
		Name:        "build",
		Target:      target,
		PreviewHash: previewHash,
	})
	if err == nil || !strings.Contains(err.Error(), "audit") {
		t.Fatalf("ResolveSkillMirrorDrift error = %v, want audit failure", err)
	}
	if afterHash := skillDirContentHash(skillDir); afterHash != beforeHash {
		t.Fatalf("canonical mutated on audit failure: before=%s after=%s", beforeHash, afterHash)
	}
}

func TestSkillMirrorReconcilerSyncBackReportsPartialFailureAfterMutation(t *testing.T) {
	project := t.TempDir()
	skillDir := filepath.Join(project, ".agent", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	writeFileWithMode(t, filepath.Join(target.Root, "build", "references", "guide.md"), "mirror edit\n", 0o644)
	previewHash, err := stableMirrorDirectoryHash(filepath.Join(target.Root, "build"))
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	audit := &failingActionAuditStore{failOnAction: "sync_back_to_canonical_finalize"}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: audit}

	report, err := ResolveSkillMirrorDrift(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "sync_back_to_canonical",
		Name:        "build",
		Target:      target,
		PreviewHash: previewHash,
	})
	if err == nil {
		t.Fatalf("ResolveSkillMirrorDrift error = nil, want finalize partial failure")
	}
	if !report.PartialFailure || report.ResultingHash == "" || report.FollowUpAction == "" {
		t.Fatalf("partial failure report = %+v", report)
	}
	assertFileContent(t, filepath.Join(skillDir, "references", "guide.md"), "mirror edit\n")
}

func TestSkillMirrorReconcilerImportValidatesPreviewAndProviderPath(t *testing.T) {
	project := t.TempDir()
	providerRoot := filepath.Join(project, ".codex", "skills")
	unmanagedDir := filepath.Join(providerRoot, "scratch")
	writeSkillWithSupportFiles(t, unmanagedDir, "scratch")
	previewHash, err := stableMirrorDirectoryHash(unmanagedDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	audit := &capturingSkillAuditStore{}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: filepath.Join(t.TempDir(), ".super-dolphin"), auditStore: audit}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerRoot, CanonicalRootID: "repo"}

	if _, err := ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_project",
		Name:        "scratch",
		Target:      target,
		PreviewHash: "wrong",
	}); err == nil {
		t.Fatalf("ImportUnmanagedProviderSkill accepted wrong preview hash")
	}
	report, err := ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_project",
		Name:        "scratch",
		Target:      target,
		PreviewHash: previewHash,
	})
	if err != nil {
		t.Fatalf("ImportUnmanagedProviderSkill: %v report=%+v", err, report)
	}
	assertFileContent(t, filepath.Join(project, ".agent", "skills", "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
	if _, err := ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_project",
		Name:        "scratch",
		Target:      target,
		PreviewHash: previewHash,
	}); err == nil {
		t.Fatalf("ImportUnmanagedProviderSkill overwrote existing canonical without explicit resolution")
	}

	symlinkRoot := filepath.Join(project, ".bad", "skills")
	if err := os.MkdirAll(filepath.Dir(symlinkRoot), 0o755); err != nil {
		t.Fatalf("MkdirAll symlink parent: %v", err)
	}
	if err := os.Symlink(providerRoot, symlinkRoot); err != nil {
		t.Fatalf("Symlink provider root: %v", err)
	}
	_, err = ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_personal_imported",
		Name:        "scratch",
		Target:      SkillMirrorTarget{TargetID: "bad", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: symlinkRoot, CanonicalRootID: "repo"},
		PreviewHash: previewHash,
	})
	if err == nil {
		t.Fatalf("ImportUnmanagedProviderSkill accepted symlinked provider root")
	}
}

func TestSkillMirrorReconcilerTakeoverValidatesPreviewAndWritesOwnership(t *testing.T) {
	project := t.TempDir()
	ownedRoot := filepath.Join(project, ".claude", "skills")
	ownedDir := filepath.Join(ownedRoot, "owned")
	writeSkillWithSupportFiles(t, ownedDir, "owned")
	ownedHash, err := stableMirrorDirectoryHash(ownedDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash owned: %v", err)
	}
	audit := &capturingSkillAuditStore{}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: audit}
	takeoverTarget := SkillMirrorTarget{TargetID: "claude:project:repo", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: ownedRoot, CanonicalRootID: "repo"}

	if _, err := TakeoverProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "takeover_provider_skill",
		Name:        "owned",
		Target:      takeoverTarget,
		PreviewHash: "wrong",
	}); err == nil {
		t.Fatalf("TakeoverProviderSkill accepted wrong preview hash")
	}
	if _, err := TakeoverProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "takeover_provider_skill",
		Name:        "owned",
		Target:      takeoverTarget,
		PreviewHash: ownedHash,
	}); err != nil {
		t.Fatalf("TakeoverProviderSkill: %v", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(ownedRoot, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read takeover manifest: %v", err)
	}
	if !manifest.Skills["owned"].Owned || manifest.Skills["owned"].MirrorHash != ownedHash {
		t.Fatalf("takeover manifest entry = %+v, want owned mirror_hash %s", manifest.Skills["owned"], ownedHash)
	}
}

func TestSkillsChangedJSONRoundTripWithPersonalType(t *testing.T) {
	original := uidtoSkillsChangedForPersonalType("demo", personalSkillTypeAgent)
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded uidto.SkillsChanged
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.PersonalType != personalSkillTypeAgent {
		t.Fatalf("round-trip personal_type = %q, want agent; decoded=%#v", decoded.PersonalType, decoded)
	}
}

func TestPublishSkillsChangedSeparatesPersonalTypes(t *testing.T) {
	first := normalizeSkillsChanged(uidtoSkillsChangedForPersonalType("same", personalSkillTypeUser))
	second := normalizeSkillsChanged(uidtoSkillsChangedForPersonalType("same", personalSkillTypeAgent))
	if skillsChangedMergeable(first, second) {
		t.Fatalf("personal/user and personal/agent changes with same name should not merge")
	}
}

func findMirrorConflict(t *testing.T, conflicts []SkillMirrorConflict, kind, name, scope string) SkillMirrorConflict {
	t.Helper()
	for _, conflict := range conflicts {
		if conflict.Kind == kind && conflict.Name == name && (scope == "" || conflict.Scope == scope) {
			return conflict
		}
	}
	t.Fatalf("missing conflict kind=%q name=%q scope=%q in %+v", kind, name, scope, conflicts)
	return SkillMirrorConflict{}
}

func assertMirrorConflictActions(t *testing.T, conflict SkillMirrorConflict, want ...string) {
	t.Helper()
	var got []string
	for _, action := range conflict.Actions {
		got = append(got, action.Action)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflict actions = %v, want %v; conflict=%+v", got, want, conflict)
	}
}

func uidtoSkillsChangedForPersonalType(name, personalType string) uidto.SkillsChanged {
	return uidto.SkillsChanged{
		Name:         name,
		Action:       "write",
		Actions:      []string{"write"},
		Count:        1,
		Scope:        skillScopePersonal,
		PersonalType: personalType,
	}
}

type failingActionAuditStore struct {
	failOnAction string
	inserts      []auditstore.InsertParams
}

func (s *failingActionAuditStore) List(context.Context, auditstore.ListFilter) ([]auditstore.AuditEvent, error) {
	return nil, nil
}

func (s *failingActionAuditStore) Insert(_ context.Context, p auditstore.InsertParams) error {
	if s.failOnAction != "" && p.Action == s.failOnAction {
		return errors.New("injected audit failure")
	}
	s.inserts = append(s.inserts, p)
	return nil
}
