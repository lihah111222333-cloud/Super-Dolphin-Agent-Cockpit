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
	personalType, err := resolutionRequestPersonalType(req)
	if err != nil {
		return canonicalSkillRecord{}, "", err
	}
	for _, record := range records {
		if record.Name == req.Name && record.Scope == req.Target.Scope && resolutionRecordPersonalTypeMatches(record, personalType) {
			return record, filepath.Join(req.Target.Root, req.Name), nil
		}
	}
	return canonicalSkillRecord{}, "", fmt.Errorf("canonical skill not found: %s", req.Name)
}

func resolutionRequestPersonalType(req SkillMirrorResolutionRequest) (string, error) {
	if req.Target.Scope != skillScopePersonal {
		return "", nil
	}
	manifest, err := readTargetManifest(req.Target)
	if err != nil {
		return "", err
	}
	entry, ok := manifest.Skills[req.Name]
	if !ok {
		return "", nil
	}
	if strings.TrimSpace(entry.PersonalType) != "" {
		return strings.TrimSpace(entry.PersonalType), nil
	}
	return personalTypeFromCanonicalID(entry.CanonicalID), nil
}

func resolutionRecordPersonalTypeMatches(record canonicalSkillRecord, personalType string) bool {
	if record.Scope != skillScopePersonal || strings.TrimSpace(personalType) == "" {
		return true
	}
	return record.PersonalType == personalType
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

func verifyResolutionRequestPreview(svc *service, req SkillMirrorResolutionRequest, mirrorDir string) (skillResolutionPreviewItem, string, error) {
	if strings.TrimSpace(req.PreviewID) == "" {
		hash, err := verifyResolutionPreview(mirrorDir, req.PreviewHash)
		return skillResolutionPreviewItem{}, hash, err
	}
	preview, err := svc.lookupResolutionPreview(req.PreviewID, req.Action, req.PreviewHash)
	if err != nil {
		return skillResolutionPreviewItem{}, "", err
	}
	hash, err := verifyResolutionPreviewMirrorBinding(preview, mirrorDir)
	if err != nil {
		return skillResolutionPreviewItem{}, "", err
	}
	return preview, hash, nil
}

func (s *service) lookupResolutionPreview(previewID, action, previewHash string) (skillResolutionPreviewItem, error) {
	if s == nil {
		return skillResolutionPreviewItem{}, fmt.Errorf("skill service is required")
	}
	now := time.Now().UTC()
	id := strings.TrimSpace(previewID)
	s.resolutionPreviewMu.Lock()
	defer s.resolutionPreviewMu.Unlock()
	stored, ok := s.resolutionPreviews[id]
	if !ok {
		return skillResolutionPreviewItem{}, fmt.Errorf("skill resolution preview not found or expired")
	}
	if now.After(stored.ExpiresAt) {
		delete(s.resolutionPreviews, id)
		return skillResolutionPreviewItem{}, fmt.Errorf("skill resolution preview not found or expired")
	}
	if stored.Action != action || stored.Item.Action != action {
		return skillResolutionPreviewItem{}, fmt.Errorf("skill resolution preview action mismatch")
	}
	if strings.TrimSpace(previewHash) == "" || stored.Item.PreviewHash != strings.TrimSpace(previewHash) {
		return skillResolutionPreviewItem{}, fmt.Errorf("skill resolution preview hash mismatch")
	}
	return stored.Item, nil
}

func verifyResolutionPreviewMirrorBinding(preview skillResolutionPreviewItem, mirrorDir string) (string, error) {
	if err := ensureProviderSkillDirSafe(mirrorDir); err != nil {
		return "", err
	}
	hash, err := stableMirrorDirectoryHash(mirrorDir)
	if err != nil {
		return "", err
	}
	want := ""
	switch {
	case sameResolutionPath(preview.SourcePath, mirrorDir):
		want = preview.SourceHash
	case sameResolutionPath(preview.TargetPath, mirrorDir):
		want = preview.TargetHash
	default:
		return "", fmt.Errorf("skill resolution preview path mismatch")
	}
	if strings.TrimSpace(want) == "" || want != hash {
		return "", fmt.Errorf("skill resolution preview source hash mismatch")
	}
	return hash, nil
}

func verifyResolutionPreviewTarget(preview skillResolutionPreviewItem, targetDir string) error {
	if preview == (skillResolutionPreviewItem{}) {
		return nil
	}
	if !sameResolutionPath(preview.TargetPath, targetDir) {
		return fmt.Errorf("skill resolution preview target mismatch")
	}
	return nil
}

func sameResolutionPath(previewPath, path string) bool {
	if strings.TrimSpace(previewPath) == "" {
		return false
	}
	return sameCleanPath(filepath.FromSlash(previewPath), path)
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

func unmanagedProviderSource(svc *service, req SkillMirrorResolutionRequest) (string, string, skillResolutionPreviewItem, error) {
	if err := validateExistingMirrorRoot(req.Target); err != nil {
		return "", "", skillResolutionPreviewItem{}, err
	}
	name, err := validateSkillName(req.Name)
	if err != nil {
		return "", "", skillResolutionPreviewItem{}, err
	}
	sourceDir := filepath.Join(req.Target.Root, name)
	preview, hash, err := verifyResolutionRequestPreview(svc, req, sourceDir)
	if err != nil {
		return "", "", skillResolutionPreviewItem{}, err
	}
	return sourceDir, hash, preview, nil
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
	if err := rejectSymlinkAncestors(target.Root); err != nil {
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

func confirmDeleteDriftedMirror(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	if svc == nil {
		return report, fmt.Errorf("skill service is required")
	}
	target, err := confirmDeleteMirrorTarget(req)
	if err != nil {
		return report, err
	}
	preview, mirrorHash, err := verifyResolutionRequestPreview(svc, req, target.mirrorDir)
	if err != nil {
		return report, err
	}
	if err := verifyResolutionPreviewTarget(preview, target.mirrorDir); err != nil {
		return report, err
	}
	if _, err := backupSkillDir(target.mirrorDir); err != nil {
		return report, err
	}
	auditRecord := confirmDeleteAuditRecord(req, target, mirrorHash)
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := removeManagedMirror(req.Target, target); err != nil {
		return partialMirrorResolutionReport(report, "", "retry_manifest_write"), err
	}
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		return partialMirrorResolutionReport(report, "", "retry_audit_finalize"), err
	}
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, target.name, req.Target.Scope, target.personalType)
	return report, nil
}

type confirmDeleteMirrorDetails struct {
	name         string
	mirrorDir    string
	personalType string
	entry        SkillMirrorEntry
	manifest     SkillMirrorManifest
}

func confirmDeleteMirrorTarget(req SkillMirrorResolutionRequest) (confirmDeleteMirrorDetails, error) {
	if err := validateExistingMirrorRoot(req.Target); err != nil {
		return confirmDeleteMirrorDetails{}, err
	}
	name, err := validateSkillName(req.Name)
	if err != nil {
		return confirmDeleteMirrorDetails{}, err
	}
	manifest, err := readTargetManifest(req.Target)
	if err != nil {
		return confirmDeleteMirrorDetails{}, err
	}
	entry, ok := manifest.Skills[name]
	if !ok {
		return confirmDeleteMirrorDetails{}, fmt.Errorf("managed skill mirror not found: %s", name)
	}
	personalType := strings.TrimSpace(entry.PersonalType)
	if personalType == "" {
		personalType = personalTypeFromCanonicalID(entry.CanonicalID)
	}
	return confirmDeleteMirrorDetails{
		name:         name,
		mirrorDir:    filepath.Join(req.Target.Root, name),
		personalType: personalType,
		entry:        entry,
		manifest:     manifest,
	}, nil
}

func confirmDeleteAuditRecord(req SkillMirrorResolutionRequest, target confirmDeleteMirrorDetails, oldHash string) skillMirrorMutationAuditRecord {
	record := newMirrorMutationAuditRecord(req.Action, target.name, req.Target.Scope, target.personalType, oldHash, "")
	if target.entry.CanonicalID != "" {
		record.CanonicalID = target.entry.CanonicalID
	}
	return record
}

func removeManagedMirror(target SkillMirrorTarget, details confirmDeleteMirrorDetails) error {
	if err := os.RemoveAll(details.mirrorDir); err != nil {
		return err
	}
	delete(details.manifest.Skills, details.name)
	details.manifest.GeneratedAt = time.Now().UTC()
	return writeSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile), details.manifest)
}

