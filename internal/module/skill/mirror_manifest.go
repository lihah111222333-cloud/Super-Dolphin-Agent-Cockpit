package skill

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/mirrorpath"
)

const skillMirrorManifestFile = ".super-dolphin-skill-mirror.json"

var errSkillMirrorManifestTargetMismatch = errors.New("skill mirror manifest target mismatch")

type SkillMirrorManifest struct {
	Version         int                         `json:"version"`
	Manager         string                      `json:"manager"`
	Scope           string                      `json:"scope"`
	Provider        string                      `json:"provider"`
	CanonicalRootID string                      `json:"canonical_root_id"`
	GeneratedAt     time.Time                   `json:"generated_at"`
	Skills          map[string]SkillMirrorEntry `json:"skills"`
}

type SkillMirrorEntry struct {
	CanonicalID   string `json:"canonical_id"`
	CanonicalHash string `json:"canonical_hash"`
	MirrorHash    string `json:"mirror_hash"`
	SourceType    string `json:"source_type"`
	PersonalType  string `json:"personal_type,omitempty"`
	Owned         bool   `json:"owned"`
}

// loadSkillMirrorManifest 加载技能镜像manifest。
func loadSkillMirrorManifest(path string, target SkillMirrorTarget) (SkillMirrorManifest, error) {
	manifest, err := readSkillMirrorManifest(path)
	if errors.Is(err, os.ErrNotExist) {
		return newSkillMirrorManifest(target), nil
	}
	if err != nil {
		return SkillMirrorManifest{}, err
	}
	if manifest.Provider != string(target.Provider) || manifest.Scope != target.Scope || manifest.CanonicalRootID != target.CanonicalRootID {
		return SkillMirrorManifest{}, errSkillMirrorManifestTargetMismatch
	}
	if manifest.Skills == nil {
		manifest.Skills = make(map[string]SkillMirrorEntry)
	}
	return manifest, nil
}

func repairMismatchedSkillMirrorManifest(records []canonicalSkillRecord, target SkillMirrorTarget) (SkillMirrorManifest, error) {
	if target.Scope != skillScopePersonal {
		return SkillMirrorManifest{}, errSkillMirrorManifestTargetMismatch
	}
	manifest, err := rebuiltSkillMirrorManifest(records, target)
	if err != nil {
		return SkillMirrorManifest{}, err
	}
	if err := writeSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile), manifest); err != nil {
		return SkillMirrorManifest{}, err
	}
	return manifest, nil
}

// rebuiltSkillMirrorManifest 根据现有 mirror 目录重建 manifest。
// 只接纳 hash 仍匹配 canonical 记录的目录，漂移或未知目录留给冲突处理。
func rebuiltSkillMirrorManifest(records []canonicalSkillRecord, target SkillMirrorTarget) (SkillMirrorManifest, error) {
	manifest := newSkillMirrorManifest(target)
	for _, record := range recordsForMirrorTarget(records, target) {
		mirrorDir := filepath.Join(target.Root, record.Name)
		_, exists, err := existingMirrorHash(mirrorDir)
		if err != nil {
			return SkillMirrorManifest{}, err
		}
		if !exists {
			continue
		}
		canonicalHash, err := stableMirrorDirectoryHash(record.Dir)
		if err != nil {
			return SkillMirrorManifest{}, err
		}
		expectedMirrorHash, err := expectedCanonicalMirrorHash(target.Root, record, target.Scope)
		if err != nil {
			return SkillMirrorManifest{}, err
		}
		manifest.Skills[record.Name] = mirrorManifestEntry(record, canonicalHash, expectedMirrorHash)
	}
	return manifest, nil
}

func expectedCanonicalMirrorHash(root string, record canonicalSkillRecord, scope string) (string, error) {
	hash, _, err := expectedCanonicalMirrorHashes(root, record, scope)
	return hash, err
}

