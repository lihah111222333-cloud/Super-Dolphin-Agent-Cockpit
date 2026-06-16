package multilsp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	goRootKindGoWork          = "go_work"
	goRootKindGoMod           = "go_mod"
	goRootKindSingleSubmodule = "single_submodule"
	goRootKindMultiModule     = "multi_module"
	goRootKindDirFallback     = "dir_fallback"

	goworkModeAuto     = "auto"
	goworkModeOff      = "off"
	goworkModeExplicit = "explicit"
)

type GoRootRequest struct {
	CWD           string
	FilePath      string
	Env           []string
	NoiseDirNames []string
}

type GoRootInfo struct {
	RootKind      string
	WorkspaceRoot string
	GoWorkPath    string
	ModuleRoot    string
	GoModPath     string
	ModuleRoots   []string
	GOWORKMode    string
	ProjectRoot   string
	GoToolchain   GoToolchainInfo
}

type GoToolchainInfo struct {
	RequiredVersion string
	BinDir          string
	PathEnv         string
	ForceLocal      bool
}

type goWorkspaceKeyParts struct {
	Language              string
	RootKind              string
	WorkspaceRoot         string
	LanguageWorkspaceRoot string
	ProjectRoot           string
	LanguageSpecific      map[string]string
}

// ResolveGoRoot 解析go根目录。
func ResolveGoRoot(req GoRootRequest) (GoRootInfo, error) {
	target, projectRoot, err := resolveGoRootRequestPaths(req)
	if err != nil {
		return GoRootInfo{}, err
	}
	env := goRootRequestEnv(req)
	noiseDirNames := resolvedGoNoiseDirNames(req.NoiseDirNames)
	if info, handled, err := resolveGoRootFromGOWORK(target, projectRoot, env, noiseDirNames); handled || err != nil {
		return withGoToolchain(info, env, err)
	}
	goWorkPath, err := findGoWorkPath(target)
	if err != nil {
		return GoRootInfo{}, err
	}
	if goWorkPath != "" {
		info, err := resolveGoWorkRoot(target, projectRoot, goWorkPath, goworkModeAuto)
		if err == nil && !goWorkRootContainsTarget(info, target) {
			info, err = resolveGoRootWithoutGoWork(target, projectRoot, goworkModeAuto, noiseDirNames)
		}
		return withGoToolchain(info, env, err)
	}
	info, err := resolveGoRootWithoutGoWork(target, projectRoot, goworkModeAuto, noiseDirNames)
	return withGoToolchain(info, env, err)
}

// resolveGoRootRequestPaths 解析go根目录请求路径。
func resolveGoRootRequestPaths(req GoRootRequest) (string, string, error) {
	projectRoot, err := normalizeOptionalPath(req.CWD, "")
	if err != nil {
		return "", "", err
	}
	target, err := resolveGoTargetPath(req.FilePath, projectRoot)
	if err != nil {
		return "", "", err
	}
	if target == "" {
		target = projectRoot
	}
	if projectRoot == "" {
		projectRoot = fallbackProjectRoot(target)
	}
	if target == "" {
		return "", "", ErrWorkspaceRootEmpty
	}
	return target, projectRoot, nil
}

func goRootRequestEnv(req GoRootRequest) []string {
	if req.Env != nil {
		return req.Env
	}
	return os.Environ()
}

