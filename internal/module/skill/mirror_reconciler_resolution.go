package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func resolutionRecordAndMirrorDir(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (canonicalSkillRecord, string, error) {
	if svc == nil {
		return canonicalSkillRecord{}, "", fmt.Errorf("skill service is required")
	}
	cwd := cwdFromContext(ctx)
	if strings.TrimSpace(cwd) == "" {
		cwd = svc.projectRoot
	}
	records, err := newCanonicalStore(svc.resolvedSuperDolphinHome()).scan(cwd)
	if err != nil {
		return canonicalSkillRecord{}, "", err
	}
	for _, record := range records {
		if record.Name == req.Name && record.Scope == req.Target.Scope {
			return record, filepath.Join(req.Target.Root, req.Name), nil
		}
	}
	return canonicalSkillRecord{}, "", fmt.Errorf("canonical skill not found: %s", req.Name)
}

func verifyResolutionPreview(dir, want string) (string, error) {
	if err := ensureProviderSkillDirSafe(dir); err != nil {
		return "", err
	}
	got, err := stableMirrorDirectoryHash(dir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(want) == "" || got != strings.TrimSpace(want) {
		return "", fmt.Errorf("skill mirror preview hash mismatch")
	}
	return got, nil
}

func canonicalResolutionTargetDir(svc *service, record canonicalSkillRecord, req SkillMirrorResolutionRequest) (string, error) {
	name := strings.TrimSpace(req.Name)
	if req.NewName != "" {
		var err error
		name, err = validateSkillName(req.NewName)
		if err != nil {
			return "", err
		}
	}
	if record.Scope == skillScopePersonal {
		return filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", record.PersonalType, name), nil
	}
	return filepath.Join(defaultProjectSkillsRoot(svc.projectRoot), name), nil
}

func replaceSkillDirFromMirror(sourceDir, targetDir string) error {
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return err
	}
	_, _, err := copySkillDir(sourceDir, targetDir)
	return err
}

func backupSkillDir(targetDir string) (string, error) {
	if !skillMainFileExists(targetDir) {
		return "", nil
	}
	backupDir := filepath.Join(filepath.Dir(targetDir), ".super-dolphin-mirror-backup", filepath.Base(targetDir)+"-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(filepath.Dir(backupDir), 0o755); err != nil {
		return "", err
	}
	if _, _, err := copySkillDir(targetDir, backupDir); err != nil {
		_ = os.RemoveAll(backupDir)
		return "", err
	}
	return backupDir, nil
}

func unmanagedProviderSource(req SkillMirrorResolutionRequest) (string, string, error) {
	if err := validateExistingMirrorRoot(req.Target); err != nil {
		return "", "", err
	}
	name, err := validateSkillName(req.Name)
	if err != nil {
		return "", "", err
	}
	sourceDir := filepath.Join(req.Target.Root, name)
	hash, err := verifyResolutionPreview(sourceDir, req.PreviewHash)
	if err != nil {
		return "", "", err
	}
	return sourceDir, hash, nil
}

func importCanonicalTargetDir(svc *service, req SkillMirrorResolutionRequest) (string, string, string, error) {
	name, err := validateSkillName(req.Name)
	if err != nil {
		return "", "", "", err
	}
	switch req.Action {
	case "import_to_personal_imported":
		if svc == nil {
			return "", "", "", fmt.Errorf("skill service is required")
		}
		return filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeImported, name), skillScopePersonal, personalSkillTypeImported, nil
	case "import_to_project":
		if svc == nil {
			return "", "", "", fmt.Errorf("skill service is required")
		}
		return filepath.Join(defaultProjectSkillsRoot(svc.projectRoot), name), skillScopeProject, "", nil
	default:
		return "", "", "", fmt.Errorf("unsupported import action %q", req.Action)
	}
}

func validateExistingMirrorRoot(target SkillMirrorTarget) error {
	if err := validateSkillMirrorTarget(target); err != nil {
		return err
	}
	info, err := os.Lstat(target.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill mirror root is symlink: %s", target.Root)
	}
	if !info.IsDir() {
		return fmt.Errorf("skill mirror root is not a directory: %s", target.Root)
	}
	return nil
}

func ensureProviderSkillDirSafe(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider skill dir is symlink: %s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("provider skill path is not a directory: %s", dir)
	}
	return nil
}

func writeTakeoverManifest(target SkillMirrorTarget, name, mirrorHash string) error {
	manifest, err := readTargetManifest(target)
	if err != nil {
		return err
	}
	manifest.Skills[name] = SkillMirrorEntry{
		CanonicalID:   target.Scope + "/" + name,
		CanonicalHash: mirrorHash,
		MirrorHash:    mirrorHash,
		SourceType:    target.Scope,
		Owned:         true,
	}
	manifest.GeneratedAt = time.Now().UTC()
	return writeSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile), manifest)
}

func updateOwnedMirrorManifest(target SkillMirrorTarget, record canonicalSkillRecord, mirrorHash string) error {
	manifest, err := readTargetManifest(target)
	if err != nil {
		return err
	}
	canonicalHash, err := stableMirrorDirectoryHash(record.Dir)
	if err != nil {
		return err
	}
	manifest.Skills[record.Name] = mirrorManifestEntry(record, canonicalHash, mirrorHash)
	manifest.GeneratedAt = time.Now().UTC()
	return writeSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile), manifest)
}

func newMirrorMutationAuditRecord(action, name, scope, personalType, oldHash, newHash string) skillMirrorMutationAuditRecord {
	return skillMirrorMutationAuditRecord{
		Action:       action,
		CanonicalID:  canonicalSourceID(canonicalSkillRecord{Name: name, Scope: scope, PersonalType: personalType}),
		Scope:        scope,
		PersonalType: personalType,
		Name:         name,
		OldHash:      oldHash,
		NewHash:      newHash,
		Actor:        "super-dolphin",
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func partialMirrorResolutionReport(report SkillMirrorResolutionReport, resultingHash, followUp string) SkillMirrorResolutionReport {
	report.ResultingHash = resultingHash
	report.PartialFailure = true
	report.FollowUpAction = followUp
	return report
}
