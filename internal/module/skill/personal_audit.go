package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/ownerperms"
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
)

const (
	skillMutationAuditEventType      = "skill_mutation"
	personalSkillArchiveRecordFile   = ".super-dolphin-skill-archive.json"
	personalSkillRecoveryRecordFile  = ".super-dolphin-skill-recovery.json"
	personalSkillRecoverySnapshotDir = ".super-dolphin-recovery-snapshot"
)

type personalSkillArchiveRecord struct {
	ArchiveID     string `json:"archive_id"`
	ArchivePath   string `json:"archive_path"`
	CanonicalID   string `json:"canonical_id"`
	Scope         string `json:"scope"`
	PersonalType  string `json:"personal_type"`
	Name          string `json:"name"`
	CanonicalHash string `json:"canonical_hash"`
	Actor         string `json:"actor"`
	Timestamp     string `json:"timestamp"`
}

type personalSkillRecoveryRecord struct {
	Action       string `json:"action"`
	CanonicalID  string `json:"canonical_id"`
	Scope        string `json:"scope"`
	PersonalType string `json:"personal_type"`
	Name         string `json:"name"`
	OldHash      string `json:"old_hash,omitempty"`
	NewHash      string `json:"new_hash,omitempty"`
	Actor        string `json:"actor"`
	Timestamp    string `json:"timestamp"`
}

type personalSkillPolicy struct {
	Version      int                         `json:"version"`
	OwnerKey     string                      `json:"owner_key"`
	KeepSelected []personalSkillKeepSelected `json:"keep_selected,omitempty"`
}

type personalSkillKeepSelected struct {
	Name                 string                      `json:"name"`
	SelectedSourceID     string                      `json:"selected_source_id"`
	SelectedPersonalType string                      `json:"selected_personal_type"`
	SelectedContentHash  string                      `json:"selected_content_hash"`
	ExcludedSourceIDs    []string                    `json:"excluded_source_ids,omitempty"`
	Sources              []personalSkillPolicySource `json:"sources,omitempty"`
}

type personalSkillPolicySource struct {
	CanonicalID  string `json:"canonical_id"`
	PersonalType string `json:"personal_type"`
	ContentHash  string `json:"content_hash"`
	Path         string `json:"path,omitempty"`
}

// applyPersonalSkillPolicy 应用personal技能策略。
func (s *canonicalStore) applyPersonalSkillPolicy(records []canonicalSkillRecord) ([]canonicalSkillRecord, error) {
	if strings.TrimSpace(s.superDolphinHome) == "" || strings.TrimSpace(s.osUID) == "" {
		return records, nil
	}
	policy, err := s.readPersonalSkillPolicy()
	if err != nil {
		return nil, err
	}
	if len(policy.KeepSelected) == 0 {
		return records, nil
	}
	selectionByName, err := personalSelectionByName(policy.KeepSelected)
	if err != nil {
		return nil, err
	}
	sourceIDsByName := canonicalSourceIDsByName(records)
	return filterCanonicalRecords(records, func(record canonicalSkillRecord) bool {
		return keepCanonicalRecordForPersonalSelection(record, selectionByName, sourceIDsByName)
	}), nil
}

func personalSelectionByName(selections []personalSkillKeepSelected) (map[string]personalSkillKeepSelected, error) {
	selectionByName := make(map[string]personalSkillKeepSelected, len(selections))
	for _, selection := range selections {
		name, _, err := normalizeSkillIdentityName(selection.Name, "")
		if err != nil {
			return nil, fmt.Errorf("invalid personal skill policy name: %w", err)
		}
		selection.Name = name
		selectionByName[strings.ToLower(name)] = selection
	}
	return selectionByName, nil
}

// keepCanonicalRecordForPersonalSelection 为personalselection处理keepcanonical记录。
func keepCanonicalRecordForPersonalSelection(record canonicalSkillRecord, selectionByName map[string]personalSkillKeepSelected, sourceIDsByName map[string]map[string]struct{}) bool {
	selection, ok := selectionByName[strings.ToLower(record.Name)]
	if !ok {
		return true
	}
	sourceID := canonicalSourceID(record)
	if strings.TrimSpace(selection.SelectedSourceID) != "" &&
		!canonicalSourceIDExistsForName(sourceIDsByName, selection.Name, selection.SelectedSourceID) {
		return true
	}
	if selectedCanonicalRecord(record, selection, sourceID) {
		return true
	}
	if !stringSliceContains(selection.ExcludedSourceIDs, sourceID) {
		return true
	}
	return !personalSelectionSourceMatchesRecord(selection, record, sourceID)
}

