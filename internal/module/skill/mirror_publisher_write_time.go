package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errProjectPolicyCannotMutateSharedPersonalMirror = errors.New("project skill policy cannot suppress personal skills through a shared provider mirror")

func (s *service) publishWriteTimeMirrors(ctx context.Context, cwd, scope, personalType, name string) SkillMirrorReport {
	targets := s.writeTimeMirrorTargets(cwd, scope)
	if len(targets) == 0 {
		return unconfiguredMirrorPublishReport(scope, personalType, name)
	}
	store := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	report, ok := s.prepareWriteTimeMirrorPublish(cwd, targets, store, scope, personalType, name)
	if !ok {
		return report
	}
	records, conflicts, err := writeTimeMirrorRecords(ctx, store, cwd, scope)
	if err != nil {
		return mirrorPublishErrorReport(targets, scope, personalType, name, err)
	}
	return publishPreparedWriteTimeMirrors(s.mirrorLocks, ctx, report, targets, records, conflicts, scope, personalType, name)
}

func (s *service) ensureWriteTimeMirrorPublishAllowed(ctx context.Context, cwd, scope, personalType, name string) error {
	targets := s.writeTimeMirrorTargets(cwd, scope)
	if len(targets) == 0 {
		return mirrorPublishBlockingError(unconfiguredMirrorPublishReport(scope, personalType, name))
	}
	store := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	records, canonicalConflicts, err := writeTimeMirrorRecords(ctx, store, cwd, scope)
	if err != nil {
		return mirrorPublishBlockingError(mirrorPublishErrorReport(targets, scope, personalType, name, err))
	}
	var report SkillMirrorReport
	if len(canonicalConflicts) > 0 {
		appendCanonicalConflictReportItems(&report, targets, canonicalConflicts)
	}
	mirrorConflicts, err := DetectSkillMirrorConflicts(records, targets)
	if err != nil {
		return mirrorPublishBlockingError(mirrorPublishErrorReport(targets, scope, personalType, name, err))
	}
	appendSkillMirrorConflictReportItems(&report, mirrorConflicts)
	return mirrorPublishBlockingError(report)
}

func (s *service) publishWriteTimeMirrorsBlocking(ctx context.Context, cwd, scope, personalType, name string) (SkillMirrorReport, error) {
	report := s.publishWriteTimeMirrors(ctx, cwd, scope, personalType, name)
	return report, mirrorPublishBlockingError(report)
}

func appendSkillMirrorConflictReportItems(report *SkillMirrorReport, conflicts []SkillMirrorConflict) {
	if report == nil || len(conflicts) == 0 {
		return
	}
	for _, conflict := range conflicts {
		report.Conflicts = append(report.Conflicts, SkillMirrorReportItem{
			TargetID:           conflict.TargetID,
			Provider:           conflict.Provider,
			Scope:              conflict.Scope,
			RelativeMirrorPath: skillSlug(conflict.Name),
			CanonicalID:        conflict.CanonicalID,
			OldHash:            conflict.MirrorHash,
			NewHash:            conflict.CanonicalHash,
			ConflictKind:       conflict.Kind,
			Error:              fmt.Sprintf("provider mirror conflict %q must be resolved before canonical skill mutation", conflict.Kind),
		})
	}
}

func (s *service) publishWriteTimeMirrorsForScope(ctx context.Context, cwd, scope, personalType, name string) SkillMirrorReport {
	targets := s.writeTimeMirrorTargets(cwd, scope)
	if len(targets) == 0 {
		return unconfiguredMirrorPublishReport(scope, personalType, name)
	}
	store := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	report, ok := s.prepareWriteTimeMirrorPublish(cwd, targets, store, scope, personalType, name)
	if !ok {
		return report
	}
	records, conflicts, err := writeTimeMirrorRecordsForScope(ctx, store, cwd, scope)
	if err != nil {
		return mirrorPublishErrorReport(targets, scope, personalType, name, err)
	}
	return publishPreparedWriteTimeMirrors(s.mirrorLocks, ctx, report, targets, records, conflicts, scope, personalType, name)
}