// resolveGoRootFromGOWORK 从gowork解析go根目录。
func resolveGoRootFromGOWORK(target, projectRoot string, env []string, noiseDirNames []string) (GoRootInfo, bool, error) {
	gowork, ok := envValue(env, "GOWORK")
	if !ok {
		return GoRootInfo{}, false, nil
	}
	trimmed := strings.TrimSpace(gowork)
	if strings.EqualFold(trimmed, goworkModeOff) {
		info, err := resolveGoRootWithoutGoWork(target, projectRoot, goworkModeOff, noiseDirNames)
		return info, true, err
	}
	if trimmed == "" || strings.EqualFold(trimmed, goworkModeAuto) {
		return GoRootInfo{}, false, nil
	}
	goWorkPath, err := normalizeOptionalPath(trimmed, "")
	if err != nil {
		return GoRootInfo{}, true, err
	}
	if !fileExists(goWorkPath) {
		return GoRootInfo{}, true, fmt.Errorf("GOWORK path does not exist: %s", goWorkPath)
	}
	info, err := resolveGoWorkRoot(target, projectRoot, goWorkPath, goworkModeExplicit)
	if err == nil && !goWorkRootContainsTarget(info, target) {
		// Ambient GOWORK can point at another worktree's go.work; let target-local discovery decide.
		return GoRootInfo{}, false, nil
	}
	return info, true, err
}

// resolveGoRootWithoutGoWork 解析go根目录withoutgowork。
func resolveGoRootWithoutGoWork(target, projectRoot, mode string, noiseDirNames []string) (GoRootInfo, error) {
	if goModPath, err := findGoModPath(target); err != nil {
		return GoRootInfo{}, err
	} else if goModPath != "" {
		moduleRoot := filepath.Dir(goModPath)
		return GoRootInfo{
			RootKind:      goRootKindGoMod,
			WorkspaceRoot: moduleRoot,
			ModuleRoot:    moduleRoot,
			GoModPath:     goModPath,
			ModuleRoots:   []string{moduleRoot},
			GOWORKMode:    mode,
			ProjectRoot:   fallbackProjectRootValue(projectRoot, moduleRoot),
		}, nil
	}

	fallbackRoot := projectRoot
	if fallbackRoot == "" {
		fallbackRoot = fallbackProjectRoot(target)
	}
	if fallbackRoot == "" {
		fallbackRoot = filepath.Dir(target)
	}
	modules, err := findFirstLevelGoModRoots(fallbackRoot, noiseDirNames)
	if err != nil {
		return GoRootInfo{}, err
	}
	switch len(modules) {
	case 0:
		return GoRootInfo{
			RootKind:      goRootKindDirFallback,
			WorkspaceRoot: fallbackRoot,
			GOWORKMode:    mode,
			ProjectRoot:   fallbackProjectRootValue(projectRoot, fallbackRoot),
		}, nil
	case 1:
		return GoRootInfo{
			RootKind:      goRootKindSingleSubmodule,
			WorkspaceRoot: modules[0],
			ModuleRoot:    modules[0],
			GoModPath:     filepath.Join(modules[0], "go.mod"),
			ModuleRoots:   modules,
			GOWORKMode:    mode,
			ProjectRoot:   fallbackProjectRootValue(projectRoot, fallbackRoot),
		}, nil
	default:
		return GoRootInfo{
			RootKind:      goRootKindMultiModule,
			WorkspaceRoot: fallbackRoot,
			ModuleRoots:   modules,
			GOWORKMode:    mode,
			ProjectRoot:   fallbackProjectRootValue(projectRoot, fallbackRoot),
		}, nil
	}
}

func resolveGoWorkRoot(target, projectRoot, goWorkPath, mode string) (GoRootInfo, error) {
	goWorkPath, err := normalizeOptionalPath(goWorkPath, "")
	if err != nil {
		return GoRootInfo{}, err
	}
	workspaceRoot := filepath.Dir(goWorkPath)
	moduleRoots, err := parseGoWorkModuleRoots(goWorkPath)
	if err != nil {
		return GoRootInfo{}, fmt.Errorf("parse go.work %s: %w", goWorkPath, err)
	}
	moduleRoot := longestContainingRoot(target, moduleRoots)
	goModPath := ""
	if moduleRoot != "" {
		goModPath = filepath.Join(moduleRoot, "go.mod")
	}
	return GoRootInfo{
		RootKind:      goRootKindGoWork,
		WorkspaceRoot: workspaceRoot,
		GoWorkPath:    goWorkPath,
		ModuleRoot:    moduleRoot,
		GoModPath:     goModPath,
		ModuleRoots:   moduleRoots,
		GOWORKMode:    mode,
		ProjectRoot:   fallbackProjectRootValue(projectRoot, workspaceRoot),
	}, nil
}

