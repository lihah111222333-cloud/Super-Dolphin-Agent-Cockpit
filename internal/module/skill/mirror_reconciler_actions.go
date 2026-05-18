package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolveSkillMirrorDrift(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	req.Action = normalizeResolutionAction(req.Action)
	switch req.Action {
	case "sync_back_to_canonical", "sync_back_to_personal":
		return syncBackMirrorToCanonical(ctx, svc, req)
	case "canonical_overwrite_mirror", "personal_overwrite_mirror":
		return overwriteMirrorFromCanonical(ctx, svc, req)
	case "save_as_new_skill", "save_as_new_personal_skill":
		return saveMirrorAsNewCanonical(ctx, svc, req)
	case "confirm_delete_drifted_mirror":
		return confirmDeleteDriftedMirror(ctx, svc, req)
	default:
		return SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}, fmt.Errorf("unsupported mirror resolution action %q", req.Action)
	}
}

func applyUnmanagedProviderResolution(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	switch req.Action {
	case ResolutionImportPersonal, ResolutionImportProject:
		return ImportUnmanagedProviderSkill(ctx, svc, req)
	case ResolutionTakeoverProvider:
		return TakeoverProviderSkill(ctx, svc, req)
	default:
		return SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}, fmt.Errorf("unsupported unmanaged resolution action %q", req.Action)
	}
}

func (s *service) resolutionApplyTarget(cwd string, item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionApplyParams) (SkillMirrorTarget, error) {
	provider, err := resolutionApplyProvider(preview, p)
	if err != nil {
		return SkillMirrorTarget{}, err
	}
	entry, err := selectResolutionProviderEntry(item, provider)
	if err != nil {
		return SkillMirrorTarget{}, err
	}
	if err := validateResolutionApplyPreviewSource(entry, preview); err != nil {
		return SkillMirrorTarget{}, err
	}
	return s.findResolutionApplyTarget(cwd, item, entry)
}

func resolutionApplyProvider(preview skillResolutionPreviewItem, p skillResolutionApplyParams) (string, error) {
	provider := strings.TrimSpace(p.Provider)
	if preview.Provider != "" {
		provider = preview.Provider
	}
	if preview.SourceProvider != "" {
		provider = preview.SourceProvider
	}
	sourcePathID := strings.TrimSpace(p.SourcePathID)
	if sourcePathID == "" {
		sourcePathID = preview.SourcePathID
	}
	if strings.HasPrefix(sourcePathID, "provider:") {
		return resolutionApplyProviderFromPathID(provider, sourcePathID)
	}
	return provider, nil
}

func resolutionApplyProviderFromPathID(provider, sourcePathID string) (string, error) {
	pathProvider := strings.TrimPrefix(sourcePathID, "provider:")
	if provider != "" && provider != pathProvider {
		return "", fmt.Errorf("source_provider does not match source_path_id")
	}
	return pathProvider, nil
}

func validateResolutionApplyPreviewSource(entry skillResolutionProviderEntry, preview skillResolutionPreviewItem) error {
	if preview.SourcePathID != "" && entry.SourcePathID != preview.SourcePathID {
		return fmt.Errorf("skill resolution preview source mismatch")
	}
	return nil
}

func (s *service) findResolutionApplyTarget(cwd string, item skillResolutionItem, entry skillResolutionProviderEntry) (SkillMirrorTarget, error) {
	for _, target := range s.resolutionMirrorTargets(cwd) {
		if resolutionApplyTargetMatches(target, item, entry) {
			return target, nil
		}
	}
	return SkillMirrorTarget{}, fmt.Errorf("skill mirror target not found for provider %q", entry.Provider)
}

func resolutionApplyTargetMatches(target SkillMirrorTarget, item skillResolutionItem, entry skillResolutionProviderEntry) bool {
	if entry.TargetID != "" {
		return target.TargetID == entry.TargetID
	}
	return string(target.Provider) == entry.Provider && target.Scope == item.Scope && target.Root == filepath.Dir(filepath.FromSlash(entry.SourcePath))
}

func resolutionApplyName(p skillResolutionApplyParams, item skillResolutionItem) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return item.Name
	}
	return name
}

