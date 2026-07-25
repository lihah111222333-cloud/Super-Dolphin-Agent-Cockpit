package multilsp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
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

// GoRootRequest 描述一次 Go LSP root 解析所需的 cwd、目标文件和环境。
type GoRootRequest struct {
	CWD           string
	FilePath      string
	Env           []string
	NoiseDirNames []string
}

// GoRootInfo 保存 Go root 解析结果，包括 workspace、module、go.work 和工具链信息。
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

// GoToolchainInfo 描述 Go 语言服务运行时需要注入的工具链路径和版本约束。
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

// ResolveGoRoot 为 Go LSP 请求选择工作根目录和工具链环境。
// 它优先尊重有效的 GOWORK，再按 go.mod、子模块和目录回退逐级收敛。
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
			info, err = resolveGoRootWithoutGoWork(target, projectRoot, goworkModeOff, noiseDirNames)
		}
		return withGoToolchain(info, env, err)
	}
	info, err := resolveGoRootWithoutGoWork(target, projectRoot, goworkModeAuto, noiseDirNames)
	return withGoToolchain(info, env, err)
}

// resolveGoRootRequestPaths 归一化请求中的 cwd/filePath，并确定后续向上查找的起点。
// cwd 非法或目标路径无法转成绝对路径时立即返回错误，避免 LSP 绑定到错误目录。
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

// resolveGoRootFromGOWORK 按请求环境里的 GOWORK 解析 workspace。
// auto 模式只接受覆盖当前目标的 go.work，避免外部 worktree 的环境变量污染本次 LSP scope。
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
		// 环境里的 GOWORK 指向其他 worktree 时必须显式 off，不能让子进程继续继承父环境。
		info, err = resolveGoRootWithoutGoWork(target, projectRoot, goworkModeOff, noiseDirNames)
		return info, true, err
	}
	return info, true, err
}

// resolveGoRootWithoutGoWork 处理未启用 go.work 或 go.work 无效时的根目录选择。
// 它先找最近 go.mod，再识别项目根下一层多模块，最后才回退到目标目录。
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
// 这些路径没有具体源码文件，也应视为当前 workspace 的合法 LSP 请求。
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

// findUpwardFile 从起点目录向父级查找指定文件。
// 它用于 go.mod/go.work 发现，文件系统访问失败会直接返回错误给调用方。
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

// findFirstLevelGoModRoots 枚举项目根下一层 go.mod 子模块。
// 仅检查第一层并跳过噪声目录，避免把 vendor、缓存或深层依赖误识别为 workspace folder。
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
// 多模块 workspace 依赖这个选择把单个文件绑定到最近模块，而不是外层项目根。
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

// pathWithinRoot 判断 path 是否位于 root 之内。
// Rel 失败、跨到父目录或形成绝对相对路径时都返回 false，防止 scope 越界。
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

// cleanUniqueFolderPaths 规范化并去重 workspace folder 列表。
// first 始终排在最前，确保 LSP 初始化的主 workspace root 稳定。
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

// goRootEnv 为解析出的 Go root 生成 LSP server 进程环境。
// 这里只写入 GOWORK、PATH、GOTOOLCHAIN，其他环境由调用方按策略继承。
func goRootEnv(root GoRootInfo) []string {
	env := make([]string, 0, 3)
	switch root.GOWORKMode {
	case goworkModeOff:
		env = append(env, "GOWORK=off")
	case goworkModeAuto, goworkModeExplicit:
		if root.GoWorkPath != "" {
			env = append(env, "GOWORK="+root.GoWorkPath)
		}
	}
	if root.GoToolchain.PathEnv != "" {
		env = append(env, "PATH="+root.GoToolchain.PathEnv)
	}
	if root.GoToolchain.ForceLocal {
		env = append(env, "GOTOOLCHAIN=local")
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func goWorkspaceKey(root GoRootInfo) string {
	parts := goWorkspaceKeyPartsFor(root)
	return strings.Join([]string{
		parts.Language,
		parts.RootKind,
		parts.WorkspaceRoot,
		parts.LanguageWorkspaceRoot,
		parts.ProjectRoot,
		formatLanguageSpecific(parts.LanguageSpecific),
	}, "\x00")
}

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

func formatLanguageSpecific(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for i, key := range keys {
		if i > 0 {
			builder.WriteByte('\x1f')
		}
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(values[key])
	}
	return builder.String()
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
	for _, entry := range slices.Backward(env) {
		if value, ok := strings.CutPrefix(entry, prefix); ok {
			return value, true
		}
	}
	return "", false
}