func expectedCanonicalMirrorHashes(root string, record canonicalSkillRecord, scope string) (string, string, error) {
	tempDir, err := os.MkdirTemp(root, ".skill-mirror-hash-"+record.Name+"-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tempDir)
	if err := copyCanonicalSkillDir(record.Dir, tempDir, scope, skillMirrorIdentity{Name: record.Name, DisplayName: record.info.DisplayName}); err != nil {
		return "", "", err
	}
	hash, err := stableMirrorDirectoryHash(tempDir)
	if err != nil {
		return "", "", err
	}
	contentHash, err := skillDirContentHash(tempDir)
	if err != nil {
		return "", "", err
	}
	return hash, contentHash, nil
}

func newSkillMirrorManifest(target SkillMirrorTarget) SkillMirrorManifest {
	return SkillMirrorManifest{
		Version:         1,
		Manager:         "super-dolphin",
		Scope:           target.Scope,
		Provider:        string(target.Provider),
		CanonicalRootID: target.CanonicalRootID,
		GeneratedAt:     time.Now().UTC(),
		Skills:          make(map[string]SkillMirrorEntry),
	}
}

func mirrorManifestEntry(record canonicalSkillRecord, canonicalHash, mirrorHash string) SkillMirrorEntry {
	return SkillMirrorEntry{
		CanonicalID:   canonicalSourceID(record),
		CanonicalHash: canonicalHash,
		MirrorHash:    mirrorHash,
		SourceType:    record.Scope,
		PersonalType:  record.PersonalType,
		Owned:         true,
	}
}

// writeSkillMirrorManifest 写入技能镜像manifest。
func writeSkillMirrorManifest(path string, manifest SkillMirrorManifest) error {
	if filepath.Base(path) != skillMirrorManifestFile {
		return fmt.Errorf("skill mirror manifest path must end with %s", skillMirrorManifestFile)
	}
	if err := ensureMirrorManifestRegularPath(path, true); err != nil {
		return err
	}
	if err := validateSkillMirrorManifest(manifest); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal skill mirror manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create skill mirror manifest dir: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
}

// writeFileAtomic 写入文件atomic。
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func readSkillMirrorManifest(path string) (SkillMirrorManifest, error) {
	if filepath.Base(path) != skillMirrorManifestFile {
		return SkillMirrorManifest{}, fmt.Errorf("skill mirror manifest path must end with %s", skillMirrorManifestFile)
	}
	if err := ensureMirrorManifestRegularPath(path, false); err != nil {
		return SkillMirrorManifest{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillMirrorManifest{}, fmt.Errorf("read skill mirror manifest: %w", err)
	}
	var manifest SkillMirrorManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return SkillMirrorManifest{}, fmt.Errorf("decode skill mirror manifest: %w", err)
	}
	return manifest, validateSkillMirrorManifest(manifest)
}

// ensureMirrorManifestRegularPath 确保镜像manifestregular路径。
func ensureMirrorManifestRegularPath(path string, allowMissing bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat skill mirror manifest: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill mirror manifest is symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("skill mirror manifest is not a regular file: %s", path)
	}
	return nil
}

