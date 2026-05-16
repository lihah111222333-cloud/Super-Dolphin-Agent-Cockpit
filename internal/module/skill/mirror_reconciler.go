package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	skillConflictSameName                  = "same_name"
	skillConflictMirrorDrift               = "mirror_drift"
	skillConflictUnmanagedSameName         = "unmanaged_same_name"
	skillConflictCanonicalDeletedWithDrift = "canonical_deleted_with_drift"
	skillConflictMultiMirrorDrift          = "multi_mirror_drift"
)

type SkillMirrorConflict struct {
	Kind          string
	TargetID      string
	Provider      SkillProvider
	Scope         string
	PersonalType  string
	Name          string
	CanonicalID   string
	MirrorPath    string
	CanonicalHash string
	MirrorHash    string
	PreviewHash   string
	Sources       []SkillMirrorConflictSource
	Actions       []SkillMirrorResolutionAction
}

type SkillMirrorConflictSource struct {
	Scope         string
	PersonalType  string
	CanonicalID   string
	ContentHash   string
	CanonicalHash string
}

type SkillMirrorResolutionAction struct {
	Action      string
	PreviewHash string
}

type SkillMirrorResolutionRequest struct {
	Action      string
	Name        string
	NewName     string
	Target      SkillMirrorTarget
	PreviewHash string
}

type SkillMirrorResolutionReport struct {
	Action         string
	Name           string
	ResultingHash  string
	PartialFailure bool
	FollowUpAction string
}

type skillMirrorMutationAuditRecord struct {
	Action       string `json:"action"`
	CanonicalID  string `json:"canonical_id"`
	Scope        string `json:"scope"`
	PersonalType string `json:"personal_type,omitempty"`
	Name         string `json:"name"`
	OldHash      string `json:"old_hash,omitempty"`
	NewHash      string `json:"new_hash,omitempty"`
	Actor        string `json:"actor"`
	Timestamp    string `json:"timestamp"`
}

func DetectSkillMirrorConflicts(records []canonicalSkillRecord, targets []SkillMirrorTarget) ([]SkillMirrorConflict, error) {
	var conflicts []SkillMirrorConflict
	driftCount := map[string]int{}
	recordsByScopeName := canonicalRecordsByScopeName(records)
	for _, target := range targets {
		targetConflicts, err := detectSkillMirrorTargetConflicts(recordsByScopeName, target)
		if err != nil {
			return conflicts, err
		}
		for _, conflict := range targetConflicts {
			if conflict.Kind == skillConflictMirrorDrift {
				driftCount[conflict.CanonicalID]++
			}
			conflicts = append(conflicts, conflict)
		}
	}
	for i := range conflicts {
		if conflicts[i].Kind == skillConflictMirrorDrift && driftCount[conflicts[i].CanonicalID] > 1 {
			conflicts[i].Kind = skillConflictMultiMirrorDrift
		}
	}
	sortMirrorConflicts(conflicts)
	return conflicts, nil
}

func MirrorConflictsFromCanonical(conflicts []canonicalSkillConflict) []SkillMirrorConflict {
	out := make([]SkillMirrorConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		item := SkillMirrorConflict{Kind: skillConflictSameName, Name: conflict.Name}
		for _, source := range conflict.Sources {
			item.Sources = append(item.Sources, SkillMirrorConflictSource{
				Scope:        source.Scope,
				PersonalType: source.PersonalType,
				CanonicalID:  canonicalSourceID(canonicalSkillRecord{Name: source.Name, Scope: source.Scope, PersonalType: source.PersonalType}),
				ContentHash:  source.ContentHash,
			})
		}
		out = append(out, item)
	}
	return out
}

func ResolveSkillMirrorDrift(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	switch req.Action {
	case "sync_back_to_canonical", "sync_back_to_personal":
		return syncBackMirrorToCanonical(ctx, svc, req)
	case "canonical_overwrite_mirror", "personal_overwrite_mirror":
		return overwriteMirrorFromCanonical(ctx, svc, req)
	case "save_as_new_skill", "save_as_new_personal_skill":
		return saveMirrorAsNewCanonical(ctx, svc, req)
	default:
		return SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}, fmt.Errorf("unsupported mirror resolution action %q", req.Action)
	}
}