func syncBackMirrorToCanonical(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	prepared, err := prepareCanonicalMirrorResolution(ctx, svc, req)
	if err != nil {
		return report, err
	}
	if _, err := backupSkillDir(prepared.targetDir); err != nil {
		return report, err
	}
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.Name, prepared.record.Scope, prepared.record.PersonalType, skillDirContentHash(prepared.targetDir), "")
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := replaceSkillDirFromMirror(prepared.mirrorDir, prepared.targetDir); err != nil {
		return report, err
	}
	if err := setMirrorResolutionResultHash(&report, prepared.targetDir); err != nil {
		return report, err
	}
	auditRecord.NewHash = report.ResultingHash
	if err := updateOwnedMirrorManifest(req.Target, prepared.record, prepared.mirrorHash); err != nil {
		return partialMirrorResolutionReport(report, report.ResultingHash, "retry_manifest_write"), err
	}
	markResolutionMirrorPublish(ctx, svc, req, prepared.record.Scope, prepared.record.PersonalType, req.Name, &report)
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		report.PartialFailure = true
		report.FollowUpAction = "retry_audit_finalize"
		return report, err
	}
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.Name, prepared.record.Scope, prepared.record.PersonalType)
	return report, nil
}

func overwriteMirrorFromCanonical(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	record, mirrorDir, err := resolutionRecordAndMirrorDir(ctx, svc, req)
	if err != nil {
		return report, err
	}
	preview, _, err := verifyResolutionRequestPreview(svc, req, mirrorDir)
	if err != nil {
		return report, err
	}
	if err := verifyResolutionPreviewTarget(preview, mirrorDir); err != nil {
		return report, err
	}
	if _, err := backupSkillDir(mirrorDir); err != nil {
		return report, err
	}
	oldHash := skillDirContentHash(mirrorDir)
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.Name, record.Scope, record.PersonalType, oldHash, "")
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	newHash, err := replaceMirrorSkillDir(req.Target.Root, req.Name, record.Dir, req.Target.Scope)
	if err != nil {
		return report, err
	}
	if err := updateOwnedMirrorManifest(req.Target, record, newHash); err != nil {
		return partialMirrorResolutionReport(report, newHash, "retry_manifest_write"), err
	}
	report.ResultingHash = newHash
	auditRecord.NewHash = newHash
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		return partialMirrorResolutionReport(report, newHash, "retry_audit_finalize"), err
	}
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.Name, record.Scope, record.PersonalType)
	return report, nil
}

func saveMirrorAsNewCanonical(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	prepared, err := prepareCanonicalMirrorResolution(ctx, svc, req)
	if err != nil {
		return report, err
	}
	if prepared.targetDir == prepared.record.Dir {
		return report, fmt.Errorf("new skill name is required")
	}
	if err := ensureSkillDirAbsent(prepared.targetDir, req.NewName); err != nil {
		return report, err
	}
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.NewName, prepared.record.Scope, prepared.record.PersonalType, "", "")
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := replaceSkillDirFromMirror(prepared.mirrorDir, prepared.targetDir); err != nil {
		return report, err
	}
	if err := rewriteCopiedSkillName(prepared.targetDir, req.NewName); err != nil {
		return report, err
	}
	if err := setMirrorResolutionResultHash(&report, prepared.targetDir); err != nil {
		return report, err
	}
	auditRecord.NewHash = report.ResultingHash
	if err := resolveOriginalMirrorAfterSaveAsNewOrDelete(req, prepared); err != nil {
		return partialMirrorResolutionReport(report, report.ResultingHash, "retry_mirror_resolution"), err
	}
	markResolutionMirrorPublish(ctx, svc, req, prepared.record.Scope, prepared.record.PersonalType, req.NewName, &report)
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		return partialMirrorResolutionReport(report, report.ResultingHash, "retry_audit_finalize"), err
	}
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.NewName, prepared.record.Scope, prepared.record.PersonalType)
	return report, nil
}

func resolveOriginalMirrorAfterSaveAsNewOrDelete(req SkillMirrorResolutionRequest, prepared canonicalMirrorResolution) error {
	if !canonicalSkillDirExists(prepared.record.Dir) {
		return removeOriginalDeletedMirrorAfterSaveAsNew(req)
	}
	return resolveOriginalMirrorAfterSaveAsNew(req, prepared)
}

