package appupdaterecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const releaseDigestChunkSize = 128 << 10

type releaseDigestOps struct {
	readChunk func(context.Context, io.Reader, []byte) (int, error)
}

// ComputeReleaseDigest 计算文件或目录 release 的确定性 SHA-256。
func ComputeReleaseDigest(path string) (string, error) {
	return ComputeReleaseDigestContext(context.Background(), path)
}

// ComputeReleaseDigestContext 在目录遍历、条目处理和文件分块边界响应取消。
func ComputeReleaseDigestContext(ctx context.Context, path string) (string, error) {
	return computeReleaseDigestContextWithOps(ctx, path, releaseDigestOps{
		readChunk: func(_ context.Context, reader io.Reader, buffer []byte) (int, error) {
			return reader.Read(buffer)
		},
	})
}

// computeReleaseDigestContextWithOps 校验依赖后分派单文件或目录摘要。
func computeReleaseDigestContextWithOps(ctx context.Context, path string, ops releaseDigestOps) (string, error) {
	if ctx == nil {
		return "", errors.New("release digest context is required")
	}
	if ops.readChunk == nil {
		return "", errors.New("release digest chunk reader is required")
	}
	info, err := inspectReleaseDigestRoot(ctx, path)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	if info.Mode().IsRegular() {
		if err := hashRegularFileContext(ctx, hasher, path, ops); err != nil {
			return "", err
		}
		return hex.EncodeToString(hasher.Sum(nil)), nil
	}
	if !info.IsDir() {
		return "", fmt.Errorf("release path %s must be a regular file or directory", path)
	}
	entries, err := collectReleaseDigestEntries(ctx, path)
	if err != nil {
		return "", err
	}
	if err := hashReleaseEntriesContext(ctx, hasher, path, entries, ops); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func inspectReleaseDigestRoot(ctx context.Context, path string) (fs.FileInfo, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect release path %s: %w", path, err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return info, nil
}

// collectReleaseDigestEntries 在 WalkDir callback 内响应取消并稳定排序。
func collectReleaseDigestEntries(ctx context.Context, path string) ([]string, error) {
	entries := make([]string, 0, 64)
	if err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if current != path {
			entries = append(entries, current)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk release path %s: %w", path, err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}

func hashReleaseEntriesContext(ctx context.Context, hasher hash.Hash, root string, entries []string, ops releaseDigestOps) error {
	for _, entryPath := range entries {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		if err := hashReleaseEntryContext(ctx, hasher, root, entryPath, ops); err != nil {
			return err
		}
	}
	return nil
}

// hashReleaseEntryContext 在单个条目的每个文件系统操作前后检查取消。
func hashReleaseEntryContext(ctx context.Context, hasher hash.Hash, root string, path string, ops releaseDigestOps) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect release entry %s: %w", path, err)
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("resolve release entry %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(hasher, "%s\x00%s\x00", filepath.ToSlash(relative), info.Mode().String()); err != nil {
		return fmt.Errorf("hash release entry metadata %s: %w", path, err)
	}
	if info.Mode().IsRegular() {
		return hashRegularFileContext(ctx, hasher, path, ops)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return hashReleaseSymlinkContext(ctx, hasher, path)
	}
	return nil
}

func hashReleaseSymlinkContext(ctx context.Context, hasher hash.Hash, path string) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("read release symlink %s: %w", path, err)
	}
	if _, err := io.WriteString(hasher, target); err != nil {
		return fmt.Errorf("hash release symlink %s: %w", path, err)
	}
	return nil
}

// hashRegularFileContext 打开并关闭文件，分块循环由独立函数负责。
func hashRegularFileContext(ctx context.Context, hasher hash.Hash, path string, ops releaseDigestOps) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open release file %s: %w", path, err)
	}
	copyErr := copyReleaseFileContext(ctx, hasher, file, ops)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("hash release file %s: %w", path, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close release file %s: %w", path, closeErr)
	}
	return nil
}

func copyReleaseFileContext(ctx context.Context, hasher hash.Hash, file io.Reader, ops releaseDigestOps) error {
	buffer := make([]byte, releaseDigestChunkSize)
	for {
		done, err := hashNextReleaseChunk(ctx, hasher, file, buffer, ops)
		if err != nil || done {
			return err
		}
	}
}

// hashNextReleaseChunk 在单次读取前后检查取消并校验 reader 契约。
func hashNextReleaseChunk(ctx context.Context, hasher hash.Hash, file io.Reader, buffer []byte, ops releaseDigestOps) (bool, error) {
	if err := context.Cause(ctx); err != nil {
		return false, err
	}
	read, readErr := ops.readChunk(ctx, file, buffer)
	if err := context.Cause(ctx); err != nil {
		return false, err
	}
	if read < 0 || read > len(buffer) {
		return false, errors.New("release digest chunk reader returned invalid byte count")
	}
	if read > 0 {
		if _, err := hasher.Write(buffer[:read]); err != nil {
			return false, err
		}
	}
	if errors.Is(readErr, io.EOF) {
		return true, nil
	}
	if readErr != nil {
		return false, readErr
	}
	if read == 0 {
		return false, io.ErrNoProgress
	}
	return false, nil
}

// syncRelease 将 candidate 的文件内容和目录项按自底向上顺序持久化。
func syncRelease(path string) error {
	files, directories, err := releaseSyncPaths(path)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := syncRegularFile(file); err != nil {
			return err
		}
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := syncDirectory(directories[index]); err != nil {
			return err
		}
	}
	return nil
}

// releaseSyncPaths 枚举需要 fsync 的 regular files 和目录。
func releaseSyncPaths(root string) ([]string, []string, error) {
	var files []string
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files = append(files, path)
		} else if info.IsDir() {
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("enumerate release for fsync %s: %w", root, err)
	}
	if len(files) == 0 && len(directories) == 0 {
		return nil, nil, fmt.Errorf("release path cannot be synchronized: %s", root)
	}
	return files, directories, nil
}

func syncRegularFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open release file for fsync %s: %w", path, err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("fsync release file %s: %w", path, err)
	}
	return nil
}

func verifyRelease(path string, identity ReleaseIdentity) error {
	digest, err := ComputeReleaseDigest(path)
	if err != nil {
		return err
	}
	if digest != identity.SHA256 {
		return fmt.Errorf("release digest at %s = %s, want %s", path, digest, identity.SHA256)
	}
	return nil
}