func buildResolutionPreviewItems(item skillResolutionItem, p skillResolutionPreviewParams, superHome string) ([]skillResolutionPreviewItem, error) {
	if len(item.ProviderEntries) > 1 && previewAllProviders(item, p) {
		out := make([]skillResolutionPreviewItem, 0, len(item.ProviderEntries))
		for _, entry := range item.ProviderEntries {
			next := p
			next.Provider = entry.Provider
			preview, err := buildResolutionPreviewItem(item, next, superHome)
			if err != nil {
				return nil, err
			}
			out = append(out, preview)
		}
		return out, nil
	}
	preview, err := buildResolutionPreviewItem(item, p, superHome)
	if err != nil {
		return nil, err
	}
	return []skillResolutionPreviewItem{preview}, nil
}

func previewAllProviders(item skillResolutionItem, p skillResolutionPreviewParams) bool {
	return p.Provider == "" && p.SourceProvider == "" && p.SourcePathID == "" &&
		(p.Action == ResolutionViewDiff || overwriteResolutionAction(p.Action) || (syncBackResolutionAction(p.Action) && sameResolutionSourceHashes(item.ProviderEntries)))
}

func buildResolutionPreviewItem(item skillResolutionItem, p skillResolutionPreviewParams, superHome string) (skillResolutionPreviewItem, error) {
	if len(item.ProviderEntries) == 0 {
		return canonicalResolutionPreviewItem(item, p)
	}
	provider, err := selectedResolutionPreviewProvider(item, p)
	if err != nil {
		return skillResolutionPreviewItem{}, err
	}
	entry, err := selectResolutionProviderEntry(item, provider)
	if err != nil {
		return skillResolutionPreviewItem{}, err
	}
	preview := resolutionPreviewPaths(item, entry, p, superHome)
	if p.IncludeDiff || p.Action == ResolutionViewDiff || p.Action == ResolutionViewUnmanaged {
		preview.Diff = resolutionPreviewDiff(preview)
	}
	if p.Action == ResolutionViewDiff || p.Action == ResolutionViewUnmanaged {
		return preview, nil
	}
	if err := validateMutatingResolutionPreview(item, preview, p); err != nil {
		return skillResolutionPreviewItem{}, err
	}
	preview.BackupPath = resolutionPreviewBackupPath(preview.TargetPath, item, p)
	preview.PreviewHash = resolutionPreviewHash(item, preview, p)
	return preview, nil
}