func goWorkRootContainsTarget(info GoRootInfo, target string) bool {
	if strings.TrimSpace(target) == "" {
		return true
	}
	if goWorkRootContainsSpecialTarget(info, target) {
		return true
	}
	for _, moduleRoot := range info.ModuleRoots {
		if pathWithinRoot(target, moduleRoot) {
			return true
		}
	}
	return false
}

// goWorkRootContainsSpecialTarget 判断目标是否正好落在 go.work 或 workspace 根上。
func goWorkRootContainsSpecialTarget(info GoRootInfo, target string) bool {
	normalized, err := platformshared.NormalizeAbsolutePath(target)
	if err != nil || normalized == "" {
		return false
	}
	return (info.GoWorkPath != "" && normalized == info.GoWorkPath) ||
		(info.WorkspaceRoot != "" && normalized == info.WorkspaceRoot)
}

func findGoModPath(path string) (string, error) {
	return findUpwardFile(path, "go.mod")
}

func findGoWorkPath(path string) (string, error) {
	return findUpwardFile(path, "go.work")
}

// findUpwardFile 查找upward文件。
func findUpwardFile(path, name string) (string, error) {
	absPath, err := platformshared.NormalizeAbsolutePath(path)
	if err != nil {
		return "", err
	}
	startDir, err := resolveStartDir(absPath)
	if err != nil {
		return "", err
	}
	for dir := startDir; dir != "" && dir != "."; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, name)
		if fileExists(candidate) {
			return candidate, nil
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", nil
}

// findFirstLevelGoModRoots 查找firstlevelgomod根目录。
func findFirstLevelGoModRoots(root string, noiseDirNames []string) ([]string, error) {
	root, err := normalizeOptionalPath(root, "")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("go root %q missing: %w", root, err)
		}
		return nil, err
	}
	modules := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if shouldSkipGoChildModuleDir(entry.Name(), noiseDirNames) {
			continue
		}
		if fileExists(filepath.Join(root, entry.Name(), "go.mod")) {
			modules = append(modules, filepath.Join(root, entry.Name()))
		}
	}
	return cleanSortedUniquePaths(modules), nil
}

func shouldSkipGoChildModuleDir(name string, noiseDirNames []string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	_, ok := stringSetFromList(resolvedGoNoiseDirNames(noiseDirNames))[name]
	return ok
}

func resolvedGoNoiseDirNames(noiseDirNames []string) []string {
	if len(noiseDirNames) > 0 {
		return noiseDirNames
	}
	noiseDirSet := defaultLanguageServiceNoiseDirSet()
	names := make([]string, 0, len(noiseDirSet))
	for name := range noiseDirSet {
		names = append(names, name)
	}
	return names
}

func resolveGoTargetPath(filePath, cwd string) (string, error) {
	trimmed := strings.TrimSpace(filePath)
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "file://") {
		return absolutePathFromURI(trimmed)
	}
	if !filepath.IsAbs(trimmed) && cwd != "" {
		trimmed = filepath.Join(cwd, trimmed)
	}
	return platformshared.NormalizeAbsolutePath(trimmed)
}

func normalizeOptionalPath(path, base string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	if !filepath.IsAbs(trimmed) && base != "" {
		trimmed = filepath.Join(base, trimmed)
	}
	return platformshared.NormalizeAbsolutePath(trimmed)
}

func fallbackProjectRoot(target string) string {
	if strings.TrimSpace(target) == "" {
		return ""
	}
	startDir, err := resolveStartDir(target)
	if err != nil {
		return filepath.Dir(target)
	}
	return startDir
}

func fallbackProjectRootValue(projectRoot, fallback string) string {
	if strings.TrimSpace(projectRoot) != "" {
		return projectRoot
	}
	return fallback
}

