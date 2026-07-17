package gate

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RuntimeSeedSchemaVersion is the shared image-builder/executor manifest schema.
const RuntimeSeedSchemaVersion = 1

// RuntimeSeedManifest binds immutable runtime seeds to repository lock files.
type RuntimeSeedManifest struct {
	SchemaVersion         uint32 `json:"schema_version"`
	GoSumSHA256           string `json:"go_sum_sha256"`
	VendorTreeSHA256      string `json:"vendor_tree_sha256"`
	PackageLockSHA256     string `json:"package_lock_sha256"`
	NodeModulesTreeSHA256 string `json:"node_modules_tree_sha256"`
}

// installRuntimeSeeds 按门禁需要校验 manifest 后安装锁文件绑定的依赖种子。
func installRuntimeSeeds(config executorConfig, layout executorLayout, program ExecutorProgram) error {
	if !program.NeedsGoSeed && !program.NeedsFrontendSeed {
		return nil
	}
	manifest, err := LoadRuntimeSeedManifest(config.runtimeSeedManifest)
	if err != nil {
		return err
	}
	if program.NeedsGoSeed {
		if err := installBoundSeed(
			filepath.Join(layout.sourceCopy, "go.sum"), manifest.GoSumSHA256,
			filepath.Join(config.runtimeSeedRoot, "vendor"), manifest.VendorTreeSHA256,
			filepath.Join(layout.sourceCopy, "vendor"),
		); err != nil {
			return fmt.Errorf("install Go runtime seed: %w", err)
		}
	}
	if program.NeedsFrontendSeed {
		if err := installBoundSeed(
			filepath.Join(layout.sourceCopy, "frontend-app", "package-lock.json"), manifest.PackageLockSHA256,
			filepath.Join(config.runtimeSeedRoot, "frontend", "node_modules"), manifest.NodeModulesTreeSHA256,
			filepath.Join(layout.sourceCopy, "frontend-app", "node_modules"),
		); err != nil {
			return fmt.Errorf("install frontend runtime seed: %w", err)
		}
	}
	return nil
}

// LoadRuntimeSeedManifest 从固定镜像路径严格解码种子清单。
func LoadRuntimeSeedManifest(path string) (RuntimeSeedManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("open runtime seed manifest: %w", err)
	}
	defer file.Close()
	return DecodeRuntimeSeedManifest(io.LimitReader(file, 1<<20))
}

// DecodeRuntimeSeedManifest 拒绝未知字段、尾随 JSON 和非法摘要。
func DecodeRuntimeSeedManifest(reader io.Reader) (RuntimeSeedManifest, error) {
	if reader == nil {
		return RuntimeSeedManifest{}, errors.New("runtime seed manifest reader is required")
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var manifest RuntimeSeedManifest
	if err := decoder.Decode(&manifest); err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("decode runtime seed manifest: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return RuntimeSeedManifest{}, err
	}
	if err := manifest.validateShape(); err != nil {
		return RuntimeSeedManifest{}, err
	}
	return manifest, nil
}

// EncodeRuntimeSeedManifest 输出镜像构建器与执行器共用的规范 JSON。
func EncodeRuntimeSeedManifest(writer io.Writer, manifest RuntimeSeedManifest) error {
	if writer == nil {
		return errors.New("runtime seed manifest writer is required")
	}
	if err := manifest.validateShape(); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("encode runtime seed manifest: %w", err)
	}
	return nil
}

func (manifest RuntimeSeedManifest) validateShape() error {
	if manifest.SchemaVersion != RuntimeSeedSchemaVersion {
		return fmt.Errorf("runtime seed schema = %d, want %d", manifest.SchemaVersion, RuntimeSeedSchemaVersion)
	}
	for name, digest := range map[string]string{
		"go_sum_sha256": manifest.GoSumSHA256, "vendor_tree_sha256": manifest.VendorTreeSHA256,
		"package_lock_sha256": manifest.PackageLockSHA256, "node_modules_tree_sha256": manifest.NodeModulesTreeSHA256,
	} {
		if !validSHA256Digest(digest) {
			return fmt.Errorf("runtime seed manifest %s is invalid", name)
		}
	}
	return nil
}

