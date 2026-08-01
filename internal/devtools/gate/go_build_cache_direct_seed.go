package gate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const GoBuildCacheDirectSeedManifestSchemaVersion uint32 = 1

var goBuildCacheDirectSeedDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// GoBuildCacheDirectSeedEntry 绑定只读挂载 Go 构建缓存树中的一个条目。
type GoBuildCacheDirectSeedEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	Type   string `json:"type"`
}

// GoBuildCacheDirectSeedManifest 把直读 DataCache 树绑定到生成它的运行时输入。
type GoBuildCacheDirectSeedManifest struct {
	SchemaVersion     uint32                        `json:"schema_version"`
	RuntimeGoSHA256   string                        `json:"runtime_go_sha256"`
	RuntimeDepsSHA256 string                        `json:"runtime_deps_sha256"`
	RootMode          uint32                        `json:"root_mode"`
	TreeSHA256        string                        `json:"tree_sha256"`
	Entries           []GoBuildCacheDirectSeedEntry `json:"entries"`
}

// BuildGoBuildCacheDirectSeedManifest 按规范顺序清点最终直读种子树。
func BuildGoBuildCacheDirectSeedManifest(root, runtimeGoSHA256, runtimeDepsSHA256 string) (GoBuildCacheDirectSeedManifest, error) {
	manifest := GoBuildCacheDirectSeedManifest{
		SchemaVersion:   GoBuildCacheDirectSeedManifestSchemaVersion,
		RuntimeGoSHA256: runtimeGoSHA256, RuntimeDepsSHA256: runtimeDepsSHA256,
	}
	if err := validateGoBuildCacheDirectRuntimeBindings(runtimeGoSHA256, runtimeDepsSHA256); err != nil {
		return GoBuildCacheDirectSeedManifest{}, err
	}
	rootMode, entries, err := collectGoBuildCacheDirectSeedEntries(root)
	if err != nil {
		return GoBuildCacheDirectSeedManifest{}, err
	}
	manifest.RootMode = rootMode
	manifest.Entries = entries
	manifest.TreeSHA256, err = goBuildCacheDirectSeedTreeDigest(rootMode, entries)
	if err != nil {
		return GoBuildCacheDirectSeedManifest{}, err
	}
	return manifest, nil
}

// ValidateGoBuildCacheDirectSeedManifest 拒绝过期运行时绑定和非法清单记录。
func ValidateGoBuildCacheDirectSeedManifest(manifest GoBuildCacheDirectSeedManifest) error {
	if manifest.SchemaVersion != GoBuildCacheDirectSeedManifestSchemaVersion {
		return errors.New("Go build cache direct seed manifest schema is invalid")
	}
	if err := validateGoBuildCacheDirectRuntimeBindings(manifest.RuntimeGoSHA256, manifest.RuntimeDepsSHA256); err != nil {
		return err
	}
	if len(manifest.Entries) == 0 || manifest.RootMode&0o222 != 0 {
		return errors.New("Go build cache direct seed manifest is empty")
	}
	if err := validateGoBuildCacheDirectSeedEntries(manifest.Entries); err != nil {
		return err
	}
	digest, err := goBuildCacheDirectSeedTreeDigest(manifest.RootMode, manifest.Entries)
	if err != nil {
		return err
	}
	if manifest.TreeSHA256 != digest {
		return errors.New("Go build cache direct seed manifest tree digest is invalid")
	}
	return nil
}

// MatchesRuntimeDeltas 判断直读种子能否在指定运行时差量后继续复用。
func (manifest GoBuildCacheDirectSeedManifest) MatchesRuntimeDeltas(runtimeGoSHA256, runtimeDepsSHA256 string) bool {
	return ValidateGoBuildCacheDirectSeedManifest(manifest) == nil &&
		manifest.RuntimeGoSHA256 == runtimeGoSHA256 && manifest.RuntimeDepsSHA256 == runtimeDepsSHA256
}

// ValidateGoBuildCacheDirectSeed 验证已挂载直读种子与清单完全一致。
func ValidateGoBuildCacheDirectSeed(root string, manifest GoBuildCacheDirectSeedManifest) error {
	if err := ValidateGoBuildCacheDirectSeedManifest(manifest); err != nil {
		return err
	}
	rootMode, actual, err := collectGoBuildCacheDirectSeedEntries(root)
	if err != nil {
		return err
	}
	if rootMode != manifest.RootMode || !slices.EqualFunc(actual, manifest.Entries, func(left, right GoBuildCacheDirectSeedEntry) bool { return left == right }) {
		return errors.New("Go build cache direct seed tree does not match manifest")
	}
	return nil
}

// ValidateGoBuildCacheDirectSeedMount 在分片热路径只校验已验收不可变 DataCache 的清单与挂载根。
func ValidateGoBuildCacheDirectSeedMount(root string, manifest GoBuildCacheDirectSeedManifest) error {
	if err := ValidateGoBuildCacheDirectSeedManifest(manifest); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("stat Go build cache direct seed mount: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o222 != 0 {
		return errors.New("Go build cache direct seed mount is not a physical read-only directory")
	}
	return nil
}

