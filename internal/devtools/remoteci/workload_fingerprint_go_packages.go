package remoteci

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"sort"
	"strconv"
	"strings"
)

// addProductionGoPackageEntriesWithAssets 将生产依赖闭包及可选构建资产加入输入集合。
func (snapshot *remoteGitTreeSnapshot) addProductionGoPackageEntriesWithAssets(
	targetDirectory string,
	selected map[string]remoteGitTreeEntry,
	includeAssets bool,
	profile remoteGoBuildProfile,
) error {
	cacheKey := fmt.Sprintf("%t:%s:%s", includeAssets, profile.cacheKey(), targetDirectory)
	snapshot.cacheMu.Lock()
	if snapshot.productionClosureCache == nil {
		snapshot.productionClosureCache = make(map[string]remoteProductionClosureCache)
	}
	cached, ok := snapshot.productionClosureCache[cacheKey]
	snapshot.cacheMu.Unlock()
	if !ok {
		closure := make(map[string]remoteGitTreeEntry)
		err := snapshot.buildProductionGoPackageEntriesWithAssets(
			targetDirectory,
			closure,
			includeAssets,
			profile,
		)
		cached = remoteProductionClosureCache{
			entries: sortedRemoteGitTreeEntries(closure),
			err:     err,
		}
		snapshot.cacheMu.Lock()
		if existing, exists := snapshot.productionClosureCache[cacheKey]; exists {
			cached = existing
		} else {
			snapshot.productionClosureCache[cacheKey] = cached
		}
		snapshot.cacheMu.Unlock()
	}
	if cached.err != nil {
		return cached.err
	}
	for _, entry := range cached.entries {
		selected[entry.path] = entry
	}
	return nil
}

// buildProductionGoPackageEntriesWithAssets 遍历受 linux/amd64 约束的本地生产包依赖闭包。
func (snapshot *remoteGitTreeSnapshot) buildProductionGoPackageEntriesWithAssets(
	targetDirectory string,
	selected map[string]remoteGitTreeEntry,
	includeAssets bool,
	profile remoteGoBuildProfile,
) error {
	visited := make(map[string]struct{})
	queue := []string{targetDirectory}
	for len(queue) > 0 {
		directory := queue[0]
		queue = queue[1:]
		if _, ok := visited[directory]; ok {
			continue
		}
		visited[directory] = struct{}{}
		found, imports, err := snapshot.productionGoPackageEntries(directory, selected, profile)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("remote worker production package directory %q has no source files", directory)
		}
		if includeAssets {
			snapshot.addGoPackageBuildAssets(directory, selected)
		}
		queue = append(queue, imports...)
	}
	return nil
}

func (snapshot *remoteGitTreeSnapshot) addGoPackageBuildAssets(
	directory string,
	selected map[string]remoteGitTreeEntry,
) {
	for _, entry := range snapshot.entries {
		if path.Ext(entry.path) == ".go" {
			continue
		}
		if path.Dir(entry.path) == directory ||
			strings.HasPrefix(entry.path, directory+"/") {
			selected[entry.path] = entry
		}
	}
}

