package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/mirrorpath"
)

// ResolveExternalPersonalProjectSameName 处理外部 personal mirror 和项目 skill 同名。
// 这些动作必须由用户选择，不能被日常刷新自动触发。
func ResolveExternalPersonalProjectSameName(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	req.Action = normalizeResolutionAction(req.Action)
	switch req.Action {
	case ResolutionUseProjectSharedSkill:
		return useProjectSharedForExternalPersonal(ctx, svc, req)
	case ResolutionUseExternalProviderSkill:
		return useExternalForExternalPersonalProject(ctx, svc, req)
	case ResolutionSaveAsNewPersonal:
		return saveExternalPersonalProjectSameNameAsPersonal(ctx, svc, req)
	default:
		return SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}, fmt.Errorf("unsupported external personal project resolution action %q", req.Action)
	}
}

// useProjectSharedForExternalPersonal 保留项目 skill。
// 只清理 preview 指向的同名外部 mirror 副本。
func useProjectSharedForExternalPersonal(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	record, err := externalPersonalProjectCanonicalRecord(ctx, svc, req)
	if err != nil {
		return report, err
	}
	report.Name = record.Name
	sourceDir, _, preview, err := externalPersonalProjectSource(svc, req)
	if err != nil {
		return report, err
	}
	if err := validateExternalPersonalUseProjectPreview(preview, record.Dir, sourceDir); err != nil {
		return report, err
	}
	if err := mirrorpath.ForExistingSkillDirs([]string{providerPersonalMirrorRoot(SkillProviderClaude), providerPersonalMirrorRoot(SkillProviderCodex)}, record.Name, sourceDir, func(dir string) error {
		return removeSameNameDuplicateSource(skillResolutionSource{Scope: skillScopePersonal, Path: filepath.ToSlash(dir)})
	}); err != nil {
		return report, err
	}
	report.ResultingHash = preview.SourceHash
	if report.ResultingHash == "" {
		report.ResultingHash, err = stableMirrorDirectoryHash(record.Dir)
		if err != nil {
			return report, err
		}
	}
	markExternalPersonalProjectMirrorPublish(ctx, svc, skillScopeProject, "", record.Name, &report)
	svc.publishSkillsChanged(ctx, req.Action, record.Name, skillScopeProject)
	return report, nil
}

// useExternalForExternalPersonalProject 把外部 mirror 提升为项目 skill。
// 替换前要确认 preview 仍指向当前项目目录。
func useExternalForExternalPersonalProject(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	record, err := externalPersonalProjectCanonicalRecord(ctx, svc, req)
	if err != nil {
		return report, err
	}
	report.Name = record.Name
	sourceDir, _, preview, err := externalPersonalProjectSource(svc, req)
	if err != nil {
		return report, err
	}
	if !sameResolutionPath(preview.SourcePath, sourceDir) {
		return report, fmt.Errorf("skill resolution preview source mismatch")
	}
	if err := verifyResolutionPreviewTarget(preview, record.Dir); err != nil {
		return report, err
	}
	hash, err := replaceProjectSkillWithExternalPersonal(sourceDir, record.Dir)
	if err != nil {
		return report, err
	}
	report.ResultingHash = hash
	markExternalPersonalProjectMirrorPublish(ctx, svc, skillScopeProject, "", record.Name, &report)
	svc.publishSkillsChanged(ctx, req.Action, record.Name, skillScopeProject)
	return report, nil
}

