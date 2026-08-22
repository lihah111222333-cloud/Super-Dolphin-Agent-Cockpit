package remoteportable

import (
	"context"
	"errors"
	"fmt"
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
	packages := portableGoPackages(entries, moduleDirs, excludedNestedModules, platform)
	return PortableGoWorkloadInventory{GoPackages: packages, NestedGoModules: moduleDirs}, nil
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

// portableGoPackages 过滤 excluded subtree 后构建确定性 module/package 目标。
func portableGoPackages(entries []GitTreeEntry, moduleDirs, excludedNestedModules []string, platform string) []string {
	moduleSet := make(map[string]struct{}, len(moduleDirs))
	for _, directory := range moduleDirs {
		moduleSet[directory] = struct{}{}
	}
	packages := make(map[string]struct{})
	for _, entry := range entries {
		if path.Ext(entry.Path) != ".go" || !portableGoFileMatchesPlatform(entry.Path, platform) {
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
		relative := strings.TrimPrefix(directory, moduleRoot)
		relative = strings.TrimPrefix(relative, "/")
		packageTarget := "."
		if relative != "" {
			packageTarget = "./" + relative
		}
		packages[moduleRoot+"\x00"+packageTarget] = struct{}{}
	}
	result := make([]string, 0, len(packages))
	for key := range packages {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}

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

// portableGoFileMatchesPlatform 按 Go 文件名的精确 OS/架构后缀筛选目标平台。
func portableGoFileMatchesPlatform(filePath, platform string) bool {
	fileOS, fileArch, ok := portableGoFilenameTarget(filePath)
	if !ok {
		return false
	}
	if fileArch != "" && fileArch != "amd64" {
		return false
	}
	if fileOS == "unix" {
		return platform == string(RepositoryWorkloadPlatformLinuxAMD64)
	}
	switch platform {
	case string(RepositoryWorkloadPlatformLinuxAMD64):
		return fileOS == "" || fileOS == "linux"
	case string(RepositoryWorkloadPlatformWindowsAMD64):
		return fileOS == "" || fileOS == "windows"
	default:
		return false
	}
}

// portableGoFilenameTarget 解析 Go 文件名末尾的 OS 与架构选择器。
func portableGoFilenameTarget(filePath string) (string, string, bool) {
	stem, ok := strings.CutSuffix(path.Base(filePath), ".go")
	if !ok {
		return "", "", false
	}
	components := strings.Split(strings.TrimSuffix(stem, "_test"), "_")
	fileOS, fileArch := "", ""
	last := len(components) - 1
	if last >= 0 && portableGoFilenameOS(components[last]) {
		fileOS = components[last]
	} else if last >= 0 && portableGoFilenameArch(components[last]) {
		fileArch = components[last]
		last--
		if last >= 0 && portableGoFilenameOS(components[last]) {
			fileOS = components[last]
		}
	}
	return fileOS, fileArch, true
}

func portableGoFilenameOS(value string) bool {
	switch value {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos", "unix":
		return true
	default:
		return false
	}
}

func portableGoFilenameArch(value string) bool {
	switch value {
	case "386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm":
		return true
	default:
		return false
	}
}