func (s *service) publishWriteTimeMirrorsForEffectiveSet(ctx context.Context, cwd, name string) SkillMirrorReport {
	targets := uniqueMirrorTargets(append(s.writeTimeMirrorTargets(cwd, skillScopeProject), s.writeTimeMirrorTargets(cwd, skillScopePersonal)...))
	if len(targets) == 0 {
		return unconfiguredMirrorPublishReport("", "", name)
	}
	store := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	report, ok := s.prepareWriteTimeMirrorPublish(cwd, targets, store, "", "", name)
	if !ok {
		return report
	}
	records, conflicts, err := store.EffectiveSet(ctx, cwd)
	if err != nil {
		return mirrorPublishErrorReport(targets, "", "", name, err)
	}
	return publishPreparedWriteTimeMirrors(s.mirrorLocks, ctx, report, targets, records, conflicts, "", "", name)
}

func (s *service) prepareWriteTimeMirrorPublish(cwd string, targets []SkillMirrorTarget, store *canonicalStore, scope, personalType, name string) (SkillMirrorReport, bool) {
	var report SkillMirrorReport
	cleanupReport, err := s.cleanupProjectSuppressedPersonalMirrors(cwd, targets, store)
	appendSkillMirrorReport(&report, cleanupReport)
	if err != nil {
		appendSkillMirrorReport(&report, mirrorPublishErrorReport(targets, scope, personalType, name, err))
		return report, false
	}
	return report, true
}

func writeTimeMirrorRecords(ctx context.Context, store *canonicalStore, cwd, scope string) ([]canonicalSkillRecord, []canonicalSkillConflict, error) {
	if scope == skillScopePersonal {
		return mirrorRecordsForScope(ctx, store, cwd, skillScopePersonal)
	}
	return store.EffectiveSet(ctx, cwd)
}

func writeTimeMirrorRecordsForScope(ctx context.Context, store *canonicalStore, cwd, scope string) ([]canonicalSkillRecord, []canonicalSkillConflict, error) {
	if scope == skillScopePersonal {
		return mirrorRecordsForScope(ctx, store, cwd, skillScopePersonal)
	}
	records, err := store.scan(cwd)
	if err != nil {
		return nil, nil, err
	}
	records, err = store.applyEffectivePolicies(cwd, records)
	if err != nil {
		return nil, nil, err
	}
	records = filterCanonicalRecordsForScope(records, scope)
	conflicts := canonicalSameNameConflicts(records)
	if len(conflicts) > 0 {
		records = canonicalRecordsWithoutConflicts(records, conflicts)
	}
	return records, conflicts, nil
}

func publishPreparedWriteTimeMirrors(locks *MirrorRootLockRegistry, ctx context.Context, report SkillMirrorReport, targets []SkillMirrorTarget, records []canonicalSkillRecord, conflicts []canonicalSkillConflict, scope, personalType, name string) SkillMirrorReport {
	if len(conflicts) > 0 {
		appendCanonicalConflictReportItems(&report, targets, conflicts)
	}
	publishReport, err := PublishSkillMirrors(locks, ctx, records, targets)
	appendSkillMirrorReport(&report, publishReport)
	if err != nil {
		appendSkillMirrorReport(&report, mirrorPublishErrorReport(targets, scope, personalType, name, err))
	}
	return report
}

