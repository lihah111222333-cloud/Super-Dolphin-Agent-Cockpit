package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		oldHash = skillDirContentHash(targetDir)
	}
	record := s.personalRecoveryRecord(action, name, scope, personalType, oldHash, "", now)
	if err := s.writeSkillMutationAudit(ctx, action+"_intent", record); err != nil {
		return personalSkillRecoveryRecord{}, err
	}
	return record, nil
}

func (s *service) finalizePersonalMutation(ctx context.Context, action, targetDir string, record personalSkillRecoveryRecord) error {
	record.NewHash = skillDirContentHash(targetDir)
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
	if _, _, err := copySkillDir(targetDir, backupDir); err != nil {
		_ = os.RemoveAll(backupDir)
		return "", err
	}
	return backupDir, nil
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

func copySkillDirContents(source, target string) (int, int64, error) {
	files, total := 0, int64(0)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(target, 0o755)
		}
		dst := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files, total = files+1, total+int64(len(data))
		return os.WriteFile(dst, data, 0o644)
	})
	return files, total, err
}
