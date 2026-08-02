package remoteci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const baselineBuildInputManifestSchemaVersion = "1"

// loadBaselineBuildInputManifest 只为已验收基线读取当前或历史构建输入清单。
func loadBaselineBuildInputManifest(entries map[string]sourceexport.TreeEntry) (buildInputManifest, error) {
	entry, exists := entries[buildInputManifestPath]
	if !exists {
		return buildInputManifest{}, fmt.Errorf("candidate source is missing %s", buildInputManifestPath)
	}
	var manifest buildInputManifest
	if err := decodeStrictJSON(entry.Data, &manifest); err != nil {
		return buildInputManifest{}, fmt.Errorf("decode build input manifest: %w", err)
	}
	if manifest.SchemaVersion == buildInputManifestSchemaVersion {
		return manifest, validateBuildInputManifest(manifest)
	}
	if manifest.SchemaVersion != baselineBuildInputManifestSchemaVersion {
		return buildInputManifest{}, fmt.Errorf("build input manifest schema version %q is unsupported", manifest.SchemaVersion)
	}
	if len(manifest.GateCompileInputs) != 0 {
		return buildInputManifest{}, errors.New("schema 1 build input manifest declares gate compile inputs")
	}
	if err := validateContextPath(manifest.Dockerfile, make(map[string]string)); err != nil {
		return buildInputManifest{}, fmt.Errorf("manifest Dockerfile: %w", err)
	}
	if err := validateManifestInputs(manifest); err != nil {
		return buildInputManifest{}, err
	}
	for _, input := range manifest.Inputs {
		if strings.ContainsAny(input, "*?[") {
			return buildInputManifest{}, fmt.Errorf("schema 1 build input %q must be an exact path", input)
		}
	}
	manifest.GateCompileInputs = slices.Clone(manifest.Inputs)
	return manifest, validateGateCompileInputs(manifest)
}

// LoadGateCLICompileClosure 从精确 Git tree 加载 gate CLI 自更新所需的编译闭包。
func LoadGateCLICompileClosure(
	ctx context.Context,
	repoRoot string,
	treeOID string,
) (sourceDigest string, toolchainDigest string, entries []sourceexport.TreeEntry, err error) {
	if err := errors.Join(validateContext(ctx), validateCanonicalDirectory(repoRoot, false)); err != nil {
		return "", "", nil, fmt.Errorf("validate gate CLI compile closure input: %w", err)
	}
	if !gitObjectPattern.MatchString(treeOID) {
		return "", "", nil, errors.New("gate CLI compile closure tree must be a canonical Git object ID")
	}
	if err := verifyReadOnlyTreeObject(ctx, repoRoot, treeOID); err != nil {
		return "", "", nil, err
	}

	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repoRoot, treeOID)
	if err != nil {
		return "", "", nil, fmt.Errorf("load gate CLI compile tree: %w", err)
	}
	paths, err := snapshot.gateCLICompileClosurePaths(ctx)
	if err != nil {
		return "", "", nil, err
	}
	loaded, err := loadReadOnlyTreePaths(ctx, repoRoot, treeOID, paths)
	if err != nil {
		return "", "", nil, err
	}
	byPath := make(map[string]sourceexport.TreeEntry, len(loaded))
	for _, entry := range loaded {
		byPath[entry.Path] = entry
	}
	toolchain, exists := byPath[toolchainLockPath]
	if !exists {
		return "", "", nil, fmt.Errorf("gate CLI compile closure is missing %s", toolchainLockPath)
	}
	canonical, err := canonicalContextDigests(loaded)
	if err != nil {
		return "", "", nil, fmt.Errorf("build canonical gate CLI compile context: %w", err)
	}
	return canonical.ContextDigest, bytesDigest(toolchain.Data), cloneTreeEntries(loaded), nil
}

// gateCLICompileClosurePaths 由实际 Go 导入图而不是可漂移的 build manifest 推导 gate 编译输入。
func (snapshot *remoteGitTreeSnapshot) gateCLICompileClosurePaths(ctx context.Context) ([]string, error) {
	if snapshot == nil {
		return nil, errors.New("gate CLI compile tree snapshot is required")
	}
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return nil, fmt.Errorf("load gate CLI compile Go sources: %w", err)
	}
	selected := map[string]struct{}{
		"go.mod": {}, "go.sum": {}, toolchainLockPath: {},
	}
	pending := []string{"cmd/super-dolphin-gate"}
	seenDirectories := make(map[string]struct{})
	for len(pending) != 0 {
		directory := pending[0]
		pending = pending[1:]
		if _, seen := seenDirectories[directory]; seen {
			continue
		}
		seenDirectories[directory] = struct{}{}
		files, err := snapshot.gateCLICompilePackageFiles(directory)
		if err != nil {
			return nil, err
		}
		for _, filePath := range files {
			selected[filePath] = struct{}{}
		}
		imports, err := snapshot.gateCLICompileLocalImports(files)
		if err != nil {
			return nil, err
		}
		pending = append(pending, imports...)
	}
	paths := make([]string, 0, len(selected))
	for filePath := range selected {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return paths, nil
}

