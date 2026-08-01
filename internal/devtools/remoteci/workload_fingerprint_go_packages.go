package remoteci

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
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
) error {
	cacheKey := fmt.Sprintf("%t:%s", includeAssets, targetDirectory)
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
		found, imports, err := snapshot.productionGoPackageEntries(directory, selected)
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
) (bool, []string, error) {
	imports := make(map[string]struct{})
	found := false
	for filePath, source := range snapshot.goSources {
		if !remoteProductionGoSourceInDirectory(filePath, source, directory) {
			continue
		}
		entry, ok := snapshot.byPath[filePath]
		if !ok || entry.kind != "blob" {
			return false, nil, fmt.Errorf("remote worker production source %q is not a Git blob", filePath)
		}
		selected[filePath] = entry
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

func remoteProductionGoSourceInDirectory(filePath string, source []byte, directory string) bool {
	return path.Dir(filePath) == directory && path.Ext(filePath) == ".go" &&
		!strings.HasSuffix(filePath, "_test.go") && remoteGoSourceAppliesLinuxAMD64(filePath, source)
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
func (snapshot *remoteGitTreeSnapshot) addGoTestPackageCompileEntries(directory string, selected map[string]remoteGitTreeEntry) ([]remoteGoTestFile, error) {
	files, _, fallback := snapshot.remoteGoTestDeclarations(directory)
	if fallback {
		return nil, errors.New("parse Go test compile inputs")
	}
	if err := snapshot.addGoTestProductionCompileEntries(directory, selected); err != nil {
		return nil, err
	}
	if !snapshot.hasProductionGoPackage(directory) && len(files) == 0 {
		return nil, fmt.Errorf("remote worker Go test package directory %q has no linux/amd64 source files", directory)
	}
	for _, file := range files {
		wholeTree, err := snapshot.addGoTestCompileFileEntries(directory, file, selected)
		if err != nil || wholeTree {
			return nil, err
		}
	}
	return files, nil
}

// addGoExactTestCompileEntries 仅加入精确测试共享的生产编译闭包。
// 测试声明、TestMain、init、包变量和测试辅助函数由 goTestSources 按声明闭包单独摘要。
func (snapshot *remoteGitTreeSnapshot) addGoExactTestCompileEntries(
	directory string,
	selected map[string]remoteGitTreeEntry,
) error {
	files, _, fallback := snapshot.remoteGoTestDeclarations(directory)
	if fallback {
		return errors.New("parse exact Go test compile inputs")
	}
	if err := snapshot.addGoTestProductionCompileEntries(directory, selected); err != nil {
		return err
	}
	if !snapshot.hasProductionGoPackage(directory) && len(files) == 0 {
		return fmt.Errorf("remote worker exact Go test package directory %q has no linux/amd64 source files", directory)
	}
	return nil
}

// addGoTestCompileFileEntries 收集单个测试文件的导入和可静态观察输入。
func (snapshot *remoteGitTreeSnapshot) addGoTestCompileFileEntries(directory string, file remoteGoTestFile, selected map[string]remoteGitTreeEntry) (bool, error) {
	entry, ok := snapshot.byPath[file.path]
	if !ok {
		return false, fmt.Errorf("Go test compile input %q is absent from Git tree", file.path)
	}
	selected[file.path] = entry
	for _, importPath := range remoteGoTestImports(file.file) {
		if localDirectory, local := snapshot.resolveLocalGoImport(importPath); local {
			if err := snapshot.addProductionGoPackageEntriesWithAssets(localDirectory, selected, true); err != nil {
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

func (snapshot *remoteGitTreeSnapshot) addGoTestProductionCompileEntries(directory string, selected map[string]remoteGitTreeEntry) error {
	if !snapshot.hasProductionGoPackage(directory) {
		return nil
	}
	return snapshot.addProductionGoPackageEntriesWithAssets(directory, selected, true)
}

// hasProductionGoPackage 报告目录是否含有适用于 worker 平台的生产 Go 源码。
func (snapshot *remoteGitTreeSnapshot) hasProductionGoPackage(directory string) bool {
	for filePath, source := range snapshot.goSources {
		if remoteProductionGoSourceInDirectory(filePath, source, directory) {
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
		"scripts/test_with_guard.sh", "scripts/check_nested_go_modules.sh", "scripts/real_go_resolver.sh",
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