// validateSkillMirrorManifest 校验技能镜像manifest。
func validateSkillMirrorManifest(manifest SkillMirrorManifest) error {
	for name, entry := range manifest.Skills {
		if _, err := validateSkillName(name); err != nil {
			return fmt.Errorf("skill mirror manifest has unsafe skill name %q: %w", name, err)
		}
		if strings.TrimSpace(manifest.Scope) == skillScopePersonal {
			if err := validatePersonalMirrorEntry(name, entry); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(manifest.Scope) != skillScopePersonal {
		return nil
	}
	if !strings.HasPrefix(strings.TrimSpace(manifest.CanonicalRootID), "sd_owner:") {
		return fmt.Errorf("personal mirror canonical_root_id must be owner_key")
	}
	return nil
}

func validatePersonalMirrorEntry(name string, entry SkillMirrorEntry) error {
	canonicalID := filepath.ToSlash(strings.TrimSpace(entry.CanonicalID))
	personalType := strings.TrimSpace(entry.PersonalType)
	if mirrorpath.UnsafeRelative(canonicalID) {
		return fmt.Errorf("personal mirror %s canonical_id must be home-relative", name)
	}
	canonicalType, err := validatePersonalMirrorCanonicalID(name, canonicalID)
	if err != nil {
		return err
	}
	if personalType == "" {
		return fmt.Errorf("personal mirror %s personal_type is required", name)
	}
	if personalType != canonicalType {
		return fmt.Errorf("personal mirror %s personal_type does not match canonical_id", name)
	}
	return nil
}

// validatePersonalMirrorCanonicalID 校验 personal mirror 的 canonical_id 与个人类型一致。
func validatePersonalMirrorCanonicalID(name, canonicalID string) (string, error) {
	parts := strings.Split(canonicalID, "/")
	if len(parts) < 3 || parts[0] != skillScopePersonal {
		return "", fmt.Errorf("personal mirror %s canonical_id must be personal/<type>/<name>", name)
	}
	if _, normalizedType, err := normalizeSkillTarget(skillScopePersonal, parts[1]); err != nil || normalizedType != parts[1] {
		return "", fmt.Errorf("personal mirror %s canonical_id has invalid personal type", name)
	}
	if strings.Contains(canonicalID, ".claude/") || strings.Contains(canonicalID, ".agents/") || strings.Contains(canonicalID, ".codex/") || strings.Contains(canonicalID, "providers/") {
		return "", fmt.Errorf("personal mirror %s canonical_id must not reference provider paths", name)
	}
	return parts[1], nil
}

type mirrorHashFile struct {
	rel  string
	mode fs.FileMode
	data []byte
}

func stableMirrorDirectoryHash(root string) (string, error) {
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", fmt.Errorf("normalize mirror root: %w", err)
	}
	files, err := collectMirrorHashFiles(filepath.Clean(absRoot))
	if err != nil {
		return "", err
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return hashMirrorFiles(files), nil
}

func collectMirrorHashFiles(root string) ([]mirrorHashFile, error) {
	var files []mirrorHashFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		file, err := readMirrorHashFile(root, path, entry, walkErr)
		if err != nil || file == nil {
			return err
		}
		files = append(files, *file)
		return nil
	})
	return files, err
}

// readMirrorHashFile 读取镜像hash文件。
func readMirrorHashFile(root, path string, entry fs.DirEntry, walkErr error) (*mirrorHashFile, error) {
	if walkErr != nil || entry == nil || entry.IsDir() {
		return nil, walkErr
	}
	info, err := mirrorpath.SafeFileInfo(path, entry)
	if err != nil {
		return nil, err
	}
	rel, err := mirrorpath.SafeRelative(root, path)
	if err != nil || rel == skillMirrorManifestFile {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mirror file %s: %w", path, err)
	}
	return &mirrorHashFile{rel: rel, mode: info.Mode(), data: data}, nil
}