func canonicalSkillDirExists(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func removeOriginalDeletedMirrorAfterSaveAsNew(req SkillMirrorResolutionRequest) error {
	target, err := confirmDeleteMirrorTarget(req)
	if err != nil {
		return err
	}
	if _, err := backupSkillDir(target.mirrorDir); err != nil {
		return err
	}
	return removeManagedMirror(req.Target, target)
}

type canonicalMirrorResolution struct {
	record     canonicalSkillRecord
	mirrorDir  string
	targetDir  string
	mirrorHash string
}

func prepareCanonicalMirrorResolution(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (canonicalMirrorResolution, error) {
	record, mirrorDir, err := resolutionRecordAndMirrorDir(ctx, svc, req)
	if err != nil {
		return canonicalMirrorResolution{}, err
	}
	preview, mirrorHash, err := verifyResolutionRequestPreview(svc, req, mirrorDir)
	if err != nil {
		return canonicalMirrorResolution{}, err
	}
	targetDir, err := canonicalResolutionTargetDir(svc, record, req)
	if err != nil {
		return canonicalMirrorResolution{}, err
	}
	if err := verifyResolutionPreviewTarget(preview, targetDir); err != nil {
		return canonicalMirrorResolution{}, err
	}
	return canonicalMirrorResolution{
		record:     record,
		mirrorDir:  mirrorDir,
		targetDir:  targetDir,
		mirrorHash: mirrorHash,
	}, nil
}

func setMirrorResolutionResultHash(report *SkillMirrorResolutionReport, targetDir string) error {
	hash, err := stableMirrorDirectoryHash(targetDir)
	if err != nil {
		return err
	}
	report.ResultingHash = hash
	return nil
}

func ImportUnmanagedProviderSkill(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	prepared, err := prepareUnmanagedProviderImport(svc, req)
	if err != nil {
		return report, err
	}
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.Name, prepared.scope, prepared.personalType, "", "")
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := copyUnmanagedProviderImport(prepared); err != nil {
		return report, err
	}
	report.ResultingHash = skillDirContentHash(prepared.targetDir)
	auditRecord.NewHash = report.ResultingHash
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		report.PartialFailure = true
		report.FollowUpAction = "retry_audit_finalize"
		return report, err
	}
	markImportMirrorPublish(ctx, svc, req, prepared, &report)
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.Name, prepared.scope, prepared.personalType)
	return report, nil
}

type unmanagedProviderImport struct {
	sourceDir    string
	targetDir    string
	name         string
	scope        string
	personalType string
}

func prepareUnmanagedProviderImport(svc *service, req SkillMirrorResolutionRequest) (unmanagedProviderImport, error) {
	sourceDir, _, preview, err := unmanagedProviderSource(svc, req)
	if err != nil {
		return unmanagedProviderImport{}, err
	}
	targetDir, scope, personalType, err := importCanonicalTargetDir(svc, req)
	if err != nil {
		return unmanagedProviderImport{}, err
	}
	if err := verifyResolutionPreviewTarget(preview, targetDir); err != nil {
		return unmanagedProviderImport{}, err
	}
	if err := ensureSkillDirAbsent(targetDir, req.Name); err != nil {
		return unmanagedProviderImport{}, err
	}
	if _, err := backupSkillDir(targetDir); err != nil {
		return unmanagedProviderImport{}, err
	}
	return unmanagedProviderImport{sourceDir: sourceDir, targetDir: targetDir, name: req.Name, scope: scope, personalType: personalType}, nil
}

func copyUnmanagedProviderImport(prepared unmanagedProviderImport) error {
	if err := os.MkdirAll(filepath.Dir(prepared.targetDir), 0o755); err != nil {
		return err
	}
	if _, _, err := copySkillDir(prepared.sourceDir, prepared.targetDir); err != nil {
		return err
	}
	return rewriteCopiedSkillName(prepared.targetDir, prepared.name)
}

func markImportMirrorPublish(ctx context.Context, svc *service, req SkillMirrorResolutionRequest, prepared unmanagedProviderImport, report *SkillMirrorResolutionReport) {
	cwd := svc.projectRoot
	if targetCWD, err := projectRootFromMirrorTarget(req.Target); err == nil {
		cwd = targetCWD
	}
	publishReport := svc.publishWriteTimeMirrorsForScope(ctx, cwd, prepared.scope, prepared.personalType, req.Name)
	if len(publishReport.Conflicts) > 0 {
		report.PartialFailure = true
		report.FollowUpAction = "retry_mirror_publish"
	}
}

func TakeoverProviderSkill(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	prepared, err := prepareProviderSkillTakeover(ctx, svc, req)
	if err != nil {
		return report, err
	}
	if err := backupProviderSkillTakeover(prepared); err != nil {
		return report, err
	}
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.Name, req.Target.Scope, "", "", prepared.previewHash)
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	resultingHash, err := replaceCanonicalFromProviderTakeover(prepared)
	if err != nil {
		return report, err
	}
	if _, err := replaceProviderTakeoverMirror(req, prepared.record); err != nil {
		return report, err
	}
	report.ResultingHash = resultingHash
	markResolutionMirrorPublish(ctx, svc, req, prepared.record.Scope, prepared.record.PersonalType, req.Name, &report)
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		report.PartialFailure = true
		report.FollowUpAction = "retry_audit_finalize"
		return report, err
	}
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.Name, req.Target.Scope, "")
	return report, nil
}

