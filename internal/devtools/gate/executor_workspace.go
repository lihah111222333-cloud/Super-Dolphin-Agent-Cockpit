package gate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// prepareExecutorWorkspace 验证挂载边界并创建一次性可写源码副本与缓存目录。
func prepareExecutorWorkspace(config executorConfig) (executorLayout, error) {
	if err := validateExecutorDirectories(config); err != nil {
		return executorLayout{}, err
	}
	layout := newExecutorLayout(config.workRoot)
	if err := os.Mkdir(layout.runRoot, 0o700); err != nil {
		return executorLayout{}, fmt.Errorf("create executor run root: %w", err)
	}
	if err := copySourceSnapshot(config.sourcePath, layout.sourceCopy); err != nil {
		return executorLayout{}, errors.Join(err, cleanupExecutorWorkspace(layout))
	}
	for _, directory := range []string{layout.home, layout.tmp, layout.goCache, layout.goModCache, layout.npmCache, layout.xdgCache} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return executorLayout{}, errors.Join(fmt.Errorf("create executor directory: %w", err), cleanupExecutorWorkspace(layout))
		}
	}
	if err := os.Mkdir(filepath.Join(layout.home, ".codex"), 0o700); err != nil {
		return executorLayout{}, errors.Join(fmt.Errorf("create executor Codex home: %w", err), cleanupExecutorWorkspace(layout))
	}
	return layout, nil
}

func newExecutorLayout(workRoot string) executorLayout {
	runRoot := filepath.Join(workRoot, "run")
	return executorLayout{
		workRoot: workRoot, runRoot: runRoot, sourceCopy: filepath.Join(runRoot, "source"),
		home: filepath.Join(workRoot, "home"), tmp: filepath.Join(workRoot, "tmp"),
		goCache: filepath.Join(workRoot, "go-cache"), goModCache: filepath.Join(workRoot, "go-mod-cache"),
		npmCache: filepath.Join(workRoot, "npm-cache"), xdgCache: filepath.Join(workRoot, "xdg-cache"),
	}
}

// validateExecutorDirectories 要求 source、work 均为可信实目录且彼此不嵌套。
func validateExecutorDirectories(config executorConfig) error {
	sourcePath, err := trustedDirectory(config.sourcePath, false, -1)
	if err != nil {
		return fmt.Errorf("source snapshot: %w", err)
	}
	workRoot, err := trustedDirectory(config.workRoot, true, config.expectedUID)
	if err != nil {
		return fmt.Errorf("executor work root: %w", err)
	}
	if sourcePath == workRoot || pathContains(sourcePath, workRoot) || pathContains(workRoot, sourcePath) {
		return errors.New("source snapshot and work root must be disjoint")
	}
	if config.requireReadOnlySource {
		if err := validateReadOnlyMount(sourcePath); err != nil {
			return fmt.Errorf("source snapshot mount: %w", err)
		}
	}
	return nil
}

// trustedDirectory 校验规范实目录、所有权权限以及按需的空目录条件。
func trustedDirectory(path string, requireEmpty bool, expectedUID int) (string, error) {
	resolved, info, err := canonicalRealDirectory(path)
	if err != nil {
		return "", err
	}
	if expectedUID >= 0 {
		if err := validateDirectoryOwner(info, expectedUID); err != nil {
			return "", err
		}
	}
	if requireEmpty {
		if err := validateEmptyDirectory(path); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

// canonicalRealDirectory 拒绝非规范路径、根链接以及任意父路径链接。
func canonicalRealDirectory(path string) (string, fs.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", nil, errors.New("path must be canonical and absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("path must be a real directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", nil, errors.New("path contains a symlink")
	}
	return resolved, info, nil
}

func validateDirectoryOwner(info fs.FileInfo, expectedUID int) error {
	ownerUID, ok := fileOwnerUID(info)
	if !ok || ownerUID != expectedUID {
		return errors.New("directory owner does not match executor uid")
	}
	if info.Mode().Perm()&0o700 != 0o700 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("directory permissions must be owner-only rwx")
	}
	return nil
}

func validateEmptyDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		return errors.New("directory must be readable and empty")
	}
	return nil
}

func pathContains(parent string, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// copySourceSnapshot 完整复制可信 Git 快照并拒绝链接和特殊文件。
func copySourceSnapshot(sourceRoot string, targetRoot string) error {
	if err := os.Mkdir(targetRoot, 0o700); err != nil {
		return fmt.Errorf("create writable source copy: %w", err)
	}
	directories := []copiedDirectory{{path: targetRoot, mode: 0o700}}
	copier := sourceSnapshotCopier{sourceRoot: sourceRoot, targetRoot: targetRoot, directories: &directories}
	err := filepath.WalkDir(sourceRoot, copier.copy)
	if err != nil {
		return fmt.Errorf("copy source snapshot: %w", err)
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index].path, directories[index].mode); err != nil {
			return fmt.Errorf("preserve source directory permissions: %w", err)
		}
	}
	return nil
}

type sourceSnapshotCopier struct {
	sourceRoot  string
	targetRoot  string
	directories *[]copiedDirectory
}

// copy 校验快照条目路径与类型后复制一个遍历项。
func (copier sourceSnapshotCopier) copy(sourcePath string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	relative, err := filepath.Rel(copier.sourceRoot, sourcePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("source entry escapes snapshot root")
	}
	if relative == "." {
		return nil
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("source symlink is forbidden: %s", relative)
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	return copier.copyEntry(sourcePath, relative, info)
}

func (copier sourceSnapshotCopier) copyEntry(sourcePath string, relative string, info fs.FileInfo) error {
	targetPath := filepath.Join(copier.targetRoot, relative)
	if info.IsDir() {
		if err := os.Mkdir(targetPath, 0o700); err != nil {
			return err
		}
		*copier.directories = append(*copier.directories, copiedDirectory{path: targetPath, mode: info.Mode().Perm()})
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source entry is not a regular file: %s", relative)
	}
	return copyRegularFile(sourcePath, targetPath, info.Mode().Perm())
}

type copiedDirectory struct {
	path string
	mode fs.FileMode
}

func copyRegularFile(sourcePath string, targetPath string, mode fs.FileMode) (retErr error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, source.Close()) }()
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, target.Close()) }()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return os.Chmod(targetPath, mode)
}

func cleanupExecutorWorkspace(layout executorLayout) error {
	if layout.workRoot == "" || layout.runRoot == "" {
		return errors.New("executor workspace layout is empty")
	}
	var cleanupErr error
	for _, path := range []string{layout.runRoot, layout.home, layout.tmp, layout.goCache, layout.goModCache, layout.npmCache, layout.xdgCache} {
		if err := removeExecutorWorkspacePath(path); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove executor workspace path %q: %w", path, err))
		}
	}
	return cleanupErr
}

// removeExecutorWorkspacePath 恢复私有缓存目录的所有者写权限后严格删除该路径。
func removeExecutorWorkspacePath(path string) error {
	if err := makeExecutorDirectoriesRemovable(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

// makeExecutorDirectoriesRemovable 只修改目录权限，不跟随缓存中的符号链接。
func makeExecutorDirectoriesRemovable(root string) error {
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()|0o700)
	})
}
