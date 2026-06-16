package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	skillidentity "github.com/anthropic-ai/super-agent-v3/internal/module/skill/identity"
)

// resolutionRecordAndMirrorDir 处理resolution记录镜像目录。
func resolutionRecordAndMirrorDir(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (canonicalSkillRecord, string, error) {
	if svc == nil {
		return canonicalSkillRecord{}, "", fmt.Errorf("skill service is required")
	}
	name, _, err := normalizeSkillIdentityName(req.Name, "")
	if err != nil {
		return canonicalSkillRecord{}, "", err
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
	if record, ok := findResolutionRecord(records, req, name, personalType); ok {
		return record, filepath.Join(req.Target.Root, name), nil
	}
	if record, mirrorDir, ok, err := deletedCanonicalResolutionRecord(svc, cwd, req, name, personalType); err != nil || ok {
		return record, mirrorDir, err
	}
	return canonicalSkillRecord{}, "", fmt.Errorf("canonical skill not found: %s", name)
}

func findResolutionRecord(records []canonicalSkillRecord, req SkillMirrorResolutionRequest, name, personalType string) (canonicalSkillRecord, bool) {
	for _, record := range records {
		if record.Name == name && record.Scope == req.Target.Scope && resolutionRecordPersonalTypeMatches(record, personalType) {
			return record, true
		}
	}
	return canonicalSkillRecord{}, false
}

func deletedCanonicalResolutionRecord(svc *service, cwd string, req SkillMirrorResolutionRequest, name, personalType string) (canonicalSkillRecord, string, bool, error) {
	if !deletedCanonicalRestoreAction(req.Action) {
		return canonicalSkillRecord{}, "", false, nil
	}
	manifest, err := readTargetManifest(req.Target)
	if err != nil {
		return canonicalSkillRecord{}, "", true, err
	}
	entry, ok := manifest.Skills[name]
	if !ok {
		return canonicalSkillRecord{}, "", false, nil
	}
	scope := deletedCanonicalScope(req.Target.Scope)
	personalType = deletedCanonicalPersonalType(personalType, entry)
	record := deletedCanonicalRecord(svc, cwd, name, scope, personalType, entry.CanonicalHash)
	return record, filepath.Join(req.Target.Root, name), true, nil
}

func deletedCanonicalRestoreAction(action string) bool {
	switch action {
	case ResolutionSyncBackCanonical, ResolutionSyncBackPersonal, ResolutionSaveAsNewSkill, ResolutionSaveAsNewPersonal:
		return true
	default:
		return false
	}
}

func deletedCanonicalScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return skillScopeProject
	}
	return scope
}

func deletedCanonicalPersonalType(personalType string, entry SkillMirrorEntry) string {
	if personalType != "" {
		return personalType
	}
	if entry.PersonalType != "" {
		return strings.TrimSpace(entry.PersonalType)
	}
	return personalTypeFromCanonicalID(entry.CanonicalID)
}

func deletedCanonicalRecord(svc *service, cwd, name, scope, personalType, canonicalHash string) canonicalSkillRecord {
	record := canonicalSkillRecord{Name: name, Scope: scope, PersonalType: personalType, DirHash: canonicalHash}
	if scope == skillScopePersonal {
		record.Dir = filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalType, record.Name)
		return record
	}
	record.Dir = filepath.Join(defaultProjectSkillsRoot(projectRootForCWD(cwd, "")), record.Name)
	return record
}

// resolutionRequestPersonalType 处理resolution请求personaltype。
func resolutionRequestPersonalType(req SkillMirrorResolutionRequest) (string, error) {
	if req.Target.Scope != skillScopePersonal {
		return "", nil
	}
	name, _, err := normalizeSkillIdentityName(req.Name, "")
	if err != nil {
		return "", err
	}
	manifest, err := readTargetManifest(req.Target)
	if err != nil {
		return "", err
	}
	entry, ok := manifest.Skills[name]
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
	preview, _, err := s.lookupResolutionPreviewStored(previewID, action, previewHash)
	return preview, err
}

func (s *service) lookupResolutionPreviewForConflict(previewID, conflictID, action, previewHash string) (skillResolutionPreviewItem, error) {
	preview, stored, err := s.lookupResolutionPreviewStored(previewID, action, previewHash)
	if err != nil {
		return skillResolutionPreviewItem{}, err
	}
	if stored.ConflictID != strings.TrimSpace(conflictID) {
		return skillResolutionPreviewItem{}, fmt.Errorf("skill resolution preview conflict mismatch")
	}
	return preview, nil
}

