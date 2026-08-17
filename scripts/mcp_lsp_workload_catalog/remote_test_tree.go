package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// LoadRemoteTestPackageFilesAt 从已解析的 Git tree blob 读取 parser 所需的测试源码。
func LoadRemoteTestPackageFilesAt(repoRoot, tree string, command RemoteTestCommand) ([]RemoteTestPackageFiles, error) {
	entries, err := readRemoteTestTreeEntries(repoRoot, tree)
	if err != nil {
		return nil, err
	}
	packages := make([]RemoteTestPackageFiles, 0, len(command.Packages))
	for _, packagePath := range command.Packages {
		directory := strings.TrimPrefix(packagePath, "./")
		files := make([]RemoteTestFile, 0)
		for _, entry := range entries {
			if path.Dir(entry.Path) != directory || !strings.HasSuffix(entry.Path, "_test.go") {
				continue
			}
			if entry.Mode != "100644" && entry.Mode != "100755" {
				return nil, fmt.Errorf("remote test tree file %q is not a regular blob", entry.Path)
			}
			source, err := readRemoteTestTreeBlob(repoRoot, tree, entry.Path)
			if err != nil {
				return nil, fmt.Errorf("read exact tree test file %q: %w", entry.Path, err)
			}
			files = append(files, RemoteTestFile{Path: entry.Path, Name: path.Base(entry.Path), Source: source})
		}
		slices.SortFunc(files, func(left, right RemoteTestFile) int { return strings.Compare(left.Path, right.Path) })
		packages = append(packages, RemoteTestPackageFiles{Package: packagePath, Files: files})
	}
	return packages, nil
}

// ReadTreeFileAt 从 exact Git tree 读取普通 tracked 文件，绝不读取 mutable worktree。
func ReadTreeFileAt(repoRoot, tree, relative string) ([]byte, error) {
	if !validRemoteTestRelativePath(relative) {
		return nil, fmt.Errorf("exact tree file path %q is invalid", relative)
	}
	entries, err := readRemoteTestTreeEntries(repoRoot, tree)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Path != relative {
			continue
		}
		if entry.Mode != "100644" && entry.Mode != "100755" {
			return nil, fmt.Errorf("exact tree file %q is not a regular blob", relative)
		}
		return readRemoteTestTreeBlob(repoRoot, tree, relative)
	}
	return nil, fmt.Errorf("exact tree file %q is unavailable", relative)
}

type remoteTestTreeEntry struct {
	Mode string
	Path string
}

// ListTreePathsAt 从 resolved candidate tree 返回全部普通 blob 路径，不读取 mutable worktree。
func ListTreePathsAt(repoRoot, tree string) ([]string, error) {
	entries, err := readRemoteTestTreeEntries(repoRoot, tree)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Mode != "100644" && entry.Mode != "100755" {
			continue
		}
		paths = append(paths, entry.Path)
	}
	return paths, nil
}

// readRemoteTestTreeEntries 严格枚举 resolved tree 的 blob entries。
func readRemoteTestTreeEntries(repoRoot, tree string) ([]remoteTestTreeEntry, error) {
	if !filepath.IsAbs(repoRoot) || strings.TrimSpace(repoRoot) == "" {
		return nil, errors.New("exact tree repository root must be absolute")
	}
	if !validGitOID(tree) {
		return nil, errors.New("exact tree identity must be a resolved Git object ID")
	}
	typeOutput, err := runRemoteTestGit(repoRoot, "cat-file", "-t", "--end-of-options", tree)
	if err != nil {
		return nil, fmt.Errorf("inspect exact tree %q: %w", tree, err)
	}
	if typeOutput != "tree" {
		return nil, fmt.Errorf("exact tree identity %q resolves to %q, want tree", tree, typeOutput)
	}
	output, err := runRemoteTestGitBytes(repoRoot, "ls-tree", "-r", "-z", "--full-tree", "--end-of-options", tree)
	if err != nil {
		return nil, fmt.Errorf("list exact tree %q: %w", tree, err)
	}
	entries := make([]remoteTestTreeEntry, 0)
	for record := range bytes.SplitSeq(output, []byte{0}) {
		entry, empty, err := parseRemoteTestTreeEntry(record)
		if err != nil {
			return nil, err
		}
		if !empty {
			entries = append(entries, entry)
		}
	}
	slices.SortFunc(entries, func(left, right remoteTestTreeEntry) int { return strings.Compare(left.Path, right.Path) })
	return entries, nil
}

// parseRemoteTestTreeEntry 严格解析一条 NUL 分隔的 tree blob 记录。
func parseRemoteTestTreeEntry(record []byte) (remoteTestTreeEntry, bool, error) {
	if len(record) == 0 {
		return remoteTestTreeEntry{}, true, nil
	}
	header, filePath, ok := strings.Cut(string(record), "\t")
	fields := strings.Fields(header)
	if !ok || len(fields) != 3 || fields[1] != "blob" || !validRemoteTestRelativePath(filePath) || !validGitOID(fields[2]) {
		return remoteTestTreeEntry{}, false, errors.New("exact tree test entry is malformed")
	}
	return remoteTestTreeEntry{Mode: fields[0], Path: filePath}, false, nil
}

func readRemoteTestTreeBlob(repoRoot, tree, relative string) ([]byte, error) {
	return runRemoteTestGitBytes(repoRoot, "show", "--format=", "--end-of-options", tree+":"+relative)
}

func runRemoteTestGit(repoRoot string, args ...string) (string, error) {
	output, err := runRemoteTestGitBytes(repoRoot, args...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", errors.New("Git returned an empty value")
	}
	return value, nil
}

func runRemoteTestGitBytes(repoRoot string, args ...string) ([]byte, error) {
	output, err := BoundedGitOutput(repoRoot, args...)
	if err != nil {
		return nil, err
	}
	return output, nil
}

func validRemoteTestRelativePath(value string) bool {
	return value != "" && !path.IsAbs(value) && path.Clean(value) == value && !strings.HasPrefix(value, "../") && !strings.ContainsAny(value, "\\:\x00\r\n\t")
}

// ProjectRemoteTestCommand 将适用声明解析为有序且唯一的 package+Test pair。