// BuildRuntimeSeedManifest 从源码快照与运行时种子根构造共享清单。
func BuildRuntimeSeedManifest(snapshotRoot string, runtimeRoot string) (RuntimeSeedManifest, error) {
	snapshotPath, err := trustedDirectory(snapshotRoot, false, -1)
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("source snapshot: %w", err)
	}
	runtimePath, err := trustedDirectory(runtimeRoot, false, -1)
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("runtime seed root: %w", err)
	}
	goSum, err := fileSHA256(filepath.Join(snapshotPath, "go.sum"))
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("digest go.sum: %w", err)
	}
	packageLock, err := fileSHA256(filepath.Join(snapshotPath, "frontend-app", "package-lock.json"))
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("digest package-lock.json: %w", err)
	}
	vendor, err := RuntimeSeedTreeDigest(filepath.Join(runtimePath, "vendor"))
	if err != nil {
		return RuntimeSeedManifest{}, err
	}
	nodeModules, err := RuntimeSeedTreeDigest(filepath.Join(runtimePath, "frontend", "node_modules"))
	if err != nil {
		return RuntimeSeedManifest{}, err
	}
	return RuntimeSeedManifest{
		SchemaVersion: RuntimeSeedSchemaVersion, GoSumSHA256: goSum, VendorTreeSHA256: vendor,
		PackageLockSHA256: packageLock, NodeModulesTreeSHA256: nodeModules,
	}, nil
}

// Validate 重算锁文件与种子树摘要，确认清单未漂移。
func (manifest RuntimeSeedManifest) Validate(snapshotRoot string, runtimeRoot string) error {
	if err := manifest.validateShape(); err != nil {
		return err
	}
	actual, err := BuildRuntimeSeedManifest(snapshotRoot, runtimeRoot)
	if err != nil {
		return err
	}
	if actual != manifest {
		return errors.New("runtime seed manifest does not match snapshot or seed content")
	}
	return nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("runtime seed manifest has trailing JSON")
		}
		return fmt.Errorf("decode runtime seed manifest trailer: %w", err)
	}
	return nil
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

