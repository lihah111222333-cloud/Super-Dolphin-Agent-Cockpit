package appupdaterecovery

import (
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

// ComputeReleaseDigest 计算文件或目录 release 的确定性 SHA-256。
func ComputeReleaseDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect release path %s: %w", path, err)
	}
	hasher := sha256.New()
	if info.Mode().IsRegular() {
		if err := hashRegularFile(hasher, path); err != nil {
			return "", err
		}
		return hex.EncodeToString(hasher.Sum(nil)), nil
	}
	if !info.IsDir() {
		return "", fmt.Errorf("release path %s must be a regular file or directory", path)
	}
	entries := make([]string, 0, 64)
	if err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current != path {
			entries = append(entries, current)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("walk release path %s: %w", path, err)
	}
	sort.Strings(entries)
	for _, entryPath := range entries {
		if err := hashReleaseEntry(hasher, path, entryPath); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// hashReleaseEntry 将相对路径、模式、链接目标或文件内容写入 release hash。
func hashReleaseEntry(hasher hash.Hash, root string, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect release entry %s: %w", path, err)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("resolve release entry %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(hasher, "%s\x00%s\x00", filepath.ToSlash(relative), info.Mode().String()); err != nil {
		return fmt.Errorf("hash release entry metadata %s: %w", path, err)
	}
	if info.Mode().IsRegular() {
		return hashRegularFile(hasher, path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read release symlink %s: %w", path, err)
		}
		if _, err := io.WriteString(hasher, target); err != nil {
			return fmt.Errorf("hash release symlink %s: %w", path, err)
		}
	}
	return nil
}

func hashRegularFile(hasher hash.Hash, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open release file %s: %w", path, err)
	}
	_, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("hash release file %s: %w", path, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close release file %s: %w", path, closeErr)
	}
	return nil
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
