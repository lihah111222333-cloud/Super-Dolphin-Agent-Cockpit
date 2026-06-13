package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

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
	return publishPreparedWriteTimeMirrors(ctx, report, targets, records, conflicts, scope, personalType, name)
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
	return publishPreparedWriteTimeMirrors(ctx, report, targets, records, conflicts, scope, personalType, name)
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
	return publishPreparedWriteTimeMirrors(ctx, report, targets, records, conflicts, "", "", name)
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

func publishPreparedWriteTimeMirrors(ctx context.Context, report SkillMirrorReport, targets []SkillMirrorTarget, records []canonicalSkillRecord, conflicts []canonicalSkillConflict, scope, personalType, name string) SkillMirrorReport {
	if len(conflicts) > 0 {
		appendCanonicalConflictReportItems(&report, targets, conflicts)
	}
	publishReport, err := PublishSkillMirrors(ctx, records, targets)
	appendSkillMirrorReport(&report, publishReport)
	if err != nil {
		appendSkillMirrorReport(&report, mirrorPublishErrorReport(targets, scope, personalType, name, err))
	}
	return report
}

// cleanupProjectSuppressedPersonalMirrors 处理cleanup项目suppressedpersonalmirrors。
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
	for _, target := range personalTargets {
		targetReport, err := cleanupSuppressedPersonalMirrorTarget(target, suppressed)
		appendSkillMirrorReport(&report, targetReport)
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

// projectSuppressedPersonalRecords 处理项目suppressedpersonal记录。
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

// projectSuppressedPersonalSourceIDs 处理项目suppressedpersonalsourceids。
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

func cleanupSuppressedPersonalMirrorTarget(target SkillMirrorTarget, records []canonicalSkillRecord) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	unlock := lockSkillMirrorRoot(target.Root)
	defer unlock()
	for _, record := range records {
		item, deleted, err := cleanupSuppressedPersonalMirrorRecord(target, record)
		if err != nil {
			return report, err
		}
		if deleted {
			report.Deleted = append(report.Deleted, item)
		}
	}
	return report, nil
}

// cleanupSuppressedPersonalMirrorRecord 处理cleanupsuppressedpersonal镜像记录。
func cleanupSuppressedPersonalMirrorRecord(target SkillMirrorTarget, record canonicalSkillRecord) (SkillMirrorReportItem, bool, error) {
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