func selectedCanonicalRecord(record canonicalSkillRecord, selection personalSkillKeepSelected, sourceID string) bool {
	return sourceID == strings.TrimSpace(selection.SelectedSourceID) &&
		record.PersonalType == strings.TrimSpace(selection.SelectedPersonalType) &&
		record.ContentHash == strings.TrimSpace(selection.SelectedContentHash)
}

// personalSelectionSourceMatchesRecord 处理personalselectionsourcematches记录。
func personalSelectionSourceMatchesRecord(selection personalSkillKeepSelected, record canonicalSkillRecord, sourceID string) bool {
	for _, source := range selection.Sources {
		if strings.TrimSpace(source.CanonicalID) != sourceID {
			continue
		}
		if strings.TrimSpace(source.ContentHash) != "" && strings.TrimSpace(source.ContentHash) != record.ContentHash {
			return false
		}
		if strings.TrimSpace(source.Path) != "" && cleanSlashPath(source.Path) != cleanSlashPath(record.Dir) {
			return false
		}
		return true
	}
	return true
}

func cleanSlashPath(path string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
}

// readPersonalSkillPolicy 读取personal技能策略。
func (s *canonicalStore) readPersonalSkillPolicy() (personalSkillPolicy, error) {
	var policy personalSkillPolicy
	path := filepath.Join(s.superDolphinHome, "skills", personalSkillPolicyFile)
	if err := ownerperms.ValidateOwnerOnlyFilePath(path, "owner policy"); err != nil {
		return personalSkillPolicy{}, err
	}
	if err := readJSONFileIfExists(path, &policy); err != nil {
		return personalSkillPolicy{}, err
	}
	if policy.OwnerKey == "" {
		return policy, nil
	}
	owner, err := resolveOwnerIdentity(s.superDolphinHome, s.osUID, s.appProfile)
	if err != nil {
		return personalSkillPolicy{}, err
	}
	if policy.OwnerKey != owner.OwnerKey {
		return personalSkillPolicy{}, nil
	}
	return policy, nil
}

func (s *service) personalDeleteArchiveRecord(name, scope, personalType, archiveDir, canonicalHash string, now time.Time) (personalSkillArchiveRecord, error) {
	archiveRel, err := filepath.Rel(s.resolvedSuperDolphinHome(), archiveDir)
	if err != nil {
		return personalSkillArchiveRecord{}, err
	}
	archiveRel = filepath.ToSlash(filepath.Clean(archiveRel))
	if archiveRel == "." || strings.HasPrefix(archiveRel, "../") {
		return personalSkillArchiveRecord{}, fmt.Errorf("archive path escapes super dolphin home: %s", archiveDir)
	}
	return personalSkillArchiveRecord{
		ArchiveID:     filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(archiveDir)))),
		ArchivePath:   archiveRel,
		CanonicalID:   skillScopePersonal + "/" + personalType + "/" + name,
		Scope:         scope,
		PersonalType:  personalType,
		Name:          name,
		CanonicalHash: canonicalHash,
		Actor:         "super-dolphin",
		Timestamp:     now.UTC().Format(time.RFC3339Nano),
	}, nil
}

func (s *service) writePersonalArchiveRecord(record personalSkillArchiveRecord, archiveDir string) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(archiveDir, personalSkillArchiveRecordFile), data, 0o600)
}

func ensureSkillMainFilePresent(dir string) error {
	skillFile := filepath.Join(dir, skillMainFile)
	info, err := os.Stat(skillFile)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.ErrNotExist
	case err != nil:
		return err
	case info.IsDir():
		return fmt.Errorf("path is directory: %s", skillFile)
	default:
		return nil
	}
}

func (s *service) personalSkillArchiveDir(scope, personalType, name string) string {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	return filepath.Join(s.resolvedSuperDolphinHome(), "skills", ".archive", stamp, scope, personalType, skillSlug(name))
}