// longestContainingRoot 从候选根目录里选出最深的包含路径。
func longestContainingRoot(path string, roots []string) string {
	if len(roots) == 0 || strings.TrimSpace(path) == "" {
		return ""
	}
	normalized, err := platformshared.NormalizeAbsolutePath(path)
	if err != nil {
		return ""
	}
	best := ""
	for _, root := range roots {
		if pathWithinRoot(normalized, root) && len(root) > len(best) {
			best = root
		}
	}
	return best
}

// pathWithinRoot 处理路径within根目录。
func pathWithinRoot(path, root string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func workspaceFolders(root GoRootInfo) []protocol.WorkspaceFolder {
	paths := root.workspaceFolderPaths()
	folders := make([]protocol.WorkspaceFolder, 0, len(paths))
	for _, folderPath := range paths {
		uri := fileURIFromPath(folderPath)
		folders = append(folders, protocol.WorkspaceFolder{
			URI:  uri,
			Name: workspaceName(uri),
		})
	}
	return folders
}

func (root GoRootInfo) workspaceFolderPaths() []string {
	paths := []string{root.WorkspaceRoot}
	paths = append(paths, root.ModuleRoots...)
	return cleanUniqueFolderPaths(paths, root.WorkspaceRoot)
}

// cleanUniqueFolderPaths 处理cleanuniquefolder路径。
func cleanUniqueFolderPaths(paths []string, first string) []string {
	normalized := cleanSortedUniquePaths(paths)
	if first == "" {
		return normalized
	}
	first, err := platformshared.NormalizeAbsolutePath(first)
	if err != nil || first == "" {
		return normalized
	}
	out := []string{first}
	for _, path := range normalized {
		if path != first {
			out = append(out, path)
		}
	}
	return out
}

func cleanSortedUniquePaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		normalized, err := normalizeOptionalPath(path, "")
		if err != nil || normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

// goRootEnv 处理go根目录env。
func goWorkspaceKeyPartsFor(root GoRootInfo) goWorkspaceKeyParts {
	languageWorkspaceRoot := root.ModuleRoot
	if languageWorkspaceRoot == "" {
		languageWorkspaceRoot = root.WorkspaceRoot
	}
	projectRoot := root.ProjectRoot
	if projectRoot == "" {
		projectRoot = root.WorkspaceRoot
	}
	return goWorkspaceKeyParts{
		Language:              "go",
		RootKind:              root.RootKind,
		WorkspaceRoot:         root.WorkspaceRoot,
		LanguageWorkspaceRoot: languageWorkspaceRoot,
		ProjectRoot:           projectRoot,
		LanguageSpecific:      goLanguageSpecific(root),
	}
}

func goLanguageSpecific(root GoRootInfo) map[string]string {
	specific := map[string]string{
		"goModPath":            root.GoModPath,
		"goWorkPath":           root.GoWorkPath,
		"goworkMode":           root.GOWORKMode,
		"moduleRoot":           root.ModuleRoot,
		"moduleRootsHash":      hashStringList(cleanSortedUniquePaths(root.ModuleRoots)),
		"workspaceFoldersHash": hashWorkspaceFolders(workspaceFolders(root)),
	}
	if root.GoToolchain.PathEnv != "" {
		specific["goToolchainBinDir"] = root.GoToolchain.BinDir
		specific["goToolchainPathEnv"] = root.GoToolchain.PathEnv
		specific["goToolchainRequired"] = root.GoToolchain.RequiredVersion
	}
	return specific
}

func hashWorkspaceFolders(folders []protocol.WorkspaceFolder) string {
	values := make([]string, 0, len(folders))
	for _, folder := range folders {
		values = append(values, folder.URI+"\x00"+folder.Name)
	}
	return hashStringList(values)
}

func hashStringList(values []string) string {
	cleaned := append([]string(nil), values...)
	sort.Strings(cleaned)
	hash := sha256.New()
	for _, value := range cleaned {
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], prefix); ok {
			return value, true
		}
	}
	return "", false
}