// lookupResolutionPreviewStored 处理lookupresolutionpreviewstored。
func (s *service) lookupResolutionPreviewStored(previewID, action, previewHash string) (skillResolutionPreviewItem, skillResolutionStoredPreview, error) {
	if s == nil {
		return skillResolutionPreviewItem{}, skillResolutionStoredPreview{}, fmt.Errorf("skill service is required")
	}
	now := time.Now().UTC()
	id := strings.TrimSpace(previewID)
	s.resolutionPreviewMu.Lock()
	defer s.resolutionPreviewMu.Unlock()
	stored, ok := s.resolutionPreviews[id]
	if !ok {
		return skillResolutionPreviewItem{}, skillResolutionStoredPreview{}, fmt.Errorf("skill resolution preview not found or expired")
	}
	if now.After(stored.ExpiresAt) {
		delete(s.resolutionPreviews, id)
		return skillResolutionPreviewItem{}, skillResolutionStoredPreview{}, fmt.Errorf("skill resolution preview not found or expired")
	}
	if stored.Action != action || stored.Item.Action != action {
		return skillResolutionPreviewItem{}, skillResolutionStoredPreview{}, fmt.Errorf("skill resolution preview action mismatch")
	}
	if strings.TrimSpace(previewHash) == "" || stored.Item.PreviewHash != strings.TrimSpace(previewHash) {
		return skillResolutionPreviewItem{}, skillResolutionStoredPreview{}, fmt.Errorf("skill resolution preview hash mismatch")
	}
	return stored.Item, stored, nil
}

// verifyResolutionPreviewMirrorBinding 验证resolutionpreview镜像binding。
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
	if preview != (skillResolutionPreviewItem{}) && !sameResolutionPath(preview.TargetPath, targetDir) {
		return fmt.Errorf("skill resolution preview target mismatch")
	}
	return nil
}

func resolveOriginalMirrorAfterSaveAsNew(req SkillMirrorResolutionRequest, prepared canonicalMirrorResolution) error {
	newHash, err := replaceMirrorSkillDir(req.Target.Root, prepared.record.Name, prepared.record.Dir, req.Target.Scope, prepared.record.info.DisplayName)
	if err != nil {
		return err
	}
	return updateOwnedMirrorManifest(req.Target, prepared.record, newHash)
}

