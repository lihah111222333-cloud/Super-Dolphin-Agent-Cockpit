package gate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

type trustedSQLCSnapshot struct {
	source trustedTreeManifest
	git    trustedTreeManifest
}

type trustedTreeManifest map[string]trustedTreeEntry

type trustedTreeEntry struct {
	mode   fs.FileMode
	digest [sha256.Size]byte
	link   string
}

const (
	maxTrustedTreeEntries   = 100_000
	maxTrustedTreeFileBytes = 64 << 20
)

// captureTrustedSQLCSnapshot 在 candidate helper 运行前记录源码和 Git 元数据的可信真值。
func captureTrustedSQLCSnapshot(ctx context.Context, gitRunner sqlcconfig.GitRunner, sourceCopy string, units []sqlcconfig.Unit) (trustedSQLCSnapshot, error) {
	source, err := captureTrustedSQLCOutputs(ctx, sourceCopy, units)
	if err != nil {
		return trustedSQLCSnapshot{}, fmt.Errorf("capture trusted SQLC source snapshot: %w", err)
	}
	git, err := captureTrustedGitMetadata(ctx, gitRunner, sourceCopy)
	if err != nil {
		return trustedSQLCSnapshot{}, fmt.Errorf("capture trusted SQLC Git metadata snapshot: %w", err)
	}
	return trustedSQLCSnapshot{source: source, git: git}, nil
}

// verifyTrustedSQLCSnapshot 拒绝 candidate 对源码或 Git 元数据的任何增加、删除或修改。
func verifyTrustedSQLCSnapshot(ctx context.Context, gitRunner sqlcconfig.GitRunner, sourceCopy string, units []sqlcconfig.Unit, before trustedSQLCSnapshot) error {
	if err := verifyTrustedSQLCOutputsSnapshot(ctx, sourceCopy, units, before.source); err != nil {
		return err
	}
	return verifyTrustedGitSnapshot(ctx, gitRunner, sourceCopy, before.git)
}

func verifyTrustedSQLCOutputsSnapshot(ctx context.Context, sourceCopy string, units []sqlcconfig.Unit, before trustedTreeManifest) error {
	after, err := captureTrustedSQLCOutputs(ctx, sourceCopy, units)
	if err != nil {
		return fmt.Errorf("capture post-SQLC source snapshot: %w", err)
	}
	return compareTrustedTree(ctx, "source", before, after)
}

func verifyTrustedGitSnapshot(ctx context.Context, gitRunner sqlcconfig.GitRunner, sourceCopy string, before trustedTreeManifest) error {
	afterGit, err := captureTrustedGitMetadata(ctx, gitRunner, sourceCopy)
	if err != nil {
		return fmt.Errorf("capture post-SQLC Git metadata snapshot: %w", err)
	}
	return compareTrustedTree(ctx, "Git metadata", before, afterGit)
}

func captureTrustedSQLCOutputs(ctx context.Context, sourceCopy string, units []sqlcconfig.Unit) (trustedTreeManifest, error) {
	manifest := make(trustedTreeManifest)
	for _, unit := range units {
		if err := trustedManifestContextError(ctx); err != nil {
			return nil, err
		}
		root := filepath.Join(sourceCopy, filepath.FromSlash(unit.Output))
		tree, err := captureOptionalTrustedTree(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("capture configured output %s: %w", unit.Output, err)
		}
		for relative, item := range tree {
			manifest[filepath.ToSlash(filepath.Join(unit.Output, relative))] = item
		}
	}
	return manifest, nil
}

// captureTrustedGitMetadata 冻结 SQLC 执行依赖的仓库元数据，防止生成过程改写 Git 对象路由或索引状态。
func captureTrustedGitMetadata(ctx context.Context, gitRunner sqlcconfig.GitRunner, sourceCopy string) (trustedTreeManifest, error) {
	manifest := make(trustedTreeManifest)
	gitEntry := filepath.Join(sourceCopy, ".git")
	info, err := os.Lstat(gitEntry)
	if err != nil {
		return nil, fmt.Errorf("inspect Git entrypoint: %w", err)
	}
	item, err := trustedTreeItem(ctx, gitEntry, info)
	if err != nil {
		return nil, fmt.Errorf("capture Git entrypoint: %w", err)
	}
	manifest[".git"] = item
	for _, relative := range []string{"HEAD", "index", "config", "config.worktree", "packed-refs", "refs", "info/exclude", "objects/info/alternates"} {
		resolved, err := trustedGitPath(ctx, gitRunner, sourceCopy, relative)
		if err != nil {
			return nil, err
		}
		if relative == "objects/info/alternates" {
			if err := validateTrustedGitAlternates(sourceCopy, resolved); err != nil {
				return nil, err
			}
		}
		tree, err := captureOptionalTrustedTree(ctx, resolved)
		if err != nil {
			return nil, fmt.Errorf("capture Git metadata %s: %w", relative, err)
		}
		for child, item := range tree {
			manifest[filepath.ToSlash(filepath.Join(relative, child))] = item
		}
	}
	return manifest, nil
}