// productionGoPackageEntries 收集单个目录的适用生产源码与本地导入。
func (snapshot *remoteGitTreeSnapshot) productionGoPackageEntries(
	directory string,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (bool, []string, error) {
	imports := make(map[string]struct{})
	found := false
	for filePath, source := range snapshot.goSources {
		if !remoteProductionGoSourceInDirectory(filePath, source, directory, profile) {
			continue
		}
		entry, ok := snapshot.byPath[filePath]
		if !ok || entry.kind != "blob" {
			return false, nil, fmt.Errorf("remote worker production source %q is not a Git blob", filePath)
		}
		selected[filePath] = entry
		if err := snapshot.addGoEmbedEntries(path.Dir(filePath), source, selected); err != nil {
			return false, nil, err
		}
		found = true
		file, err := parser.ParseFile(token.NewFileSet(), filePath, source, parser.ImportsOnly)
		if err != nil {
			return false, nil, fmt.Errorf("parse remote worker production source %q: %w", filePath, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return false, nil, fmt.Errorf("parse remote worker production import in %q: %w", filePath, err)
			}
			if local, ok := snapshot.resolveLocalGoImport(importPath); ok {
				imports[local] = struct{}{}
			}
		}
	}
	return found, sortedRemoteStringSet(imports), nil
}

func remoteProductionGoSourceInDirectory(filePath string, source []byte, directory string, profile remoteGoBuildProfile) bool {
	return path.Dir(filePath) == directory && path.Ext(filePath) == ".go" &&
		!strings.HasSuffix(filePath, "_test.go") && remoteGoSourceAppliesLinuxAMD64WithProfile(filePath, source, profile)
}

// addGoEmbedEntries 将 Go 编译源码的实际 embed asset 加入 workload 输入集合。
func (snapshot *remoteGitTreeSnapshot) addGoEmbedEntries(
	directory string,
	source []byte,
	selected map[string]remoteGitTreeEntry,
) error {
	entries, err := snapshot.resolveGoEmbedAssets(directory, source)
	if err != nil {
		return fmt.Errorf("Go workload fingerprint %w", err)
	}
	maps.Copy(selected, entries)
	return nil
}

func sortedRemoteStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// addGoTestPackageCompileEntries 收集目标测试包在 worker 平台上的全部编译输入。
func (snapshot *remoteGitTreeSnapshot) addGoTestPackageCompileEntries(directory string, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile) ([]remoteGoTestFile, error) {
	files, _, fallback := snapshot.remoteGoTestDeclarations(directory, profile)
	if fallback {
		return nil, errors.New("parse Go test compile inputs")
	}
	if err := snapshot.addGoTestProductionCompileEntries(directory, selected, profile); err != nil {
		return nil, err
	}
	if !snapshot.hasProductionGoPackage(directory, profile) {
		// 仅测试包即使没有适用于 worker 平台的生产源码，仍会编译并运行包内资产。
		snapshot.addGoPackageBuildAssets(directory, selected)
	}
	if !snapshot.hasProductionGoPackage(directory, profile) && len(files) == 0 {
		return nil, fmt.Errorf("%w: remote worker Go test package directory %q has no linux/amd64 source files", errRemoteWorkloadInputUnavailable, directory)
	}
	for _, file := range files {
		_, err := snapshot.addGoTestCompileFileEntries(directory, file, selected, profile)
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// addGoExactTestCompileEntries 加入 Go test 编译整个同包测试二进制所需的输入。
// 目标测试的运行时可观察输入仍由 goTestSources 保留更细的声明摘要。
func (snapshot *remoteGitTreeSnapshot) addGoExactTestCompileEntries(
	directory string,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (bool, error) {
	entries, err := snapshot.exactCompileRootEntries(directory, profile)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		selected[entry.path] = entry
	}
	return false, nil
}

// addGoExactTestProductionCompileEntries 加入实际 Go 编译闭包及包内非 Go 资产；目标测试运行时观察由 goTestSources 单独处理。
func (snapshot *remoteGitTreeSnapshot) addGoExactTestProductionCompileEntries(directory string, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile) error {
	if !snapshot.hasProductionGoPackage(directory, profile) {
		return nil
	}
	return snapshot.addProductionGoPackageEntriesWithAssets(directory, selected, true, profile)
}

// addGoTestCompileFileEntries 收集单个测试文件的导入和可静态观察输入。
func (snapshot *remoteGitTreeSnapshot) addGoTestCompileFileEntries(directory string, file remoteGoTestFile, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile) (bool, error) {
	entry, ok := snapshot.byPath[file.path]
	if !ok {
		return false, fmt.Errorf("Go test compile input %q is absent from Git tree", file.path)
	}
	selected[file.path] = entry
	if err := snapshot.addGoEmbedEntries(path.Dir(file.path), file.source, selected); err != nil {
		return false, err
	}
	for _, importPath := range remoteGoTestImports(file.file) {
		if localDirectory, local := snapshot.resolveLocalGoImport(importPath); local {
			if err := snapshot.addProductionGoPackageEntriesWithAssets(localDirectory, selected, true, profile); err != nil {
				return false, err
			}
		}
	}
	wholeTree, err := snapshot.addGoTestFileObservedEntries(directory, file.file, file.file, selected)
	if err != nil {
		return false, err
	}
	if wholeTree {
		return true, nil
	}
	return false, nil
}

func (snapshot *remoteGitTreeSnapshot) addGoTestProductionCompileEntries(directory string, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile) error {
	if !snapshot.hasProductionGoPackage(directory, profile) {
		return nil
	}
	return snapshot.addProductionGoPackageEntriesWithAssets(directory, selected, true, profile)
}

// hasProductionGoPackage 报告目录是否含有适用于 worker 平台的生产 Go 源码。
func (snapshot *remoteGitTreeSnapshot) hasProductionGoPackage(directory string, profile remoteGoBuildProfile) bool {
	for filePath, source := range snapshot.goSources {
		if remoteProductionGoSourceInDirectory(filePath, source, directory, profile) {
			return true
		}
	}
	return false
}

func remoteGoPackageDirectory(target string) (string, error) {
	directory := strings.TrimPrefix(target, "./")
	if directory == "" || path.Clean(directory) != directory {
		return "", errors.New("Go workload package target is invalid")
	}
	return directory, nil
}

func (snapshot *remoteGitTreeSnapshot) requiredGoPackageEntries() (map[string]remoteGitTreeEntry, error) {
	selected := make(map[string]remoteGitTreeEntry)
	for _, required := range []string{
		"go.mod", "go.sum",
		"build/gate/runtime-proxy/go.mod", "build/gate/runtime-proxy/go.sum",
		"internal/devtools/gate/executor_mapping.go",
		"scripts/check_nested_go_modules.sh", "scripts/real_go_resolver.sh",
	} {
		entry, ok := snapshot.byPath[required]
		if !ok {
			return nil, fmt.Errorf("Go workload fingerprint required path %q is absent", required)
		}
		selected[required] = entry
	}
	return selected, nil
}

func (snapshot *remoteGitTreeSnapshot) addGoPackageEntries(targetDirectory string, selected map[string]remoteGitTreeEntry) error {
	visited := make(map[string]struct{})
	queue := []string{targetDirectory}
	for len(queue) > 0 {
		directory := queue[0]
		queue = queue[1:]
		if _, ok := visited[directory]; ok {
			continue
		}
		visited[directory] = struct{}{}
		foundGo := snapshot.addDirectoryEntries(directory, selected)
		if !foundGo {
			return fmt.Errorf("Go workload package directory %q has no source files", directory)
		}
		imports, err := snapshot.localGoImports(directory)
		if err != nil {
			return err
		}
		queue = append(queue, imports...)
	}
	return nil
}

// addDirectoryEntries 收集目录内 Go 源码及其相邻的非 Go 构建输入。
func (snapshot *remoteGitTreeSnapshot) addDirectoryEntries(directory string, selected map[string]remoteGitTreeEntry) bool {
	foundGo := false
	for _, entry := range snapshot.entries {
		if path.Dir(entry.path) == directory {
			selected[entry.path] = entry
			foundGo = foundGo || path.Ext(entry.path) == ".go"
			continue
		}
		if strings.HasPrefix(entry.path, directory+"/") && path.Ext(entry.path) != ".go" {
			selected[entry.path] = entry
		}
	}
	return foundGo
}

func sortedRemoteGitTreeEntries(selected map[string]remoteGitTreeEntry) []remoteGitTreeEntry {
	entries := make([]remoteGitTreeEntry, 0, len(selected))
	for _, entry := range selected {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].path < entries[right].path })
	return entries
}