func rewriteCopiedSkillIdentity(dir, newName, displayName string) error {
	name, err := validateSkillName(newName)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, skillMainFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content, ok := skillidentity.RewriteFrontmatter(string(data), name, displayName)
	if !ok {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func sameResolutionPath(previewPath, path string) bool {
	if strings.TrimSpace(previewPath) == "" {
		return false
	}
	return sameCleanPath(filepath.FromSlash(previewPath), path)
}

func canonicalResolutionTargetDir(svc *service, record canonicalSkillRecord, req SkillMirrorResolutionRequest) (string, error) {
	name := strings.TrimSpace(record.Name)
	if req.NewName != "" {
		var err error
		name, err = validateSkillName(req.NewName)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(record.Dir) != "" {
		return filepath.Join(filepath.Dir(record.Dir), name), nil
	}
	if record.Scope == skillScopePersonal {
		return filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", record.PersonalType, name), nil
	}
	return filepath.Join(defaultProjectSkillsRoot(projectRootForCWD("", svc.projectRoot)), name), nil
}

func unmanagedProviderSource(svc *service, req SkillMirrorResolutionRequest) (string, string, skillResolutionPreviewItem, error) {
	if err := validateExistingMirrorRoot(req.Target); err != nil {
		return "", "", skillResolutionPreviewItem{}, err
	}
	name, displayName, err := normalizeSkillIdentityName(req.Name, "")
	if err != nil {
		return "", "", skillResolutionPreviewItem{}, err
	}
	var lastErr error
	for _, sourceName := range uniqStrings([]string{displayName, strings.TrimSpace(req.Name), name}) {
		sourceDir := filepath.Join(req.Target.Root, sourceName)
		preview, hash, err := verifyResolutionRequestPreview(svc, req, sourceDir)
		if err == nil {
			return sourceDir, hash, preview, nil
		}
		lastErr = err
	}
	return "", "", skillResolutionPreviewItem{}, lastErr
}

// importCanonicalTargetDir 导入canonicaltarget目录。
func importCanonicalTargetDir(svc *service, req SkillMirrorResolutionRequest) (string, string, string, error) {
	name, _, err := normalizeSkillIdentityName(req.Name, "")
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
		projectRoot, err := projectRootFromMirrorTarget(req.Target)
		if err != nil {
			return "", "", "", err
		}
		return filepath.Join(defaultProjectSkillsRoot(projectRoot), name), skillScopeProject, "", nil
	default:
		return "", "", "", fmt.Errorf("unsupported import action %q", req.Action)
	}
}

// projectRootFromMirrorTarget 从镜像target处理项目根目录。
func projectRootFromMirrorTarget(target SkillMirrorTarget) (string, error) {
	if target.Scope != skillScopeProject {
		return "", fmt.Errorf("project mirror target is required")
	}
	root := filepath.Clean(target.Root)
	parent := filepath.Base(filepath.Dir(root))
	if filepath.Base(root) != "skills" || !strings.HasPrefix(parent, ".") {
		return "", fmt.Errorf("project mirror root has unexpected shape: %s", target.Root)
	}
	provider := strings.TrimPrefix(parent, ".")
	if provider == "agents" {
		provider = string(SkillProviderCodex)
	}
	if SkillProvider(provider) != SkillProviderClaude && SkillProvider(provider) != SkillProviderCodex {
		return "", fmt.Errorf("project mirror root has unsupported provider: %s", target.Root)
	}
	return filepath.Dir(filepath.Dir(root)), nil
}

// confirmDeleteDriftedMirror 处理confirmdeletedrifted镜像。
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

// confirmDeleteMirrorTarget 处理confirmdelete镜像target。
func confirmDeleteMirrorTarget(req SkillMirrorResolutionRequest) (confirmDeleteMirrorDetails, error) {
	if err := validateExistingMirrorRoot(req.Target); err != nil {
		return confirmDeleteMirrorDetails{}, err
	}
	name, _, err := normalizeSkillIdentityName(req.Name, "")
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

// buildResolutionPreviewItems 构建resolutionpreviewitems。
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

// previewAllProviders 处理previewallproviders。
func previewAllProviders(item skillResolutionItem, p skillResolutionPreviewParams) bool {
	return p.Provider == "" && p.SourceProvider == "" && p.SourcePathID == "" &&
		(p.Action == ResolutionViewDiff || p.Action == ResolutionViewUnmanaged || overwriteResolutionAction(p.Action) || (syncBackResolutionAction(p.Action) && sameResolutionSourceHashes(item.ProviderEntries)))
}

// buildResolutionPreviewItem 构建resolutionpreviewitem。
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

// selectedResolutionPreviewProvider 处理selectedresolutionpreviewprovider。
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

// selectResolutionProviderEntry 选择resolutionprovider条目。
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

func resolutionPreviewTargetPath(entry skillResolutionProviderEntry, p skillResolutionPreviewParams) string {
	name := strings.TrimSpace(p.NewName)
	if name == "" {
		return entry.TargetPath
	}
	return filepath.ToSlash(filepath.Join(filepath.Dir(entry.TargetPath), name))
}

// validateMutatingResolutionPreview 校验mutatingresolutionpreview。
func validateMutatingResolutionPreview(item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionPreviewParams) error {
	if p.Action == ResolutionReplaceProviderRootSymlink {
		return validateRootResolutionPreview(item, preview, p.Action)
	}
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

func validateRootResolutionPreview(item skillResolutionItem, preview skillResolutionPreviewItem, action string) error {
	wantKind := skillConflictMirrorRootSymlink
	if item.Kind != wantKind {
		return fmt.Errorf("%s requires %s", action, wantKind)
	}
	if strings.TrimSpace(preview.SourcePath) == "" || !sameResolutionPath(preview.SourcePath, preview.TargetPath) {
		return fmt.Errorf("%s preview requires matching source and target paths", action)
	}
	if strings.TrimSpace(preview.SourceHash) == "" {
		return fmt.Errorf("%s preview requires source hash", action)
	}
	return nil
}

func updateOwnedMirrorManifest(target SkillMirrorTarget, record canonicalSkillRecord, mirrorHash string) error {
	manifest, err := readTargetManifest(target)
	if err != nil {
		return err
	}
	if actualHash, exists, err := existingMirrorHash(filepath.Join(target.Root, record.Name)); err != nil {
		return err
	} else if exists {
		mirrorHash = actualHash
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