func selectedResolutionPreviewProvider(item skillResolutionItem, p skillResolutionPreviewParams) (string, error) {
	provider := strings.TrimSpace(p.Provider)
	if p.SourceProvider != "" {
		provider = strings.TrimSpace(p.SourceProvider)
	}
	if strings.HasPrefix(p.SourcePathID, "provider:") {
		pathProvider := strings.TrimPrefix(p.SourcePathID, "provider:")
		if provider != "" && provider != pathProvider {
			return "", fmt.Errorf("source_provider does not match source_path_id")
		}
		provider = pathProvider
	}
	if item.Kind == skillConflictMultiMirrorDrift && syncBackResolutionAction(p.Action) && !sameResolutionSourceHashes(item.ProviderEntries) && provider == "" {
		return "", fmt.Errorf("source_provider is required when multi mirror drift source hashes differ")
	}
	return provider, nil
}

func selectResolutionProviderEntry(item skillResolutionItem, provider string) (skillResolutionProviderEntry, error) {
	if len(item.ProviderEntries) == 0 {
		return skillResolutionProviderEntry{}, fmt.Errorf("resolution conflict has no provider entry")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" && len(item.ProviderEntries) == 1 {
		return item.ProviderEntries[0], nil
	}
	for _, entry := range item.ProviderEntries {
		if entry.Provider == provider {
			return entry, nil
		}
	}
	return skillResolutionProviderEntry{}, fmt.Errorf("provider %q is not part of resolution conflict", provider)
}

func resolutionPreviewPaths(item skillResolutionItem, entry skillResolutionProviderEntry, p skillResolutionPreviewParams, superHome string) skillResolutionPreviewItem {
	preview := skillResolutionPreviewItem{Action: p.Action, Provider: entry.Provider, SourceProvider: entry.Provider, SourcePathID: entry.SourcePathID}
	switch p.Action {
	case ResolutionCanonicalOverwrite, ResolutionPersonalOverwrite:
		preview.SourcePath, preview.TargetPath = entry.TargetPath, entry.SourcePath
		preview.SourceHash, preview.TargetHash = entry.TargetHash, entry.SourceHash
	case ResolutionConfirmDeleteDriftedMirror:
		preview.SourcePath, preview.TargetPath = entry.SourcePath, entry.SourcePath
		preview.SourceHash, preview.TargetHash = entry.SourceHash, entry.SourceHash
		preview.ConfirmDeleteMirrorHash = entry.SourceHash
	case ResolutionImportPersonal:
		preview.SourcePath, preview.TargetPath = entry.SourcePath, filepath.ToSlash(filepath.Join(superHome, "skills", "personal", personalSkillTypeImported, item.Name))
		preview.SourceHash, preview.TargetHash = entry.SourceHash, ""
	default:
		preview.SourcePath, preview.TargetPath = entry.SourcePath, resolutionPreviewTargetPath(entry, p)
		preview.SourceHash, preview.TargetHash = entry.SourceHash, entry.TargetHash
	}
	return preview
}

func resolutionPreviewTargetPath(entry skillResolutionProviderEntry, p skillResolutionPreviewParams) string {
	name := strings.TrimSpace(p.NewName)
	if name == "" {
		return entry.TargetPath
	}
	return filepath.ToSlash(filepath.Join(filepath.Dir(entry.TargetPath), name))
}

func validateMutatingResolutionPreview(item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionPreviewParams) error {
	if p.Action == ResolutionSaveAsNewSkill || p.Action == ResolutionSaveAsNewPersonal {
		if strings.TrimSpace(p.NewName) == "" {
			return fmt.Errorf("new_name is required for %s", p.Action)
		}
		if _, err := validateSkillName(p.NewName); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.FromSlash(preview.TargetPath)); err == nil {
			return fmt.Errorf("new skill target already exists: %s", preview.TargetPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
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