func hashMirrorFiles(files []mirrorHashFile) string {
	h := sha256.New()
	for _, file := range files {
		writeHashBytes(h, []byte(file.rel))
		writeHashUint32(h, uint32(file.mode.Perm()))
		writeHashBytes(h, file.data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeHashBytes(h hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

func writeHashUint32(h hash.Hash, value uint32) {
	var data [4]byte
	binary.BigEndian.PutUint32(data[:], value)
	_, _ = h.Write(data[:])
}

func filterCanonicalRecordsForScope(records []canonicalSkillRecord, scope string) []canonicalSkillRecord {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return records
	}
	filtered := make([]canonicalSkillRecord, 0, len(records))
	for _, record := range records {
		if record.Scope == scope {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func (s *service) writeTimeMirrorTargets(cwd, scope string) []SkillMirrorTarget {
	scope = strings.TrimSpace(scope)
	if scope == skillScopePersonal {
		return uniqueMirrorTargets(append(s.defaultPersonalMirrorTargets(), s.configuredMirrorTargets(scope)...))
	}
	if scope != skillScopeProject {
		return uniqueMirrorTargets(s.configuredMirrorTargets(scope))
	}
	projectRoot := s.projectRootForCWD(cwd)
	fingerprint := RepoFingerprint(projectRoot)
	targets := []SkillMirrorTarget{
		{
			TargetID:        "claude:project:" + fingerprint,
			Provider:        SkillProviderClaude,
			Scope:           skillScopeProject,
			Root:            providerProjectMirrorRoot(SkillProviderClaude, projectRoot),
			CanonicalRootID: fingerprint,
		},
		{
			TargetID:        "codex:project:" + fingerprint,
			Provider:        SkillProviderCodex,
			Scope:           skillScopeProject,
			Root:            providerProjectMirrorRoot(SkillProviderCodex, projectRoot),
			CanonicalRootID: fingerprint,
		},
	}
	return uniqueMirrorTargets(targets)
}

func (s *service) defaultPersonalMirrorTargets() []SkillMirrorTarget {
	superHome := s.resolvedSuperDolphinHome()
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		slog.Warn("skill: defaultPersonalMirrorTargets resolveOwnerIdentity failed", "error", err)
		return nil
	}
	return []SkillMirrorTarget{
		{
			TargetID:        "claude:user-global:" + owner.OwnerKey,
			Provider:        SkillProviderClaude,
			Scope:           skillScopePersonal,
			Root:            providerPersonalMirrorRoot(SkillProviderClaude),
			CanonicalRootID: owner.OwnerKey,
		},
		{
			TargetID:        "codex:user-global:" + owner.OwnerKey,
			Provider:        SkillProviderCodex,
			Scope:           skillScopePersonal,
			Root:            providerPersonalMirrorRoot(SkillProviderCodex),
			CanonicalRootID: owner.OwnerKey,
		},
	}
}

func providerProjectMirrorRoot(provider SkillProvider, projectRoot string) string {
	switch provider {
	case SkillProviderClaude:
		return filepath.Join(projectRoot, ".claude", "skills")
	case SkillProviderCodex:
		return filepath.Join(projectRoot, ".agents", "skills")
	default:
		return filepath.Join(projectRoot, "."+string(provider), "skills")
	}
}

func providerPersonalMirrorRoot(provider SkillProvider) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch provider {
	case SkillProviderClaude:
		return filepath.Join(home, ".claude", "skills")
	case SkillProviderCodex:
		return filepath.Join(home, ".agents", "skills")
	default:
		return ""
	}
}

func (s *service) configuredMirrorTargets(scope string) []SkillMirrorTarget {
	if s == nil {
		return nil
	}
	targets := make([]SkillMirrorTarget, 0, len(s.mirrorTargets))
	for _, target := range s.mirrorTargets {
		if strings.TrimSpace(target.Scope) == scope {
			targets = append(targets, target)
		}
	}
	return targets
}

func uniqueMirrorTargets(targets []SkillMirrorTarget) []SkillMirrorTarget {
	seen := make(map[string]bool, len(targets))
	out := make([]SkillMirrorTarget, 0, len(targets))
	for _, target := range targets {
		key := target.TargetID + "\x00" + filepath.Clean(target.Root)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, target)
	}
	return out
}

func unconfiguredMirrorPublishReport(scope, personalType, name string) SkillMirrorReport {
	if scope != skillScopePersonal {
		return SkillMirrorReport{}
	}
	return SkillMirrorReport{Conflicts: []SkillMirrorReportItem{{
		TargetID:           "personal:unconfigured",
		Scope:              skillScopePersonal,
		CanonicalID:        canonicalIDForPublishReport(scope, personalType, name),
		ConflictKind:       "publish_targets_unconfigured",
		RelativeMirrorPath: "",
	}}}
}

func mirrorPublishErrorReport(targets []SkillMirrorTarget, scope, personalType, name string, err error) SkillMirrorReport {
	report := SkillMirrorReport{Conflicts: make([]SkillMirrorReportItem, 0, len(targets))}
	for _, target := range targets {
		item := reportItem(target, skillSlug(name), canonicalIDForPublishReport(scope, personalType, name), "", "", "publish_error")
		if err != nil {
			item.Error = err.Error()
		}
		report.Conflicts = append(report.Conflicts, item)
	}
	return report
}

func canonicalIDForPublishReport(scope, personalType, name string) string {
	slug := skillSlug(name)
	if scope == skillScopePersonal {
		return "personal/" + strings.TrimSpace(personalType) + "/" + slug
	}
	return "project/" + slug
}

func attachMirrorPublish(result any, report SkillMirrorReport) any {
	if payload, ok := result.(map[string]any); ok {
		payload["mirror_publish"] = report
		return payload
	}
	return result
}
