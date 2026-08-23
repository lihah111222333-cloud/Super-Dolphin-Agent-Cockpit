package remoteportable

import (
	"context"
	"errors"
	"fmt"
	"go/build"
	"path"
	"slices"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// GitTreeEntry 是 exact tree listing 的 provider-neutral 投影。
type GitTreeEntry struct {
	Mode string
	Kind string
	Path string
}

// ExactGitTreeReader 由调用方注入 exact tree listing 和 blob 读取能力。
type ExactGitTreeReader interface {
	ListTree(context.Context, string) ([]GitTreeEntry, error)
	ReadBlob(context.Context, string, string) ([]byte, error)
}

// BuildPortableGoWorkloadInventory 从 exact tree、manifest module 声明和排除项构建 Go package inventory。
func BuildPortableGoWorkloadInventory(ctx context.Context, reader ExactGitTreeReader, tree, platform string, modules []RepositoryWorkloadModule, excludedNestedModules []string) (PortableGoWorkloadInventory, error) {
	if err := validatePortableGoInventoryRequest(ctx, reader, tree, platform, modules, excludedNestedModules); err != nil {
		return PortableGoWorkloadInventory{}, err
	}
	entries, err := reader.ListTree(ctx, tree)
	if err != nil {
		return PortableGoWorkloadInventory{}, fmt.Errorf("list exact portable Go tree: %w", err)
	}
	if err := validatePortableTreeEntries(entries); err != nil {
		return PortableGoWorkloadInventory{}, err
	}
	moduleDirs, err := loadDeclaredPortableModules(ctx, reader, tree, entries, modules, excludedNestedModules)
	if err != nil {
		return PortableGoWorkloadInventory{}, err
	}
	packages, err := portableGoPackages(ctx, reader, tree, entries, moduleDirs, excludedNestedModules, platform)
	if err != nil {
		return PortableGoWorkloadInventory{}, err
	}
	return PortableGoWorkloadInventory{GoPackages: packages, NestedGoModules: moduleDirs}, nil
}

// ValidatePortableGoModuleTopology 只校验 exact tree 的 module/exclusion 闭包，不生成平台 package catalog。
func ValidatePortableGoModuleTopology(ctx context.Context, reader ExactGitTreeReader, tree string, modules []RepositoryWorkloadModule, excludedNestedModules []string) error {
	if err := validatePortableGoInventoryRequest(ctx, reader, tree, string(RepositoryWorkloadPlatformLinuxAMD64), modules, excludedNestedModules); err != nil {
		return err
	}
	entries, err := reader.ListTree(ctx, tree)
	if err != nil {
		return fmt.Errorf("list exact portable Go tree: %w", err)
	}
	if err := validatePortableTreeEntries(entries); err != nil {
		return err
	}
	_, err = loadDeclaredPortableModules(ctx, reader, tree, entries, modules, excludedNestedModules)
	return err
}

// validatePortableGoInventoryRequest 校验 exact-tree inventory 的调用输入和 manifest 集合。
func validatePortableGoInventoryRequest(ctx context.Context, reader ExactGitTreeReader, tree, platform string, modules []RepositoryWorkloadModule, excludedNestedModules []string) error {
	if ctx == nil || reader == nil || tree == "" || len(modules) == 0 {
		return errors.New("portable Go inventory inputs are incomplete")
	}
	if platform != string(RepositoryWorkloadPlatformLinuxAMD64) && platform != string(RepositoryWorkloadPlatformWindowsAMD64) {
		return errors.New("portable Go inventory platform is unsupported")
	}
	if err := validatePortableModules(modules); err != nil {
		return fmt.Errorf("validate portable declared modules: %w", err)
	}
	if err := validatePortableExcludedModules(excludedNestedModules); err != nil {
		return fmt.Errorf("validate portable excluded nested modules: %w", err)
	}
	return nil
}

// validatePortableTreeEntries 校验 exact tree 条目均为唯一、规范的仓库相对 blob 路径。
func validatePortableTreeEntries(entries []GitTreeEntry) error {
	if len(entries) == 0 {
		return errors.New("portable exact tree is empty")
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Kind != "blob" || entry.Path == "" || path.IsAbs(entry.Path) || path.Clean(entry.Path) != entry.Path || strings.Contains(entry.Path, "\\") {
			return errors.New("portable exact tree entry is invalid")
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return fmt.Errorf("portable exact tree repeats %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
	}
	return nil
}

// loadDeclaredPortableModules 枚举 exact tree 模块边界并加载 manifest 声明模块。
func loadDeclaredPortableModules(ctx context.Context, reader ExactGitTreeReader, tree string, entries []GitTreeEntry, modules []RepositoryWorkloadModule, excludedNestedModules []string) ([]string, error) {
	actualDirectories, err := discoverPortableGoModuleDirectories(entries)
	if err != nil {
		return nil, err
	}
	byPath := portableTreeEntriesByPath(entries)
	directories, declaredSet, err := readPortableDeclaredModules(ctx, reader, tree, byPath, modules)
	if err != nil {
		return nil, err
	}
	if err := validatePortableModuleTopology(actualDirectories, declaredSet, excludedNestedModules); err != nil {
		return nil, err
	}
	return directories, nil
}

// discoverPortableGoModuleDirectories 从 exact tree 枚举全部 go.mod 目录。
func discoverPortableGoModuleDirectories(entries []GitTreeEntry) ([]string, error) {
	directories := make([]string, 0)
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if path.Base(entry.Path) != "go.mod" {
			continue
		}
		directory := path.Dir(entry.Path)
		if !validPortableGoModuleDirectory(directory) {
			return nil, fmt.Errorf("portable exact tree module path %q is invalid", directory)
		}
		if _, duplicate := seen[directory]; duplicate {
			return nil, fmt.Errorf("portable exact tree repeats module %q", directory)
		}
		seen[directory] = struct{}{}
		directories = append(directories, directory)
	}
	if len(directories) == 0 {
		return nil, errors.New("portable exact tree has no go.mod")
	}
	sort.Strings(directories)
	return directories, nil
}

// portableTreeEntriesByPath 建立 exact tree 路径索引，供声明模块存在性核对。
func portableTreeEntriesByPath(entries []GitTreeEntry) map[string]GitTreeEntry {
	byPath := make(map[string]GitTreeEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	return byPath
}

// readPortableDeclaredModules 读取并逐项核对 manifest 声明的 module path。
func readPortableDeclaredModules(ctx context.Context, reader ExactGitTreeReader, tree string, byPath map[string]GitTreeEntry, modules []RepositoryWorkloadModule) ([]string, map[string]struct{}, error) {
	directories := make([]string, 0, len(modules))
	declaredSet := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		if err := verifyPortableDeclaredModule(ctx, reader, tree, byPath, module); err != nil {
			return nil, nil, err
		}
		directories = append(directories, module.Root)
		declaredSet[module.Root] = struct{}{}
	}
	sort.Strings(directories)
	return directories, declaredSet, nil
}

// verifyPortableDeclaredModule 确认声明模块的 go.mod 存在且 module path exact。
func verifyPortableDeclaredModule(ctx context.Context, reader ExactGitTreeReader, tree string, byPath map[string]GitTreeEntry, module RepositoryWorkloadModule) error {
	moduleFile := "go.mod"
	if module.Root != "." {
		moduleFile = path.Join(module.Root, "go.mod")
	}
	if _, ok := byPath[moduleFile]; !ok {
		return fmt.Errorf("portable declared module %q go.mod is missing", module.Root)
	}
	data, err := reader.ReadBlob(ctx, tree, moduleFile)
	if err != nil {
		return fmt.Errorf("read portable module %q: %w", module.Root, err)
	}
	actualPath, err := parsePortableModulePath(moduleFile, data)
	if err != nil || actualPath != module.ModulePath {
		return fmt.Errorf("portable module %q path drifted: got %q, want %q", module.Root, actualPath, module.ModulePath)
	}
	return nil
}

// parsePortableModulePath 解析 go.mod 的唯一 module 指令。
func parsePortableModulePath(moduleFile string, data []byte) (string, error) {
	parsed, err := modfile.Parse(moduleFile, data, nil)
	if err != nil {
		return "", err
	}
	if parsed == nil || parsed.Module == nil {
		return "", errors.New("go.mod module directive is missing")
	}
	return parsed.Module.Mod.Path, nil
}

// validPortableGoModuleDirectory 校验 exact tree 中的仓库相对 module 目录。
func validPortableGoModuleDirectory(directory string) bool {
	return directory != "" && !path.IsAbs(directory) && path.Clean(directory) == directory && directory != ".." && !strings.HasPrefix(directory, "../") && !strings.Contains(directory, "\\")
}

// validatePortableModuleTopology 校验 declared、excluded 与 exact-tree module 边界的闭包关系。
func validatePortableModuleTopology(actualDirectories []string, declared map[string]struct{}, excluded []string) error {
	actualSet := make(map[string]struct{}, len(actualDirectories))
	for _, directory := range actualDirectories {
		actualSet[directory] = struct{}{}
	}
	for _, excludedRoot := range excluded {
		if err := validatePortableExcludedModule(excludedRoot, actualSet, declared); err != nil {
			return err
		}
	}
	for _, directory := range actualDirectories {
		if err := validatePortableActualModule(directory, declared, excluded); err != nil {
			return err
		}
	}
	return nil
}

// validatePortableExcludedModule 校验一个排除项存在、未声明且位于 declared module 下。
func validatePortableExcludedModule(excludedRoot string, actual map[string]struct{}, declared map[string]struct{}) error {
	if _, exists := actual[excludedRoot]; !exists {
		return fmt.Errorf("portable excluded nested module %q is stale", excludedRoot)
	}
	for declaredRoot := range declared {
		if declaredRoot == excludedRoot || strings.HasPrefix(declaredRoot, excludedRoot+"/") {
			return fmt.Errorf("portable excluded nested module %q contains declared module %q", excludedRoot, declaredRoot)
		}
	}
	if !portablePathUnderAnyDeclaredModule(excludedRoot, declared) {
		return fmt.Errorf("portable excluded nested module %q is outside declared modules", excludedRoot)
	}
	return nil
}

// validatePortableActualModule 拒绝未声明的 root 或 nested module，以及未由排除 subtree 覆盖的边界。
func validatePortableActualModule(directory string, declared map[string]struct{}, excluded []string) error {
	if directory == "." {
		if _, exists := declared[directory]; !exists {
			return errors.New("portable root Go module is undeclared")
		}
		return nil
	}
	if _, exists := declared[directory]; exists || portablePathUnderAnyExcludedModule(directory, excluded) {
		return nil
	}
	return fmt.Errorf("portable nested Go module %q is undeclared and not excluded", directory)
}

// portablePathUnderAnyDeclaredModule 判断路径是否属于任一 declared module。
func portablePathUnderAnyDeclaredModule(directory string, declared map[string]struct{}) bool {
	for module := range declared {
		if module == "." || directory == module || strings.HasPrefix(directory, module+"/") {
			return true
		}
	}
	return false
}

// portablePathUnderAnyExcludedModule 判断路径是否落入任一 excluded module subtree。
func portablePathUnderAnyExcludedModule(directory string, excluded []string) bool {
	for _, module := range excluded {
		if directory == module || strings.HasPrefix(directory, module+"/") {
			return true
		}
	}
	return false
}

// portableGoPackages 过滤 excluded subtree 后构建确定性、实际可构建的 module/package 目标。
func portableGoPackages(ctx context.Context, reader ExactGitTreeReader, tree string, entries []GitTreeEntry, moduleDirs, excludedNestedModules []string, platform string) ([]string, error) {
	sources, err := loadPortableGoSources(ctx, reader, tree, entries, moduleDirs, excludedNestedModules)
	if err != nil {
		return nil, err
	}
	buildContext, err := portableGoBuildContext(platform, sources)
	if err != nil {
		return nil, err
	}
	candidates, err := portableGoPackageCandidates(buildContext, sources, entries, moduleDirs, excludedNestedModules)
	if err != nil {
		return nil, err
	}
	return portableBuildablePackageKeys(candidates), nil
}

// loadPortableGoSources 从 exact tree 读取 declared module 内且不属于 excluded subtree 的 Go blob。
func loadPortableGoSources(ctx context.Context, reader ExactGitTreeReader, tree string, entries []GitTreeEntry, moduleDirs, excludedNestedModules []string) (map[string]portableGoSource, error) {
	moduleSet := make(map[string]struct{}, len(moduleDirs))
	for _, moduleRoot := range moduleDirs {
		moduleSet[moduleRoot] = struct{}{}
	}
	sources := make(map[string]portableGoSource)
	for _, entry := range entries {
		if path.Ext(entry.Path) != ".go" {
			continue
		}
		directory := path.Dir(entry.Path)
		if portablePathUnderAnyExcludedModule(directory, excludedNestedModules) {
			continue
		}
		moduleRoot := portableOwningModule(directory, moduleDirs)
		if moduleRoot == "" || !portablePathUnderDeclaredModule(directory, moduleRoot, moduleSet) {
			continue
		}
		data, err := reader.ReadBlob(ctx, tree, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("read portable Go source %q: %w", entry.Path, err)
		}
		sources[entry.Path] = portableGoSource{entry: entry, data: data, moduleRoot: moduleRoot}
	}
	return sources, nil
}

// portableGoPackageCandidates 用标准 build context 选择文件并合并同包的 cgo/embed 可构建性。
func portableGoPackageCandidates(buildContext build.Context, sources map[string]portableGoSource, entries []GitTreeEntry, moduleDirs, excludedNestedModules []string) (map[string]portableGoPackageCandidate, error) {
	candidates := make(map[string]portableGoPackageCandidate)
	for _, source := range sources {
		packageKey, candidate, matched, err := portableGoSourceCandidate(buildContext, source, entries, moduleDirs, excludedNestedModules)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		current := candidates[packageKey]
		current.hasBuildableFile = current.hasBuildableFile || candidate.hasBuildableFile
		current.invalid = current.invalid || candidate.invalid
		candidates[packageKey] = current
	}
	return candidates, nil
}

// portableBuildablePackageKeys 筛出至少一个可构建文件且未被 unresolved embed 使整包失效的目标。
func portableBuildablePackageKeys(candidates map[string]portableGoPackageCandidate) []string {
	result := make([]string, 0, len(candidates))
	for packageKey, candidate := range candidates {
		if candidate.hasBuildableFile && !candidate.invalid {
			result = append(result, packageKey)
		}
	}
	slices.Sort(result)
	return result
}

// portableOwningModule 返回包含 directory 的最长 declared module root。
func portableOwningModule(directory string, moduleDirs []string) string {
	best := ""
	for _, module := range moduleDirs {
		if module == "." || directory == module || strings.HasPrefix(directory, module+"/") {
			if len(module) > len(best) {
				best = module
			}
		}
	}
	return best
}

func portablePathUnderDeclaredModule(directory, module string, modules map[string]struct{}) bool {
	if _, ok := modules[module]; !ok {
		return false
	}
	return module == "." || directory == module || strings.HasPrefix(directory, module+"/")
}
