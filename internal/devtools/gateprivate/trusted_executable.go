package gateprivate

import (
	"fmt"
	"os"
	"path/filepath"
)

type trustedExecutableOwner func(os.FileInfo) bool
type trustedDirectoryPermissions func(os.FileInfo) bool

// CanonicalRootExecutable 校验可执行文件由 root 持有，且路径祖先不可被调用者替换。
func CanonicalRootExecutable(name, path string) (string, error) {
	return canonicalOwnedExecutable(
		name, path, "root", trustedExecutableOwnedByRoot, trustedRootDirectoryPermissions,
	)
}

// CanonicalCurrentOrRootExecutable 校验可执行文件由当前用户或 root 持有，且路径祖先具有同等所有权。
func CanonicalCurrentOrRootExecutable(name, path string) (string, error) {
	return canonicalOwnedExecutable(
		name, path, "current user or root",
		trustedExecutableOwnedByCurrentOrRoot, trustedCurrentOrRootDirectoryPermissions,
	)
}

func canonicalOwnedExecutable(
	name, path, ownerDescription string,
	owner trustedExecutableOwner,
	directoryPermissions trustedDirectoryPermissions,
) (string, error) {
	resolved, err := canonicalExecutablePath(name, path)
	if err != nil {
		return "", err
	}
	if err := validateOwnedExecutableFile(name, resolved, ownerDescription, owner); err != nil {
		return "", err
	}
	if err := validateOwnedPathAncestors(
		name, filepath.Dir(resolved), ownerDescription, owner, directoryPermissions,
	); err != nil {
		if !trustedExecutableOnReadOnlyMount(resolved) {
			return "", err
		}
	}
	return resolved, nil
}

func canonicalExecutablePath(name, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s path must be absolute: %q", name, path)
	}
	if filepath.Clean(path) != path {
		return "", fmt.Errorf("%s path must be canonical: %q", name, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s path %q: %w", name, path, err)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("%s resolved path must be absolute: %q", name, resolved)
	}
	if filepath.Clean(resolved) != resolved {
		return "", fmt.Errorf("%s resolved path must be canonical: %q", name, resolved)
	}
	return resolved, nil
}

func validateOwnedExecutableFile(
	name, path, ownerDescription string,
	owner trustedExecutableOwner,
) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s path %q: %w", name, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s path is not a regular file: %q", name, path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s resolved path is a symlink: %q", name, path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s path is group- or other-writable: %q", name, path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s path is not executable: %q", name, path)
	}
	if !owner(info) {
		return fmt.Errorf("%s must be owned by %s: %q", name, ownerDescription, path)
	}
	return nil
}

func validateOwnedPathAncestors(
	name, start, ownerDescription string,
	owner trustedExecutableOwner,
	directoryPermissions trustedDirectoryPermissions,
) error {
	for directory := start; ; directory = filepath.Dir(directory) {
		if err := validateOwnedPathDirectory(
			name, directory, ownerDescription, owner, directoryPermissions,
		); err != nil {
			return err
		}
		if directory == filepath.Dir(directory) {
			return nil
		}
	}
}

func validateOwnedPathDirectory(
	name, directory, ownerDescription string,
	owner trustedExecutableOwner,
	directoryPermissions trustedDirectoryPermissions,
) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect %s parent %q: %w", name, directory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s parent is not a directory: %q", name, directory)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s parent is a symlink: %q", name, directory)
	}
	if !owner(info) {
		return fmt.Errorf("%s parent is not owned by %s: %q", name, ownerDescription, directory)
	}
	if !directoryPermissions(info) {
		return fmt.Errorf("%s parent permissions are unsafe: %q", name, directory)
	}
	return nil
}

func trustedRootDirectoryPermissions(info os.FileInfo) bool {
	return info.Mode().Perm()&0o022 == 0 || info.Mode()&os.ModeSticky != 0
}

func trustedCurrentOrRootDirectoryPermissions(info os.FileInfo) bool {
	if info.Mode().Perm()&0o002 != 0 {
		return false
	}
	return info.Mode().Perm()&0o020 == 0 || trustedExecutableOwnedByCurrent(info)
}