// installBoundSeed 校验锁摘要和种子树摘要后防覆盖复制依赖目录。
func installBoundSeed(boundFile string, expectedBoundDigest string, seedRoot string, expectedTreeDigest string, targetRoot string) error {
	boundDigest, err := fileSHA256(boundFile)
	if err != nil {
		return fmt.Errorf("digest bound source file: %w", err)
	}
	if boundDigest != expectedBoundDigest {
		return errors.New("runtime seed source lock digest does not match snapshot")
	}
	seedPath, err := trustedDirectory(seedRoot, false, -1)
	if err != nil {
		return fmt.Errorf("runtime seed directory: %w", err)
	}
	treeDigest, err := RuntimeSeedTreeDigest(seedPath)
	if err != nil {
		return err
	}
	if treeDigest != expectedTreeDigest {
		return errors.New("runtime seed tree digest does not match manifest")
	}
	if _, err := os.Lstat(targetRoot); !errors.Is(err, os.ErrNotExist) {
		return errors.New("runtime seed target already exists")
	}
	return copyRuntimeSeed(seedPath, targetRoot)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

// RuntimeSeedTreeDigest 计算镜像构建器与执行器共享的规范目录树摘要。
func RuntimeSeedTreeDigest(root string) (string, error) {
	resolvedRoot, err := trustedDirectory(root, false, -1)
	if err != nil {
		return "", fmt.Errorf("runtime seed tree root: %w", err)
	}
	digest := sha256.New()
	if err := writeSeedRecord(digest, 'V', []byte("super-dolphin-runtime-seed-tree"), []byte("1")); err != nil {
		return "", fmt.Errorf("initialize runtime seed tree digest: %w", err)
	}
	err = filepath.WalkDir(resolvedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(resolvedRoot, path)
		if err != nil || relative == "." {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		return hashSeedEntry(digest, resolvedRoot, relative, path, info)
	})
	if err != nil {
		return "", fmt.Errorf("digest runtime seed tree: %w", err)
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

// hashSeedEntry 把路径、类型、权限、内容或安全链接目标纳入树摘要。
func hashSeedEntry(digest hash.Hash, root string, relative string, path string, info fs.FileInfo) error {
	relative = filepath.ToSlash(relative)
	mode := make([]byte, 4)
	binary.BigEndian.PutUint32(mode, uint32(info.Mode().Perm()))
	switch {
	case info.IsDir():
		return writeSeedRecord(digest, 'D', []byte(relative), mode)
	case info.Mode().IsRegular():
		contentDigest, err := regularFileContentDigest(path, info.Size())
		if err != nil {
			return err
		}
		size := make([]byte, 8)
		binary.BigEndian.PutUint64(size, uint64(info.Size()))
		return writeSeedRecord(digest, 'F', []byte(relative), mode, size, contentDigest)
	case info.Mode()&os.ModeSymlink != 0:
		target, err := safeSeedSymlink(root, relative, path)
		if err != nil {
			return err
		}
		return writeSeedRecord(digest, 'L', []byte(relative), []byte(target))
	default:
		return fmt.Errorf("runtime seed entry %q has forbidden type", relative)
	}
}

// writeSeedRecord 用固定字段计数和长度前缀写入一个不可歧义的摘要记录。
func writeSeedRecord(digest hash.Hash, kind byte, fields ...[]byte) error {
	if digest == nil {
		return errors.New("runtime seed digest is required")
	}
	fieldCount := make([]byte, 4)
	binary.BigEndian.PutUint32(fieldCount, uint32(len(fields)))
	if _, err := digest.Write(append([]byte{kind}, fieldCount...)); err != nil {
		return err
	}
	for _, field := range fields {
		length := make([]byte, 8)
		binary.BigEndian.PutUint64(length, uint64(len(field)))
		if _, err := digest.Write(length); err != nil {
			return err
		}
		if _, err := digest.Write(field); err != nil {
			return err
		}
	}
	return nil
}

// regularFileContentDigest 计算独立内容摘要并拒绝哈希期间可见的大小漂移。
func regularFileContentDigest(path string, expectedSize int64) (_ []byte, retErr error) {
	if expectedSize < 0 {
		return nil, errors.New("runtime seed file size is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	digest := sha256.New()
	written, err := io.Copy(digest, file)
	if err != nil {
		return nil, err
	}
	if written != expectedSize {
		return nil, errors.New("runtime seed file size changed while hashing")
	}
	return digest.Sum(nil), nil
}

// safeSeedSymlink 仅允许最终解析结果仍位于种子根内的相对链接。
func safeSeedSymlink(root string, relative string, path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil || filepath.IsAbs(target) {
		return "", errors.New("runtime seed symlink target is invalid")
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(relative)), target))
	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return "", errors.New("runtime seed symlink escapes seed root")
	}
	resolvedTarget, err := filepath.EvalSymlinks(filepath.Join(root, resolved))
	if err != nil {
		return "", errors.New("runtime seed symlink target is missing")
	}
	if resolvedTarget != root && !pathContains(root, resolvedTarget) {
		return "", errors.New("runtime seed symlink chain escapes seed root")
	}
	return target, nil
}

// copyRuntimeSeed 防覆盖复制已校验的种子，并在最后恢复目录权限。
func copyRuntimeSeed(sourceRoot string, targetRoot string) error {
	if err := os.Mkdir(targetRoot, 0o700); err != nil {
		return fmt.Errorf("create runtime seed target: %w", err)
	}
	directories := []copiedDirectory{{path: targetRoot, mode: 0o700}}
	copier := runtimeSeedCopier{sourceRoot: sourceRoot, targetRoot: targetRoot, directories: &directories}
	err := filepath.WalkDir(sourceRoot, copier.copy)
	if err != nil {
		return fmt.Errorf("copy runtime seed: %w", err)
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index].path, directories[index].mode); err != nil {
			return err
		}
	}
	return nil
}

type runtimeSeedCopier struct {
	sourceRoot  string
	targetRoot  string
	directories *[]copiedDirectory
}

func (copier runtimeSeedCopier) copy(sourcePath string, _ fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	relative, err := filepath.Rel(copier.sourceRoot, sourcePath)
	if err != nil || relative == "." {
		return err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	return copier.copyEntry(sourcePath, relative, info)
}

// copyEntry 按条目类型复制目录、普通文件或已验证的内部链接。
func (copier runtimeSeedCopier) copyEntry(sourcePath string, relative string, info fs.FileInfo) error {
	targetPath := filepath.Join(copier.targetRoot, relative)
	switch {
	case info.IsDir():
		if err := os.Mkdir(targetPath, 0o700); err != nil {
			return err
		}
		*copier.directories = append(*copier.directories, copiedDirectory{path: targetPath, mode: info.Mode().Perm()})
		return nil
	case info.Mode().IsRegular():
		return copyRegularFile(sourcePath, targetPath, info.Mode().Perm())
	case info.Mode()&os.ModeSymlink != 0:
		target, err := safeSeedSymlink(copier.sourceRoot, filepath.ToSlash(relative), sourcePath)
		if err != nil {
			return err
		}
		return os.Symlink(target, targetPath)
	default:
		return errors.New("runtime seed contains a forbidden entry type")
	}
}
