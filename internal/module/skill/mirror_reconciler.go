package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	skillConflictSameName                  = "same_name"
	skillConflictMirrorDrift               = "mirror_drift"
	skillConflictUnmanagedSameName         = "unmanaged_same_name"
	skillConflictUnmanagedProviderSkill    = "unmanaged_provider_skill"
	skillConflictCanonicalDeletedWithDrift = "canonical_deleted_with_drift"
	skillConflictMultiMirrorDrift          = "multi_mirror_drift"
	skillMirrorBackupDirName               = ".super-dolphin-mirror-backup"
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
	PreviewID   string
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
	recordsByScopeName := canonicalRecordsByMirrorKey(records)
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
	record, canonicalExists := mirrorCanonicalRecord(records, target, entry, name)
	if !managed {
		return unmanagedProviderSkillConflict(target, record, name, mirrorDir, mirrorHash, canonicalExists), true, nil
	}
	conflict, err := managedMirrorConflict(target, entry, record, name, mirrorDir, mirrorHash, canonicalExists)
	if err != nil {
		return SkillMirrorConflict{}, false, err
	}
	return conflict, conflict.Kind != "", nil
}

func mirrorCanonicalRecord(records map[string]canonicalSkillRecord, target SkillMirrorTarget, entry SkillMirrorEntry, name string) (canonicalSkillRecord, bool) {
	if record, ok := records[mirrorRecordKey(target.Scope, entry.PersonalType, name)]; ok {
		return record, true
	}
	if target.Scope != skillScopePersonal || strings.TrimSpace(entry.PersonalType) != "" {
		return canonicalSkillRecord{}, false
	}
	for _, personalType := range []string{personalSkillTypeUser, personalSkillTypeAgent, personalSkillTypeImported, personalSkillTypeHub} {
		if record, ok := records[mirrorRecordKey(skillScopePersonal, personalType, name)]; ok {
			return record, true
		}
	}
	return canonicalSkillRecord{}, false
}

func unmanagedProviderSkillConflict(target SkillMirrorTarget, record canonicalSkillRecord, name, mirrorDir, mirrorHash string, canonicalExists bool) SkillMirrorConflict {
	canonicalID := ""
	if canonicalExists {
		canonicalID = canonicalSourceID(record)
	}
	kind := skillConflictUnmanagedProviderSkill
	if canonicalExists {
		kind = skillConflictUnmanagedSameName
	}
	actions := mirrorActions("view_unmanaged", "import_to_personal_imported")
	if target.Scope != skillScopePersonal {
		if canonicalExists {
			actions = mirrorActions("view_unmanaged", "import_to_personal_imported", "takeover_provider_skill")
		} else {
			actions = mirrorActions("view_unmanaged", "import_to_personal_imported", "import_to_project", "takeover_provider_skill")
		}
	}
	return SkillMirrorConflict{
		Kind:         kind,
		TargetID:     target.TargetID,
		Provider:     target.Provider,
		Scope:        target.Scope,
		PersonalType: targetPersonalConflictType(target, record),
		Name:         name,
		CanonicalID:  canonicalID,
		MirrorPath:   filepath.ToSlash(mirrorDir),
		MirrorHash:   mirrorHash,
		PreviewHash:  mirrorHash,
		Actions:      actions,
	}
}

func targetPersonalConflictType(target SkillMirrorTarget, record canonicalSkillRecord) string {
	if target.Scope != skillScopePersonal {
		return ""
	}
	if strings.TrimSpace(record.PersonalType) != "" {
		return strings.TrimSpace(record.PersonalType)
	}
	return personalSkillTypeUser
}

func managedMirrorConflict(target SkillMirrorTarget, entry SkillMirrorEntry, record canonicalSkillRecord, name, mirrorDir, mirrorHash string, canonicalExists bool) (SkillMirrorConflict, error) {
	canonicalHash := entry.CanonicalHash
	if canonicalExists {
		var err error
		canonicalHash, err = stableMirrorDirectoryHash(record.Dir)
		if err != nil {
			return SkillMirrorConflict{}, err
		}
	}
	if canonicalExists && mirrorHash == entry.MirrorHash && canonicalHash == entry.CanonicalHash {
		return SkillMirrorConflict{}, nil
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
		CanonicalHash: canonicalHash,
		MirrorHash:    mirrorHash,
		PreviewHash:   mirrorHash,
		Actions:       driftActions(target.Scope),
	}, nil
}

func canonicalRecordsByMirrorKey(records []canonicalSkillRecord) map[string]canonicalSkillRecord {
	out := make(map[string]canonicalSkillRecord, len(records))
	for _, record := range records {
		out[mirrorRecordKey(record.Scope, record.PersonalType, record.Name)] = record
	}
	return out
}

func mirrorRecordKey(scope, personalType, name string) string {
	if strings.TrimSpace(scope) != skillScopePersonal {
		personalType = ""
	}
	return strings.TrimSpace(scope) + "/" + strings.TrimSpace(personalType) + "/" + strings.ToLower(strings.TrimSpace(name))
}

func readTargetManifest(target SkillMirrorTarget) (SkillMirrorManifest, error) {
	return loadSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile), target)
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
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("skill mirror entry is symlink: %s", filepath.Join(root, entry.Name()))
		}
		if entry.IsDir() && entry.Name() != skillMirrorBackupDirName {
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
	return mirrorActions("sync_back_to_canonical", "canonical_overwrite_mirror", "save_as_new_skill", "confirm_delete_drifted_mirror")
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

func replaceSkillDirFromMirror(sourceDir, targetDir string) error {
	return replaceSkillDirFromMirrorWithCopy(sourceDir, targetDir, copySkillDir)
}

func replaceSkillDirFromMirrorWithCopy(sourceDir, targetDir string, copyDir func(string, string) (int, int64, error)) error {
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(targetDir), "."+filepath.Base(targetDir)+".sync-*")
	if err != nil {
		return err
	}
	if err := os.Remove(tempDir); err != nil {
		_ = os.RemoveAll(tempDir)
		return err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if _, _, err := copyDir(sourceDir, tempDir); err != nil {
		return err
	}
	if !skillMainFileExists(tempDir) {
		return errSkillMainFileNotFound
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if err := os.Rename(tempDir, targetDir); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func backupSkillDir(targetDir string) (string, error) {
	if !skillMainFileExists(targetDir) {
		return "", nil
	}
	backupDir := mirrorBackupPathForTargetDir(targetDir, filepath.Base(targetDir)+"-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(filepath.Dir(backupDir), 0o755); err != nil {
		return "", err
	}
	if _, _, err := copySkillDir(targetDir, backupDir); err != nil {
		_ = os.RemoveAll(backupDir)
		return "", err
	}
	return backupDir, nil
}

func mirrorBackupPathForTargetDir(targetDir, leaf string) string {
	root := filepath.Dir(targetDir)
	return filepath.Join(filepath.Dir(root), skillMirrorBackupDirName, filepath.Base(root), leaf)
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