func validateGoBuildCacheDirectRuntimeBindings(runtimeGoSHA256, runtimeDepsSHA256 string) error {
	if !goBuildCacheDirectSeedDigestPattern.MatchString(runtimeGoSHA256) || !goBuildCacheDirectSeedDigestPattern.MatchString(runtimeDepsSHA256) {
		return errors.New("Go build cache direct seed runtime binding is invalid")
	}
	return nil
}

func collectGoBuildCacheDirectSeedEntries(root string) (uint32, []GoBuildCacheDirectSeedEntry, error) {
	trustedRoot, err := trustedDirectory(root, false, -1)
	if err != nil {
		return 0, nil, fmt.Errorf("Go build cache direct seed root: %w", err)
	}
	rootInfo, err := os.Lstat(trustedRoot)
	if err != nil {
		return 0, nil, err
	}
	rootMode := uint32(rootInfo.Mode().Perm())
	if rootMode&0o222 != 0 {
		return 0, nil, errors.New("Go build cache direct seed root is writable")
	}
	entries := make([]GoBuildCacheDirectSeedEntry, 0)
	err = filepath.WalkDir(trustedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == trustedRoot {
			return nil
		}
		relative, err := filepath.Rel(trustedRoot, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		seedEntry, err := newGoBuildCacheDirectSeedEntry(filepath.ToSlash(relative), path, info)
		if err != nil {
			return err
		}
		entries = append(entries, seedEntry)
		return nil
	})
	if err != nil {
		return 0, nil, fmt.Errorf("inventory Go build cache direct seed: %w", err)
	}
	if err := validateGoBuildCacheDirectSeedEntries(entries); err != nil {
		return 0, nil, err
	}
	return rootMode, entries, nil
}

func newGoBuildCacheDirectSeedEntry(relative, path string, info fs.FileInfo) (GoBuildCacheDirectSeedEntry, error) {
	entry := GoBuildCacheDirectSeedEntry{Path: relative, Mode: uint32(info.Mode().Perm())}
	if entry.Mode&0o222 != 0 {
		return GoBuildCacheDirectSeedEntry{}, fmt.Errorf("Go build cache direct seed entry %q is writable", relative)
	}
	switch {
	case info.IsDir():
		entry.Type = "directory"
	case info.Mode().IsRegular():
		digest, err := regularFileContentDigest(path, info.Size())
		if err != nil {
			return GoBuildCacheDirectSeedEntry{}, err
		}
		entry.Type, entry.Size = "file", info.Size()
		entry.SHA256 = fmt.Sprintf("sha256:%x", digest)
	case info.Mode()&os.ModeSymlink != 0:
		return GoBuildCacheDirectSeedEntry{}, fmt.Errorf("Go build cache direct seed entry %q is a symlink", relative)
	default:
		return GoBuildCacheDirectSeedEntry{}, fmt.Errorf("Go build cache direct seed entry %q has forbidden type", relative)
	}
	return entry, nil
}

func validateGoBuildCacheDirectSeedEntries(entries []GoBuildCacheDirectSeedEntry) error {
	previous := ""
	for _, entry := range entries {
		if entry.Path == "" || filepath.IsAbs(entry.Path) || filepath.ToSlash(filepath.Clean(entry.Path)) != entry.Path || strings.HasPrefix(entry.Path, "../") || entry.Path == ".." || entry.Path <= previous {
			return errors.New("Go build cache direct seed manifest entry path is invalid")
		}
		previous = entry.Path
		switch entry.Type {
		case "directory":
			if entry.SHA256 != "" || entry.Size != 0 || entry.Mode&0o222 != 0 {
				return errors.New("Go build cache direct seed manifest directory is invalid")
			}
		case "file":
			if !goBuildCacheDirectSeedDigestPattern.MatchString(entry.SHA256) || entry.Size < 0 || entry.Mode&0o222 != 0 {
				return errors.New("Go build cache direct seed manifest file is invalid")
			}
		default:
			return errors.New("Go build cache direct seed manifest entry type is invalid")
		}
	}
	return nil
}

func goBuildCacheDirectSeedTreeDigest(rootMode uint32, entries []GoBuildCacheDirectSeedEntry) (string, error) {
	digest := sha256.New()
	if err := writeSeedRecord(digest, 'V', []byte("super-dolphin-go-build-cache-direct-seed"), []byte("1")); err != nil {
		return "", err
	}
	if err := writeSeedRecord(digest, 'R', []byte(strconv.FormatUint(uint64(rootMode), 8))); err != nil {
		return "", err
	}
	for _, entry := range entries {
		mode := []byte(strconv.FormatUint(uint64(entry.Mode), 8))
		if err := writeSeedRecord(digest, 'E', []byte(entry.Path), []byte(entry.Type), mode, []byte(entry.SHA256), []byte(strconv.FormatInt(entry.Size, 10))); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}