func replaceProjectSkillWithExternalPersonal(sourceDir, targetDir string) (string, error) {
	tempDir, err := prepareProjectReplacementDirs(sourceDir, targetDir)
	if err != nil {
		return "", err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := moveExternalPersonalIntoProject(sourceDir, targetDir, tempDir); err != nil {
		return "", err
	}
	removeTemp = false
	return skillDirContentHash(targetDir)
}

// prepareProjectReplacementDirs 准备替换项目 skill 所需的临时目录。
func prepareProjectReplacementDirs(sourceDir, targetDir string) (string, error) {
	if err := ensureProviderSkillDirSafe(sourceDir); err != nil {
		return "", err
	}
	if err := ensureProviderSkillDirSafe(targetDir); err != nil {
		return "", err
	}
	if _, err := backupSkillDir(targetDir); err != nil {
		return "", err
	}
	if _, err := backupSkillDir(sourceDir); err != nil {
		return "", err
	}
	parent := filepath.Dir(targetDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tempDir, err := os.MkdirTemp(parent, "."+filepath.Base(targetDir)+".replace-*")
	if err != nil {
		return "", err
	}
	if err := os.Remove(tempDir); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", err
	}
	return tempDir, nil
}

func moveExternalPersonalIntoProject(sourceDir, targetDir, tempDir string) error {
	if err := os.Rename(sourceDir, tempDir); err != nil {
		return err
	}
	if err := os.RemoveAll(targetDir); err != nil {
		_ = os.Rename(tempDir, sourceDir)
		return err
	}
	if err := os.Rename(tempDir, targetDir); err != nil {
		_ = os.Rename(tempDir, sourceDir)
		return err
	}
	return nil
}

// saveExternalPersonalProjectSameNameAsPersonal 把外部同名 skill 另存到 personal/imported。
// 保存成功后才删除原 mirror；删除失败要告诉前端后续重试。
func saveExternalPersonalProjectSameNameAsPersonal(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	sourceDir, _, preview, err := externalPersonalProjectSource(svc, req)
	if err != nil {
		return report, err
	}
	name, err := validateSkillName(req.NewName)
	if err != nil {
		return report, fmt.Errorf("new skill name is required: %w", err)
	}
	targetDir := filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeImported, name)
	if err := verifyResolutionPreviewTarget(preview, targetDir); err != nil {
		return report, err
	}
	if err := ensureSkillDirAbsent(targetDir, name); err != nil {
		return report, err
	}
	if _, err := backupSkillDir(sourceDir); err != nil {
		return report, err
	}
	if err := replaceSkillDirFromMirror(sourceDir, targetDir); err != nil {
		return report, err
	}
	if err := rewriteCopiedSkillIdentity(targetDir, name, ""); err != nil {
		return report, err
	}
	if err := os.RemoveAll(sourceDir); err != nil {
		return partialExternalPersonalCleanupFailure(report, targetDir, err)
	}
	if err := setMirrorResolutionResultHash(&report, targetDir); err != nil {
		return report, err
	}
	markExternalPersonalProjectMirrorPublish(ctx, svc, skillScopePersonal, personalSkillTypeImported, name, &report)
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, name, skillScopePersonal, personalSkillTypeImported)
	return report, nil
}

func partialExternalPersonalCleanupFailure(report SkillMirrorResolutionReport, targetDir string, cleanupErr error) (SkillMirrorResolutionReport, error) {
	targetHash, err := skillDirContentHash(targetDir)
	if err != nil {
		return report, fmt.Errorf("hash external provider target after cleanup failure: %w", err)
	}
	return partialMirrorResolutionReport(report, targetHash, "retry_external_provider_cleanup"), cleanupErr
}

func externalPersonalProjectSource(svc *service, req SkillMirrorResolutionRequest) (string, string, skillResolutionPreviewItem, error) {
	if req.Target.Scope != skillScopePersonal {
		return "", "", skillResolutionPreviewItem{}, fmt.Errorf("personal provider mirror target is required")
	}
	return unmanagedProviderSource(svc, req)
}

// externalPersonalProjectCanonicalRecord 为外部个人项目 skill 生成 canonical 记录。
func externalPersonalProjectCanonicalRecord(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (canonicalSkillRecord, error) {
	if svc == nil {
		return canonicalSkillRecord{}, fmt.Errorf("skill service is required")
	}
	cwd := externalPersonalProjectCWD(ctx, svc)
	records, err := newCanonicalStore(svc.resolvedSuperDolphinHome()).scan(cwd)
	if err != nil {
		return canonicalSkillRecord{}, err
	}
	name, _, err := normalizeSkillIdentityName(req.Name, "")
	if err != nil {
		return canonicalSkillRecord{}, err
	}
	for _, record := range records {
		if record.Scope == skillScopeProject && record.Name == name {
			return record, nil
		}
	}
	return canonicalSkillRecord{}, fmt.Errorf("project shared skill not found: %s", name)
}

func externalPersonalProjectCWD(ctx context.Context, svc *service) string {
	if cwd := strings.TrimSpace(cwdFromContext(ctx)); cwd != "" {
		return cwd
	}
	if svc != nil {
		return svc.projectRoot
	}
	return ""
}

func validateExternalPersonalUseProjectPreview(preview skillResolutionPreviewItem, projectDir, sourceDir string) error {
	if !sameResolutionPath(preview.SourcePath, projectDir) {
		return fmt.Errorf("skill resolution preview source mismatch")
	}
	if err := verifyResolutionPreviewTarget(preview, sourceDir); err != nil {
		return err
	}
	return nil
}

func markExternalPersonalProjectMirrorPublish(ctx context.Context, svc *service, _, _ string, name string, report *SkillMirrorResolutionReport) {
	if svc == nil || report == nil {
		return
	}
	cwd := externalPersonalProjectCWD(ctx, svc)
	publishReport := svc.publishWriteTimeMirrorsForEffectiveSet(ctx, cwd, name)
	if len(publishReport.Conflicts) > 0 {
		report.PartialFailure = true
		if report.FollowUpAction == "" {
			report.FollowUpAction = "retry_mirror_publish"
		}
	}
}