type providerSkillTakeover struct {
	sourceDir   string
	previewHash string
	record      canonicalSkillRecord
}

func prepareProviderSkillTakeover(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (providerSkillTakeover, error) {
	sourceDir, previewHash, _, err := unmanagedProviderSource(svc, req)
	if err != nil {
		return providerSkillTakeover{}, err
	}
	if req.Target.Scope == skillScopePersonal {
		return providerSkillTakeover{}, fmt.Errorf("takeover_provider_skill is only supported for project provider mirrors")
	}
	record, err := takeoverCanonicalRecord(ctx, svc, req)
	if err != nil {
		return providerSkillTakeover{}, err
	}
	return providerSkillTakeover{sourceDir: sourceDir, previewHash: previewHash, record: record}, nil
}

func backupProviderSkillTakeover(prepared providerSkillTakeover) error {
	if _, err := backupSkillDir(prepared.sourceDir); err != nil {
		return err
	}
	_, err := backupSkillDir(prepared.record.Dir)
	return err
}

func replaceCanonicalFromProviderTakeover(prepared providerSkillTakeover) (string, error) {
	if err := replaceCanonicalSkillDirFromMirror(prepared.sourceDir, prepared.record.Dir); err != nil {
		return "", err
	}
	if err := rewriteCopiedSkillName(prepared.record.Dir, prepared.record.Name); err != nil {
		return "", err
	}
	return stableMirrorDirectoryHash(prepared.record.Dir)
}

func replaceProviderTakeoverMirror(req SkillMirrorResolutionRequest, record canonicalSkillRecord) (string, error) {
	mirrorHash, err := replaceMirrorSkillDir(req.Target.Root, req.Name, record.Dir, req.Target.Scope)
	if err != nil {
		return "", err
	}
	if err := updateOwnedMirrorManifest(req.Target, record, mirrorHash); err != nil {
		return "", err
	}
	return mirrorHash, nil
}

func markResolutionMirrorPublish(ctx context.Context, svc *service, req SkillMirrorResolutionRequest, scope, personalType, name string, report *SkillMirrorResolutionReport) {
	if svc == nil || report == nil {
		return
	}
	cwd := svc.projectRoot
	if targetCWD, err := projectRootFromMirrorTarget(req.Target); err == nil {
		cwd = targetCWD
	}
	publishReport := svc.publishWriteTimeMirrorsForScope(ctx, cwd, scope, personalType, name)
	if len(publishReport.Conflicts) > 0 {
		report.PartialFailure = true
		if report.FollowUpAction == "" {
			report.FollowUpAction = "retry_mirror_publish"
		}
	}
}

func takeoverCanonicalRecord(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (canonicalSkillRecord, error) {
	if svc == nil {
		return canonicalSkillRecord{}, fmt.Errorf("skill service is required")
	}
	projectRoot, err := projectRootFromMirrorTarget(req.Target)
	if err != nil {
		return canonicalSkillRecord{}, err
	}
	if cwd := strings.TrimSpace(cwdFromContext(ctx)); cwd != "" {
		projectRoot = cwd
	}
	records, err := newCanonicalStoreForOwner(svc.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile()).scan(projectRoot)
	if err != nil {
		return canonicalSkillRecord{}, err
	}
	for _, record := range records {
		if record.Scope == skillScopeProject && record.Name == req.Name {
			return record, nil
		}
	}
	name, err := validateSkillName(req.Name)
	if err != nil {
		return canonicalSkillRecord{}, err
	}
	dir := filepath.Join(defaultProjectSkillsRoot(projectRoot), name)
	return canonicalSkillRecord{Name: name, Scope: skillScopeProject, Dir: dir}, nil
}

func replaceCanonicalSkillDirFromMirror(sourceDir, targetDir string) error {
	return replaceSkillDirFromMirrorWithCopy(sourceDir, targetDir, copyMirrorSkillDir)
}

func copyMirrorSkillDir(source, target string) (int, int64, error) {
	files, total := 0, int64(0)
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if sameCleanPath(source, path) {
			return os.Mkdir(target, 0o755)
		}
		rel, err := safeMirrorRelativePath(source, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, filepath.FromSlash(rel))
		if entry.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := safeMirrorFileInfo(path, entry)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		files, total = files+1, total+int64(len(data))
		return os.WriteFile(dst, data, info.Mode().Perm())
	})
	return files, total, err
}