func (s *service) personalRecoveryRecord(action, name, scope, personalType, oldHash, newHash string, now time.Time) personalSkillRecoveryRecord {
	return personalSkillRecoveryRecord{
		Action:       action,
		CanonicalID:  skillScopePersonal + "/" + personalType + "/" + name,
		Scope:        scope,
		PersonalType: personalType,
		Name:         name,
		OldHash:      oldHash,
		NewHash:      newHash,
		Actor:        "super-dolphin",
		Timestamp:    now.UTC().Format(time.RFC3339Nano),
	}
}

func (s *service) preparePersonalMutation(ctx context.Context, action, name, targetDir, scope, personalType string) (personalSkillRecoveryRecord, error) {
	now := time.Now().UTC()
	oldHash := ""
	if skillMainFileExists(targetDir) {
		var err error
		oldHash, err = skillDirContentHash(targetDir)
		if err != nil {
			return personalSkillRecoveryRecord{}, err
		}
	}
	record := s.personalRecoveryRecord(action, name, scope, personalType, oldHash, "", now)
	if err := s.writeSkillMutationAudit(ctx, action+"_intent", record); err != nil {
		return personalSkillRecoveryRecord{}, err
	}
	return record, nil
}

func (s *service) finalizePersonalMutation(ctx context.Context, action, targetDir string, record personalSkillRecoveryRecord) error {
	newHash, err := skillDirContentHash(targetDir)
	if err != nil {
		return err
	}
	record.NewHash = newHash
	if err := s.writePersonalRecoveryRecord(record, targetDir); err != nil {
		return err
	}
	return s.writeSkillMutationAudit(ctx, action+"_finalize", record)
}

func (s *service) writePersonalRecoveryRecord(record personalSkillRecoveryRecord, targetDir string) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(targetDir, personalSkillRecoveryRecordFile), data, 0o600)
}

func backupExistingPersonalSkill(targetDir string) (string, error) {
	if !skillMainFileExists(targetDir) {
		return "", nil
	}
	backupDir := filepath.Join(filepath.Dir(targetDir), personalSkillRecoverySnapshotDir, filepath.Base(targetDir)+"-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(filepath.Dir(backupDir), 0o755); err != nil {
		return "", err
	}
	if _, _, err := copySkillDir(targetDir, backupDir); err != nil {
		_ = os.RemoveAll(backupDir)
		return "", err
	}
	return backupDir, nil
}

func rollbackPersonalSkillDir(targetDir, backupDir string) error {
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if strings.TrimSpace(backupDir) == "" {
		return nil
	}
	if _, _, err := copySkillDir(backupDir, targetDir); err != nil {
		return err
	}
	if err := os.RemoveAll(backupDir); err != nil {
		return err
	}
	return nil
}

func restoreDeletedPersonalSkill(dir, archiveDir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(archiveDir, dir); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, personalSkillArchiveRecordFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *service) writeSkillMutationAudit(ctx context.Context, action string, record any) error {
	if s == nil || s.auditStore == nil {
		return errors.New("skill mutation audit store is not configured")
	}
	extra, err := json.Marshal(record)
	if err != nil {
		return err
	}
	actor, target, detail := skillMutationAuditFields(record)
	return s.auditStore.Insert(ctx, auditstore.InsertParams{
		EventType: skillMutationAuditEventType,
		Action:    action,
		Result:    skillMutationAuditResult(action),
		Actor:     actor,
		Target:    target,
		Detail:    detail,
		Level:     "info",
		Extra:     extra,
	})
}

func skillMutationAuditFields(record any) (string, string, string) {
	switch r := record.(type) {
	case personalSkillArchiveRecord:
		return r.Actor, r.CanonicalID, r.ArchivePath
	case personalSkillRecoveryRecord:
		return r.Actor, r.CanonicalID, r.Action
	case skillMirrorMutationAuditRecord:
		return r.Actor, r.CanonicalID, r.Action
	default:
		return "super-dolphin", "", ""
	}
}

func skillMutationAuditResult(action string) string {
	if strings.HasSuffix(action, "_finalize") {
		return "success"
	}
	return "intent"
}

// copySkillDirContents 复制技能目录contents。
