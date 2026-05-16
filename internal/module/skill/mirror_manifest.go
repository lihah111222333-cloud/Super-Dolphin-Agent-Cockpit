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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const skillMirrorManifestFile = ".super-dolphin-skill-mirror.json"

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
	return os.WriteFile(path, append(data, '\n'), 0o644)
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

func validateSkillMirrorManifest(manifest SkillMirrorManifest) error {
	if strings.TrimSpace(manifest.Scope) != skillScopePersonal {
		return nil
	}
	if !strings.HasPrefix(strings.TrimSpace(manifest.CanonicalRootID), "sd_owner:") {
		return fmt.Errorf("personal mirror canonical_root_id must be owner_key")
	}
	for name, entry := range manifest.Skills {
		if err := validatePersonalMirrorEntry(name, entry); err != nil {
			return err
		}
	}
	return nil
}

func validatePersonalMirrorEntry(name string, entry SkillMirrorEntry) error {
	canonicalID := filepath.ToSlash(strings.TrimSpace(entry.CanonicalID))
	personalType := strings.TrimSpace(entry.PersonalType)
	if unsafeMirrorRelativePath(canonicalID) {
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

func validatePersonalMirrorCanonicalID(name, canonicalID string) (string, error) {
	parts := strings.Split(canonicalID, "/")
	if len(parts) < 3 || parts[0] != skillScopePersonal {
		return "", fmt.Errorf("personal mirror %s canonical_id must be personal/<type>/<name>", name)
	}
	if _, normalizedType, err := normalizeSkillTarget(skillScopePersonal, parts[1]); err != nil || normalizedType != parts[1] {
		return "", fmt.Errorf("personal mirror %s canonical_id has invalid personal type", name)
	}
	if strings.Contains(canonicalID, ".claude/") || strings.Contains(canonicalID, ".codex/") || strings.Contains(canonicalID, "providers/") {
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

func readMirrorHashFile(root, path string, entry fs.DirEntry, walkErr error) (*mirrorHashFile, error) {
	if walkErr != nil || entry == nil || entry.IsDir() {
		return nil, walkErr
	}
	info, err := safeMirrorFileInfo(path, entry)
	if err != nil {
		return nil, err
	}
	rel, err := safeMirrorRelativePath(root, path)
	if err != nil || rel == skillMirrorManifestFile {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mirror file %s: %w", path, err)
	}
	return &mirrorHashFile{rel: rel, mode: info.Mode(), data: data}, nil
}

func safeMirrorFileInfo(path string, entry fs.DirEntry) (fs.FileInfo, error) {
	info, err := entry.Info()
	if err != nil {
		return nil, fmt.Errorf("stat mirror path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe mirror path %s", path)
	}
	return info, nil
}

func safeMirrorRelativePath(root, path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("normalize mirror path %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, filepath.Clean(absPath))
	if err != nil {
		return "", fmt.Errorf("rel mirror path %s: %w", path, err)
	}
	rel = filepath.ToSlash(rel)
	if unsafeMirrorRelativePath(rel) {
		return "", fmt.Errorf("unsafe mirror path %s escapes root", path)
	}
	return rel, nil
}

func unsafeMirrorRelativePath(rel string) bool {
	if unsafeMirrorRelativeValue(rel) {
		return true
	}
	for _, part := range strings.Split(rel, "/") {
		if unsafeMirrorRelativePart(part) {
			return true
		}
	}
	return false
}

func unsafeMirrorRelativeValue(rel string) bool {
	return rel == "" || rel == "." || rel == ".." ||
		filepath.IsAbs(rel) || strings.Contains(rel, "\x00") ||
		strings.HasPrefix(rel, "../")
}

func unsafeMirrorRelativePart(part string) bool {
	return part == "" || part == "." || part == ".."
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
