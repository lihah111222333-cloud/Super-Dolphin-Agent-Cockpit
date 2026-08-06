package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/mirrorpath"
)

// replaceProviderRootSymlink 把 provider mirror 根目录从 symlink 接管成真实目录。
// 只有 preview 仍匹配当前 symlink 时才能做，避免写到外部路径。
func replaceProviderRootSymlink(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	if svc == nil {
		return report, fmt.Errorf("skill service is required")
	}
	if err := validateSkillMirrorTarget(req.Target); err != nil {
		return report, err
	}
	if err := mirrorpath.RejectSymlinkAncestors(filepath.Dir(req.Target.Root)); err != nil {
		return report, err
	}
	linkTarget, previewHash, err := verifyProviderRootSymlinkPreview(svc, req)
	if err != nil {
		return report, err
	}
	auditRecord := newProviderRootSymlinkAuditRecord(req, previewHash, "")
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := replaceSymlinkWithMirrorRoot(req.Target.Root, linkTarget); err != nil {
		return partialMirrorResolutionReport(report, "", "retry_provider_root_takeover"), err
	}
	newHash, err := publishProviderRootAfterTakeover(ctx, svc, req.Target)
	if err != nil {
		return partialMirrorResolutionReport(report, "", "retry_mirror_publish"), err
	}
	report.ResultingHash = newHash
	auditRecord.NewHash = newHash
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		return partialMirrorResolutionReport(report, newHash, "retry_audit_finalize"), err
	}
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.Name, req.Target.Scope, "")
	return report, nil
}

// verifyProviderRootSymlinkPreview 确认 UI 看到的 symlink 还是当前 symlink。
// 不匹配就报错，不重新读取后继续。
func verifyProviderRootSymlinkPreview(svc *service, req SkillMirrorResolutionRequest) (string, string, error) {
	preview, err := svc.lookupResolutionPreview(req.PreviewID, req.Action, req.PreviewHash)
	if err != nil {
		return "", "", err
	}
	if !sameResolutionPath(preview.SourcePath, req.Target.Root) || !sameResolutionPath(preview.TargetPath, req.Target.Root) {
		return "", "", fmt.Errorf("skill resolution preview root path mismatch")
	}
	hash, err := mirrorRootSymlinkHash(req.Target.Root)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(preview.SourceHash) == "" || preview.SourceHash != hash {
		return "", "", fmt.Errorf("skill resolution preview source hash mismatch")
	}
	linkTarget, err := os.Readlink(req.Target.Root)
	if err != nil {
		return "", "", err
	}
	return linkTarget, hash, nil
}

// replaceSymlinkWithMirrorRoot 只把 symlink 换成本系统可管理的空目录。
// 创建失败时恢复原 symlink。
func replaceSymlinkWithMirrorRoot(root, linkTarget string) error {
	if err := os.Remove(root); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		_ = os.Symlink(linkTarget, root)
		return err
	}
	return nil
}

func publishProviderRootAfterTakeover(ctx context.Context, svc *service, target SkillMirrorTarget) (string, error) {
	records, err := providerRootTakeoverRecords(svc, target)
	if err != nil {
		return "", err
	}
	if _, err := PublishSkillMirrors(svc.mirrorLocks, ctx, records, []SkillMirrorTarget{target}); err != nil {
		return "", err
	}
	return stableMirrorDirectoryHash(target.Root)
}

func providerRootTakeoverRecords(svc *service, target SkillMirrorTarget) ([]canonicalSkillRecord, error) {
	cwd := svc.projectRoot
	if target.Scope == skillScopeProject {
		if projectRoot, err := projectRootFromMirrorTarget(target); err == nil {
			cwd = projectRoot
		}
	}
	store := newCanonicalStoreForOwner(svc.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	records, err := store.scan(cwd)
	if err != nil {
		return nil, err
	}
	records, err = store.applyEffectivePolicies(cwd, records)
	if err != nil {
		return nil, err
	}
	return filterCanonicalRecordsForScope(records, target.Scope), nil
}

func newProviderRootSymlinkAuditRecord(req SkillMirrorResolutionRequest, oldHash, newHash string) skillMirrorMutationAuditRecord {
	record := newMirrorMutationAuditRecord(req.Action, req.Name, req.Target.Scope, "", oldHash, newHash)
	record.CanonicalID = "provider-root/" + req.Target.TargetID
	return record
}