func syncBackMirrorToCanonical(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	record, mirrorDir, err := resolutionRecordAndMirrorDir(ctx, svc, req)
	if err != nil {
		return report, err
	}
	mirrorHash, err := verifyResolutionPreview(mirrorDir, req.PreviewHash)
	if err != nil {
		return report, err
	}
	targetDir, err := canonicalResolutionTargetDir(svc, record, req)
	if err != nil {
		return report, err
	}
	if _, err := backupSkillDir(targetDir); err != nil {
		return report, err
	}
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.Name, record.Scope, record.PersonalType, skillDirContentHash(targetDir), "")
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := replaceSkillDirFromMirror(mirrorDir, targetDir); err != nil {
		return report, err
	}
	report.ResultingHash = skillDirContentHash(targetDir)
	auditRecord.NewHash = report.ResultingHash
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		report.PartialFailure = true
		report.FollowUpAction = "retry_audit_finalize"
		return report, err
	}
	_ = mirrorHash
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.Name, record.Scope, record.PersonalType)
	return report, nil
}

func overwriteMirrorFromCanonical(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	record, mirrorDir, err := resolutionRecordAndMirrorDir(ctx, svc, req)
	if err != nil {
		return report, err
	}
	if _, err := verifyResolutionPreview(mirrorDir, req.PreviewHash); err != nil {
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
	newHash, err := replaceMirrorSkillDir(req.Target.Root, req.Name, record.Dir)
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
	record, mirrorDir, err := resolutionRecordAndMirrorDir(ctx, svc, req)
	if err != nil {
		return report, err
	}
	if _, err := verifyResolutionPreview(mirrorDir, req.PreviewHash); err != nil {
		return report, err
	}
	targetDir, err := canonicalResolutionTargetDir(svc, record, req)
	if err != nil {
		return report, err
	}
	if targetDir == record.Dir {
		return report, fmt.Errorf("new skill name is required")
	}
	if err := ensureSkillDirAbsent(targetDir, req.NewName); err != nil {
		return report, err
	}
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.NewName, record.Scope, record.PersonalType, "", "")
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := replaceSkillDirFromMirror(mirrorDir, targetDir); err != nil {
		return report, err
	}
	report.ResultingHash = skillDirContentHash(targetDir)
	auditRecord.NewHash = report.ResultingHash
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		return partialMirrorResolutionReport(report, report.ResultingHash, "retry_audit_finalize"), err
	}
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.NewName, record.Scope, record.PersonalType)
	return report, nil
}

func ImportUnmanagedProviderSkill(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	sourceDir, previewHash, err := unmanagedProviderSource(req)
	if err != nil {
		return report, err
	}
	targetDir, scope, personalType, err := importCanonicalTargetDir(svc, req)
	if err != nil {
		return report, err
	}
	if err := ensureSkillDirAbsent(targetDir, req.Name); err != nil {
		return report, err
	}
	if _, err := backupSkillDir(targetDir); err != nil {
		return report, err
	}
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.Name, scope, personalType, "", "")
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return report, err
	}
	if _, _, err := copySkillDir(sourceDir, targetDir); err != nil {
		return report, err
	}
	report.ResultingHash = skillDirContentHash(targetDir)
	auditRecord.NewHash = report.ResultingHash
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		report.PartialFailure = true
		report.FollowUpAction = "retry_audit_finalize"
		return report, err
	}
	_ = previewHash
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.Name, scope, personalType)
	return report, nil
}

func TakeoverProviderSkill(ctx context.Context, svc *service, req SkillMirrorResolutionRequest) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: req.Action, Name: req.Name}
	sourceDir, previewHash, err := unmanagedProviderSource(req)
	if err != nil {
		return report, err
	}
	if _, err := backupSkillDir(sourceDir); err != nil {
		return report, err
	}
	auditRecord := newMirrorMutationAuditRecord(req.Action, req.Name, req.Target.Scope, "", "", previewHash)
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_intent", auditRecord); err != nil {
		return report, err
	}
	if err := writeTakeoverManifest(req.Target, req.Name, previewHash); err != nil {
		return report, err
	}
	report.ResultingHash = previewHash
	if err := svc.writeSkillMutationAudit(ctx, req.Action+"_finalize", auditRecord); err != nil {
		report.PartialFailure = true
		report.FollowUpAction = "retry_audit_finalize"
		return report, err
	}
	svc.publishSkillsChangedForPersonalType(ctx, req.Action, req.Name, req.Target.Scope, "")
	return report, nil
}

func detectSkillMirrorTargetConflicts(records map[string]canonicalSkillRecord, target SkillMirrorTarget) ([]SkillMirrorConflict, error) {
	if err := validateExistingMirrorRoot(target); err != nil {
		return nil, err
	}
	manifest, err := readTargetManifest(target)
	if err != nil {
		return nil, err
	}
	names, err := skillMirrorNames(target.Root)
	if err != nil {
		return nil, err
	}
	var conflicts []SkillMirrorConflict
	for _, name := range names {
		conflict, ok, err := detectSkillMirrorNameConflict(records, target, manifest, name)
		if err != nil {
			return conflicts, err
		}
		if ok {
			conflicts = append(conflicts, conflict)
		}
	}
	return conflicts, nil
}