func validateTrustedGitAlternates(sourceCopy, alternatesPath string) error {
	objectRoot, present, err := readTrustedGitAlternate(alternatesPath)
	if err != nil || !present {
		return err
	}
	return validateTrustedGitAlternatePath(sourceCopy, objectRoot)
}

// readTrustedGitAlternate 读取单一且受限的对象库指向；缺失表示仓库不使用 alternates，而非错误。
func readTrustedGitAlternate(alternatesPath string) (string, bool, error) {
	info, err := os.Lstat(alternatesPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect trusted Git alternates: %w", err)
	}
	if err := validateTrustedGitAlternatesFile(info); err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(alternatesPath)
	if err != nil {
		return "", false, fmt.Errorf("read trusted Git alternates: %w", err)
	}
	objectRoot, err := parseTrustedGitAlternate(data)
	if err != nil {
		return "", false, err
	}
	return objectRoot, true, nil
}

// validateTrustedGitAlternatesFile 只接受物化器可发布且不可被共享写入的常规小文件。
func validateTrustedGitAlternatesFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("trusted Git alternates must be a bounded non-shared-writable regular file")
	}
	if permission := info.Mode().Perm(); permission != 0o600 && permission != 0o644 {
		return errors.New("trusted Git alternates must be a bounded non-shared-writable regular file")
	}
	if info.Size() <= 1 || info.Size() > 4096 {
		return errors.New("trusted Git alternates must be a bounded non-shared-writable regular file")
	}
	return nil
}

func parseTrustedGitAlternate(data []byte) (string, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return "", errors.New("trusted Git alternates must contain one canonical line")
	}
	if bytes.Count(data, []byte{'\n'}) != 1 || bytes.ContainsAny(data, "\x00\r") {
		return "", errors.New("trusted Git alternates must contain one canonical line")
	}
	return string(data[:len(data)-1]), nil
}

// validateTrustedGitAlternatePath 要求对象库为 source copy 外部的真实绝对目录，避免候选仓库重定向对象读取。
func validateTrustedGitAlternatePath(sourceCopy, objectRoot string) error {
	if !filepath.IsAbs(objectRoot) {
		return errors.New("trusted Git alternate object path must be absolute and clean")
	}
	if filepath.Clean(objectRoot) != objectRoot {
		return errors.New("trusted Git alternate object path must be absolute and clean")
	}
	resolved, err := filepath.EvalSymlinks(objectRoot)
	if err != nil {
		return errors.New("trusted Git alternate object path must exist without symlinks")
	}
	if resolved != objectRoot {
		return errors.New("trusted Git alternate object path must exist without symlinks")
	}
	objectInfo, err := os.Stat(objectRoot)
	if err != nil {
		return errors.New("trusted Git alternate object path must be a directory")
	}
	if !objectInfo.IsDir() {
		return errors.New("trusted Git alternate object path must be a directory")
	}
	relative, err := filepath.Rel(sourceCopy, objectRoot)
	if err != nil {
		return fmt.Errorf("compare trusted Git alternate path: %w", err)
	}
	if !trustedGitAlternateOutsideSource(relative) {
		return errors.New("trusted Git alternate object path must stay outside the writable source copy")
	}
	return nil
}

func trustedGitAlternateOutsideSource(relative string) bool {
	return relative != "." && relative != ".." && strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func trustedGitPath(ctx context.Context, gitRunner sqlcconfig.GitRunner, sourceCopy, relative string) (string, error) {
	output, err := gitRunner.Output(ctx, sourceCopy, "rev-parse", "--path-format=absolute", "--git-path", relative)
	if err != nil {
		return "", fmt.Errorf("resolve Git metadata %s: %w", relative, err)
	}
	resolved := strings.TrimSpace(string(output))
	if !filepath.IsAbs(resolved) || filepath.Clean(resolved) != resolved {
		return "", fmt.Errorf("Git metadata %s resolved to non-canonical path %q", relative, resolved)
	}
	return resolved, nil
}

// captureOptionalTrustedTree 对可选元数据目录做有界快照；不存在时返回空清单供前后精确比较。
func captureOptionalTrustedTree(ctx context.Context, root string) (trustedTreeManifest, error) {
	if err := trustedManifestContextError(ctx); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return trustedTreeManifest{".": {}}, nil
	}
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return captureTrustedTree(ctx, root, "")
	}
	item, err := trustedTreeItem(ctx, root, info)
	if err != nil {
		return nil, err
	}
	return trustedTreeManifest{".": item}, nil
}