// cleanupSuppressedPersonalMirrorRecord 删除仍由系统托管的被压制 personal mirror。
// mirror hash 或内容 hash 已漂移时跳过删除，让冲突检测路径交给用户确认。
func cleanupSuppressedPersonalMirrorRecord(locks *MirrorRootLockRegistry, target SkillMirrorTarget, record canonicalSkillRecord) (SkillMirrorReportItem, bool, error) {
	unlock := locks.lock(target.Root)
	defer unlock()
	if targetUsesCanonicalSelfMirror([]canonicalSkillRecord{record}, target) {
		return SkillMirrorReportItem{}, false, nil
	}
	mirrorDir := filepath.Join(target.Root, record.Name)
	mirrorHash, exists, err := existingMirrorHash(mirrorDir)
	if err != nil || !exists {
		return SkillMirrorReportItem{}, false, err
	}
	expectedHash, expectedContentHash, err := expectedCanonicalMirrorHashes(target.Root, record, target.Scope)
	if err != nil {
		return SkillMirrorReportItem{}, false, err
	}
	mirrorContentHash, err := skillDirContentHash(mirrorDir)
	if err != nil {
		return SkillMirrorReportItem{}, false, err
	}
	if mirrorHash != expectedHash && mirrorContentHash != expectedContentHash {
		return SkillMirrorReportItem{}, false, nil
	}
	item := reportItem(target, record.Name, canonicalSourceID(record), mirrorHash, "", "")
	return item, true, os.RemoveAll(mirrorDir)
}

// cleanupProjectSuppressedPersonalMirrors 处理项目策略压制的 personal mirror。
// personal mirror 没有 project/session 维度；若继续删除会污染其他项目和恢复会话。
func (s *service) cleanupProjectSuppressedPersonalMirrors(cwd string, targets []SkillMirrorTarget, store *canonicalStore) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	personalTargets := mirrorTargetsForScope(targets, skillScopePersonal)
	if len(personalTargets) == 0 {
		return report, nil
	}
	records, err := store.scan(cwd)
	if err != nil {
		return report, err
	}
	suppressed, err := projectSuppressedPersonalRecords(cwd, records)
	if err != nil || len(suppressed) == 0 {
		return report, err
	}
	return report, errProjectPolicyCannotMutateSharedPersonalMirror
}

// projectSuppressedPersonalRecords 根据项目策略找出不应再发布到 provider 的 personal skill。
// 策略解析失败直接返回错误，调用方会停止本次 mirror 发布。
func projectSuppressedPersonalRecords(cwd string, records []canonicalSkillRecord) ([]canonicalSkillRecord, error) {
	policy, err := readProjectSkillPolicy(cwd)
	if err != nil {
		return nil, err
	}
	suppressed, err := projectSuppressedPersonalSourceIDs(policy)
	if err != nil || len(suppressed) == 0 {
		return nil, err
	}
	out := make([]canonicalSkillRecord, 0, len(suppressed))
	for _, record := range records {
		if record.Scope != skillScopePersonal {
			continue
		}
		if _, ok := suppressed[canonicalSourceID(record)]; ok {
			out = append(out, record)
		}
	}
	return out, nil
}

// projectSuppressedPersonalSourceIDs 计算项目策略压制的 personal canonical ID 集合。
// keep-selected 指向 project 时会压制同名所有 active personal 来源。
func projectSuppressedPersonalSourceIDs(policy projectSkillPolicy) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for _, item := range policy.DisablePersonalForProject {
		name, _, err := normalizeSkillIdentityName(item.Name, "")
		if err != nil {
			return nil, err
		}
		_, personalType, err := normalizeSkillTarget(skillScopePersonal, item.PersonalType)
		if err != nil {
			return nil, err
		}
		out[canonicalSourceID(canonicalSkillRecord{Name: name, Scope: skillScopePersonal, PersonalType: personalType})] = struct{}{}
	}
	for _, selection := range policy.KeepSelected {
		name, _, err := normalizeSkillIdentityName(selection.Name, "")
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(strings.TrimSpace(selection.SelectedSourceID), skillScopeProject+"/") {
			for _, personalType := range activePersonalSkillTypes() {
				out[canonicalSourceID(canonicalSkillRecord{Name: name, Scope: skillScopePersonal, PersonalType: personalType})] = struct{}{}
			}
			continue
		}
		for _, sourceID := range selection.ExcludedSourceIDs {
			if strings.HasPrefix(strings.TrimSpace(sourceID), skillScopePersonal+"/") {
				out[strings.TrimSpace(sourceID)] = struct{}{}
			}
		}
	}
	return out, nil
}