func detectSkillMirrorNameConflict(records map[string]canonicalSkillRecord, target SkillMirrorTarget, manifest SkillMirrorManifest, name string) (SkillMirrorConflict, bool, error) {
	mirrorDir := filepath.Join(target.Root, name)
	mirrorHash, exists, err := existingMirrorHash(mirrorDir)
	if err != nil || !exists {
		return SkillMirrorConflict{}, false, err
	}
	entry, managed := manifest.Skills[name]
	record, canonicalExists := records[mirrorRecordKey(target.Scope, name)]
	if !managed {
		return unmanagedSameNameConflict(target, record, name, mirrorDir, mirrorHash, canonicalExists), canonicalExists, nil
	}
	conflict := managedMirrorConflict(target, entry, record, name, mirrorDir, mirrorHash, canonicalExists)
	return conflict, conflict.Kind != "", nil
}

func unmanagedSameNameConflict(target SkillMirrorTarget, record canonicalSkillRecord, name, mirrorDir, mirrorHash string, canonicalExists bool) SkillMirrorConflict {
	canonicalID := ""
	if canonicalExists {
		canonicalID = canonicalSourceID(record)
	}
	return SkillMirrorConflict{
		Kind:        skillConflictUnmanagedSameName,
		TargetID:    target.TargetID,
		Provider:    target.Provider,
		Scope:       target.Scope,
		Name:        name,
		CanonicalID: canonicalID,
		MirrorPath:  filepath.ToSlash(mirrorDir),
		MirrorHash:  mirrorHash,
		PreviewHash: mirrorHash,
		Actions:     mirrorActions("view_unmanaged", "import_to_personal_imported", "import_to_project", "takeover_provider_skill"),
	}
}

func managedMirrorConflict(target SkillMirrorTarget, entry SkillMirrorEntry, record canonicalSkillRecord, name, mirrorDir, mirrorHash string, canonicalExists bool) SkillMirrorConflict {
	if canonicalExists && mirrorHash == entry.MirrorHash {
		return SkillMirrorConflict{}
	}
	kind := skillConflictMirrorDrift
	if !canonicalExists {
		kind = skillConflictCanonicalDeletedWithDrift
	}
	personalType := entry.PersonalType
	if personalType == "" {
		personalType = record.PersonalType
	}
	return SkillMirrorConflict{
		Kind:          kind,
		TargetID:      target.TargetID,
		Provider:      target.Provider,
		Scope:         target.Scope,
		PersonalType:  personalType,
		Name:          name,
		CanonicalID:   entry.CanonicalID,
		MirrorPath:    filepath.ToSlash(mirrorDir),
		CanonicalHash: entry.CanonicalHash,
		MirrorHash:    mirrorHash,
		PreviewHash:   mirrorHash,
		Actions:       driftActions(target.Scope),
	}
}

func canonicalRecordsByScopeName(records []canonicalSkillRecord) map[string]canonicalSkillRecord {
	out := make(map[string]canonicalSkillRecord, len(records))
	for _, record := range records {
		out[mirrorRecordKey(record.Scope, record.Name)] = record
	}
	return out
}

func mirrorRecordKey(scope, name string) string {
	return strings.TrimSpace(scope) + "/" + strings.ToLower(strings.TrimSpace(name))
}

func readTargetManifest(target SkillMirrorTarget) (SkillMirrorManifest, error) {
	manifest, err := readSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return newSkillMirrorManifest(target), nil
	}
	return manifest, err
}

func skillMirrorNames(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func driftActions(scope string) []SkillMirrorResolutionAction {
	if scope == skillScopePersonal {
		return mirrorActions("sync_back_to_personal", "personal_overwrite_mirror", "save_as_new_personal_skill")
	}
	return mirrorActions("sync_back_to_canonical", "canonical_overwrite_mirror", "save_as_new_skill")
}

func mirrorActions(actions ...string) []SkillMirrorResolutionAction {
	out := make([]SkillMirrorResolutionAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, SkillMirrorResolutionAction{Action: action})
	}
	return out
}

func sortMirrorConflicts(conflicts []SkillMirrorConflict) {
	sort.SliceStable(conflicts, func(i, j int) bool {
		if conflicts[i].Kind != conflicts[j].Kind {
			return conflicts[i].Kind < conflicts[j].Kind
		}
		return conflicts[i].Name < conflicts[j].Name
	})
}