func captureTrustedTree(ctx context.Context, root, excludedRootEntry string) (trustedTreeManifest, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("trusted manifest root is not a regular directory: %s", root)
	}
	manifest := make(trustedTreeManifest)
	walk := trustedTreeWalk{ctx: ctx, root: root, excludedRootEntry: excludedRootEntry, manifest: manifest}
	err = filepath.WalkDir(root, walk.visit)
	return manifest, err
}

type trustedTreeWalk struct {
	ctx               context.Context
	root              string
	excludedRootEntry string
	manifest          trustedTreeManifest
}

// visit 在有界 entry 数内逐项记录 manifest，并在每项前传播 worker 取消原因。
func (walk trustedTreeWalk) visit(current string, _ fs.DirEntry, walkErr error) error {
	if err := trustedManifestContextError(walk.ctx); err != nil {
		return err
	}
	if walkErr != nil {
		return walkErr
	}
	relative, err := filepath.Rel(walk.root, current)
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	if relative == walk.excludedRootEntry {
		return filepath.SkipDir
	}
	info, err := os.Lstat(current)
	if err != nil {
		return err
	}
	if len(walk.manifest) >= maxTrustedTreeEntries {
		return fmt.Errorf("trusted manifest exceeds %d entries", maxTrustedTreeEntries)
	}
	item, err := trustedTreeItem(walk.ctx, current, info)
	if err != nil {
		return fmt.Errorf("manifest %s: %w", relative, err)
	}
	walk.manifest[relative] = item
	return nil
}

// trustedTreeItem 将常规文件或符号链接编码为稳定条目，并拒绝超限及特殊文件。
func trustedTreeItem(ctx context.Context, path string, info fs.FileInfo) (trustedTreeEntry, error) {
	if err := trustedManifestContextError(ctx); err != nil {
		return trustedTreeEntry{}, err
	}
	item := trustedTreeEntry{mode: info.Mode()}
	switch {
	case info.IsDir():
		return item, nil
	case info.Mode().IsRegular():
		if info.Size() > maxTrustedTreeFileBytes {
			return trustedTreeEntry{}, fmt.Errorf("trusted manifest file exceeds %d bytes", maxTrustedTreeFileBytes)
		}
		file, err := os.Open(path)
		if err != nil {
			return trustedTreeEntry{}, err
		}
		hash := sha256.New()
		copied, copyErr := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, reader: file}, maxTrustedTreeFileBytes+1))
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return trustedTreeEntry{}, err
		}
		if copied > maxTrustedTreeFileBytes {
			return trustedTreeEntry{}, fmt.Errorf("trusted manifest file exceeds %d bytes", maxTrustedTreeFileBytes)
		}
		copy(item.digest[:], hash.Sum(nil))
		return item, nil
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(path)
		if err != nil {
			return trustedTreeEntry{}, err
		}
		item.link = link
		return item, nil
	default:
		return trustedTreeEntry{}, fmt.Errorf("unsupported file type %s", info.Mode().Type())
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read 在每次文件读取前检查 worker 取消原因，避免 manifest hash 越过 deadline。
func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := trustedManifestContextError(reader.ctx); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func trustedManifestContextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("trusted manifest context is required")
	}
	if ctx.Err() == nil {
		return nil
	}
	return errors.Join(ctx.Err(), context.Cause(ctx))
}

// compareTrustedTree 对前后清单执行精确比较，任何增删、权限或内容漂移都阻断可信执行。
func compareTrustedTree(ctx context.Context, label string, before, after trustedTreeManifest) error {
	if err := trustedManifestContextError(ctx); err != nil {
		return err
	}
	paths := make([]string, 0, len(before)+len(after))
	seen := make(map[string]bool, len(before)+len(after))
	for path := range before {
		paths = append(paths, path)
		seen[path] = true
	}
	for path := range after {
		if !seen[path] {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := trustedManifestContextError(ctx); err != nil {
			return err
		}
		beforeEntry, beforeOK := before[path]
		afterEntry, afterOK := after[path]
		if !beforeOK {
			return fmt.Errorf("trusted SQLC %s snapshot gained %s", label, path)
		}
		if !afterOK {
			return fmt.Errorf("trusted SQLC %s snapshot lost %s", label, path)
		}
		if beforeEntry != afterEntry {
			return fmt.Errorf("trusted SQLC %s snapshot changed %s", label, path)
		}
	}
	return nil
}