// gateCLICompilePackageFiles 选择会参与 go build 的生产 Go 源，测试文件不得影响编译身份。
func (snapshot *remoteGitTreeSnapshot) gateCLICompilePackageFiles(directory string) ([]string, error) {
	files := make([]string, 0)
	for filePath := range snapshot.goSources {
		if path.Dir(filePath) != directory || path.Ext(filePath) != ".go" || strings.HasSuffix(filePath, "_test.go") {
			continue
		}
		files = append(files, filePath)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("gate CLI compile package %q has no production Go files", directory)
	}
	sort.Strings(files)
	return files, nil
}

// gateCLICompileLocalImports 仅接受候选 tree 中由 Go import 实际抵达的本地包。
func (snapshot *remoteGitTreeSnapshot) gateCLICompileLocalImports(files []string) ([]string, error) {
	imports := make(map[string]struct{})
	for _, filePath := range files {
		source, exists := snapshot.goSources[filePath]
		if !exists {
			return nil, fmt.Errorf("gate CLI compile source %q is unavailable", filePath)
		}
		file, err := parser.ParseFile(token.NewFileSet(), filePath, source, parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("parse gate CLI compile source %q: %w", filePath, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("parse gate CLI compile import in %q: %w", filePath, err)
			}
			if local, ok := snapshot.resolveLocalGoImport(importPath); ok {
				imports[local] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(imports))
	for directory := range imports {
		result = append(result, directory)
	}
	sort.Strings(result)
	return result, nil
}

// verifyReadOnlyTreeObject 确认调用方指定的是可读取的 tree object，而不是 ref 或其他对象。
func verifyReadOnlyTreeObject(ctx context.Context, repoRoot string, treeOID string) error {
	output, err := runGitOutput(ctx, repoRoot, nil, "cat-file", "-t", treeOID)
	if err != nil {
		return fmt.Errorf("inspect gate CLI compile closure tree: %w", err)
	}
	objectType, err := strictGitLine(output)
	if err != nil || objectType != "tree" {
		return errors.Join(errors.New("gate CLI compile closure object must be a tree"), err)
	}
	return nil
}

// loadReadOnlyTreePaths 只枚举调用方列出的精确 blob 路径，并拒绝 Git 的任何缺失、重复或额外输出。
func loadReadOnlyTreePaths(
	ctx context.Context,
	repoRoot string,
	treeOID string,
	paths []string,
) ([]sourceexport.TreeEntry, error) {
	if len(paths) == 0 || len(paths) > maxReadOnlyGitTreeEntries {
		return nil, fmt.Errorf("gate CLI compile closure path count %d is invalid", len(paths))
	}
	expected := make(map[string]struct{}, len(paths))
	for _, entryPath := range paths {
		if err := validateContextPath(entryPath, make(map[string]string)); err != nil {
			return nil, fmt.Errorf("gate CLI compile closure path %q: %w", entryPath, err)
		}
		if _, exists := expected[entryPath]; exists {
			return nil, fmt.Errorf("gate CLI compile closure path %q is duplicated", entryPath)
		}
		expected[entryPath] = struct{}{}
	}

	args := make([]string, 0, len(paths)+5)
	args = append(args, "ls-tree", "-rz", "--full-tree", treeOID, "--")
	args = append(args, paths...)
	output, err := runGitOutput(ctx, repoRoot, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("list gate CLI compile closure paths: %w", err)
	}
	entries := make([]sourceexport.TreeEntry, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for record := range bytes.SplitSeq(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		entry, err := parseReadOnlyTreeEntry(record)
		if err != nil {
			return nil, err
		}
		if _, exists := expected[entry.Path]; !exists {
			return nil, fmt.Errorf("Git returned unexpected gate CLI compile closure path %q", entry.Path)
		}
		if _, exists := seen[entry.Path]; exists {
			return nil, fmt.Errorf("Git returned duplicate gate CLI compile closure path %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		entries = append(entries, entry)
	}
	if len(entries) != len(expected) {
		missing := make([]string, 0, len(expected)-len(entries))
		for entryPath := range expected {
			if _, exists := seen[entryPath]; !exists {
				missing = append(missing, entryPath)
			}
		}
		slices.Sort(missing)
		return nil, fmt.Errorf("gate CLI compile closure is missing Git paths %q", missing)
	}
	entries, err = loadReadOnlyTreeBlobs(ctx, repoRoot, entries)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
