package remoteportable

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
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

// BuildPortableGoWorkloadInventory 从 exact tree 和 manifest module 声明构建 Go package inventory。
func BuildPortableGoWorkloadInventory(ctx context.Context, reader ExactGitTreeReader, tree, platform string, modules []RepositoryWorkloadModule) (PortableGoWorkloadInventory, error) {
	if ctx == nil || reader == nil || tree == "" || len(modules) == 0 {
		return PortableGoWorkloadInventory{}, errors.New("portable Go inventory inputs are incomplete")
	}
	if platform != string(RepositoryWorkloadPlatformLinuxAMD64) && platform != string(RepositoryWorkloadPlatformWindowsAMD64) {
		return PortableGoWorkloadInventory{}, errors.New("portable Go inventory platform is unsupported")
	}
	entries, err := reader.ListTree(ctx, tree)
	if err != nil {
		return PortableGoWorkloadInventory{}, fmt.Errorf("list exact portable Go tree: %w", err)
	}
	if err := validatePortableTreeEntries(entries); err != nil {
		return PortableGoWorkloadInventory{}, err
	}
	moduleDirs, modulePaths, err := loadDeclaredPortableModules(ctx, reader, tree, entries, modules)
	if err != nil {
		return PortableGoWorkloadInventory{}, err
	}
	packages := portableGoPackages(entries, moduleDirs, modulePaths, platform)
	return PortableGoWorkloadInventory{GoPackages: packages, NestedGoModules: moduleDirs}, nil
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

func loadDeclaredPortableModules(ctx context.Context, reader ExactGitTreeReader, tree string, entries []GitTreeEntry, modules []RepositoryWorkloadModule) ([]string, map[string]string, error) {
	byPath := make(map[string]GitTreeEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	directories := make([]string, 0, len(modules))
	modulePaths := make(map[string]string, len(modules))
	for _, module := range modules {
		moduleFile := "go.mod"
		if module.Root != "." {
			moduleFile = path.Join(module.Root, "go.mod")
		}
		if _, ok := byPath[moduleFile]; !ok {
			return nil, nil, fmt.Errorf("portable declared module %q go.mod is missing", module.Root)
		}
		data, err := reader.ReadBlob(ctx, tree, moduleFile)
		if err != nil {
			return nil, nil, fmt.Errorf("read portable module %q: %w", module.Root, err)
		}
		if !strings.Contains(string(data), "module "+module.ModulePath) {
			return nil, nil, fmt.Errorf("portable module %q path drifted", module.Root)
		}
		directories = append(directories, module.Root)
		modulePaths[module.Root] = module.ModulePath
	}
	sort.Strings(directories)
	return directories, modulePaths, nil
}

func portableGoPackages(entries []GitTreeEntry, moduleDirs []string, modulePaths map[string]string, platform string) []string {
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
	_ = modulePaths
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
