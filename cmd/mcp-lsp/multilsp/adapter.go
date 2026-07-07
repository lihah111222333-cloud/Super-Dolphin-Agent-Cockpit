package multilsp

import (
	"context"
	"fmt"
	"go/build/constraint"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

const goBuildTagsLanguageSpecificKey = "goBuildTags"

// LanguageAdapter 封装每种语言接入 LSP manager/pool/cache 所需的差异策略。
// manager 只消费解析后的 scope、启动命令、环境、引导缓存和能力开关，避免调用层感知具体语言。
type LanguageAdapter interface {
	LanguageIDs() []string
	ResolveRoot(ctx context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error)
	ServerCommand(ctx context.Context, scope ResolvedLanguageScope) (ServerCommand, error)
	InitOptions(scope ResolvedLanguageScope) map[string]any
	EnvPolicy(scope ResolvedLanguageScope) []string
	BootstrapPolicy(scope ResolvedLanguageScope) BootstrapPolicy
	CacheKeyParts(scope ResolvedLanguageScope) map[string]string
	CapabilityPolicy() ToolCapabilityPolicy
}

// ResolvedLanguageScope 是 adapter 为 manager 解析出的语言工作区边界。
// 它同时保留通用 workspace、语言专属 root 和缓存维度所需信息。
type ResolvedLanguageScope struct {
	LanguageID            string
	WorkspaceRoot         string
	LanguageWorkspaceRoot string
	ProjectRoot           string
	RootKind              string
	LanguageSpecific      map[string]string
	WorkspaceFolders      []protocol.WorkspaceFolder
}

// ServerCommand 描述启动语言服务器所需的可执行文件和参数。
type ServerCommand struct {
	Executable string
	Args       []string
}

// BootstrapPolicy 声明 LSP client 初始化后需要预打开的目标和辅助文件。
type BootstrapPolicy struct {
	OpenTarget                     bool
	OpenSiblingDocuments           bool
	TreatMissingDiagnosticsAsEmpty bool
	SiblingExtensions              []string
	FirstSourceExtensions          []string
	IgnoredDirNames                map[string]struct{}
}

// ToolCapabilityPolicy 声明工具调用对真实 LSP client 或 fallback 能力的依赖。
type ToolCapabilityPolicy struct {
	RequiresLSPClient              bool
	DocumentSymbolFallback         bool
	RetryEmptyCallHierarchyPrepare bool
}

// LanguageAdapterRegistry 按 language id 管理 adapter，并提供并发安全的查询入口。
type LanguageAdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]LanguageAdapter
}

// NewLanguageAdapterRegistry 创建语言适配器注册表，并按每个 adapter 声明的 language id 建索引。
// 调用方可继续追加注册，重复 language id 会由后注册项覆盖。
func NewLanguageAdapterRegistry(adapters ...LanguageAdapter) *LanguageAdapterRegistry {
	registry := &LanguageAdapterRegistry{adapters: map[string]LanguageAdapter{}}
	for _, adapter := range adapters {
		registry.Register(adapter)
	}
	return registry
}

// NewDefaultLanguageAdapterRegistry 使用当前 LSP 配置创建默认语言适配器注册表。
// 配置中的 root marker、目录过滤和启动参数会在各 adapter 构造时固化。
func NewDefaultLanguageAdapterRegistry() *LanguageAdapterRegistry {
	return NewLanguageAdapterRegistryFromConfig(platformconfig.DefaultLSPConfig())
}

// Register 把一个语言 adapter 按规范化 language id 注册到表中。
// nil receiver 或 nil adapter 直接忽略，便于测试按需组装最小注册表。
func (r *LanguageAdapterRegistry) Register(adapter LanguageAdapter) {
	if r == nil || adapter == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapters == nil {
		r.adapters = map[string]LanguageAdapter{}
	}
	for _, languageID := range adapter.LanguageIDs() {
		if normalized := normalizeLanguageID(languageID); normalized != "" {
			r.adapters[normalized] = adapter
		}
	}
}

// AdapterForLanguage 查找指定 language id 的 adapter。
// language id 会先规范化，未注册或空注册表时返回 false。
func (r *LanguageAdapterRegistry) AdapterForLanguage(languageID string) (LanguageAdapter, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[normalizeLanguageID(languageID)]
	return adapter, ok
}

// LanguageIDs 返回当前注册表中已登记的 language id。
// 结果按字典序排序，保证 manifest 和测试快照稳定。
func (r *LanguageAdapterRegistry) LanguageIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.adapters))
	for id := range r.adapters {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

type goLanguageAdapter struct {
	directoryFilters []string
	noiseDirNames    []string
}

// LanguageIDs 返回 Go adapter 覆盖的文件语言。
// gomod/gosum/gowork 与 go 文件共用同一套 root 和工具链解析。
func (goLanguageAdapter) LanguageIDs() []string { return []string{"go", "gomod", "gosum", "gowork"} }

// ResolveRoot 为 Go 语言请求解析 LSP scope。
// 它把 ResolveGoRoot 的工作根、模块根和工具链信息转换为 manager/pool/cache 可消费的结构。
func (a goLanguageAdapter) ResolveRoot(_ context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error) {
	languageID := normalizeLanguageID(scope.LanguageID)
	if languageID == "" {
		return ResolvedLanguageScope{}, fmt.Errorf("go adapter requires a resolved language ID")
	}
	info, err := ResolveGoRoot(GoRootRequest{
		CWD:           scope.CWD,
		FilePath:      firstNonEmpty(target, scope.TargetPath, scope.CWD),
		Env:           os.Environ(),
		NoiseDirNames: a.noiseDirNames,
	})
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	parts := goWorkspaceKeyPartsFor(info)
	languageSpecific := copyLanguageSpecific(parts.LanguageSpecific)
	buildTags, err := goBuildTagsForTarget(scope.CWD, firstNonEmpty(target, scope.TargetPath, scope.CWD))
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	if len(buildTags) > 0 {
		languageSpecific[goBuildTagsLanguageSpecificKey] = strings.Join(buildTags, ",")
	}
	return ResolvedLanguageScope{
		LanguageID:            languageID,
		WorkspaceRoot:         parts.WorkspaceRoot,
		LanguageWorkspaceRoot: parts.LanguageWorkspaceRoot,
		ProjectRoot:           parts.ProjectRoot,
		RootKind:              parts.RootKind,
		LanguageSpecific:      languageSpecific,
		WorkspaceFolders:      workspaceFolders(info),
	}, nil
}

// ServerCommand 返回 Go 语言服务启动命令。
// gopls 的安装校验由 registry/installer 负责，这里只声明运行时可执行文件。
func (goLanguageAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{Executable: "gopls"}, nil
}

// InitOptions 生成 gopls 初始化选项。
// directoryFilters 来自 adapter 配置或默认配置，用于把噪声目录排除在 LSP 索引之外。
func (a goLanguageAdapter) InitOptions(scope ResolvedLanguageScope) map[string]any {
	options := map[string]any{
		"semanticTokens":   true,
		"directoryFilters": a.resolvedDirectoryFilters(),
	}
	buildFlags := goBuildFlagsForScope(scope)
	if len(buildFlags) > 0 {
		options["buildFlags"] = buildFlags
		options["settings"] = map[string]any{
			"gopls": map[string]any{"buildFlags": buildFlags},
		}
	}
	return options
}

// goBuildTagsForTarget 提取目标 Go 文件头部声明的 build tags。
// tags 会进入 gopls buildFlags 和 cache key，避免 e2e 等带 tag 文件被诊断成 orphan。
func goBuildTagsForTarget(cwd, target string) ([]string, error) {
	target = strings.TrimSpace(target)
	if target == "" || filepath.Ext(target) != ".go" {
		return nil, nil
	}
	path, err := normalizeGoBuildTagTarget(cwd, target)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		return nil, nil
	default:
		return nil, fmt.Errorf("read Go build tags: %w", err)
	}
	return goBuildTagsFromSource(string(data))
}

func normalizeGoBuildTagTarget(cwd, target string) (string, error) {
	if filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	base := strings.TrimSpace(cwd)
	if base == "" {
		base = "."
	}
	path, err := filepath.Abs(filepath.Join(base, target))
	if err != nil {
		return "", fmt.Errorf("resolve Go build tag target: %w", err)
	}
	return filepath.Clean(path), nil
}

// goBuildTagsFromSource 扫描 Go 文件头部 build constraint 并提取正向 tag。
// 非约束注释和空行会被跳过，遇到 package 前的真实代码后停止扫描。
func goBuildTagsFromSource(source string) ([]string, error) {
	tags := map[string]struct{}{}
	for rawLine := range strings.SplitSeq(source, "\n") {
		line := strings.TrimSpace(rawLine)
		if goBuildConstraintLine(line) {
			if err := collectGoBuildConstraintTags(line, tags); err != nil {
				return nil, err
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		break
	}
	return sortedGoBuildTags(tags), nil
}

func goBuildConstraintLine(line string) bool {
	return constraint.IsGoBuild(line) || constraint.IsPlusBuild(line)
}

func collectGoBuildConstraintTags(line string, tags map[string]struct{}) error {
	expr, err := constraint.Parse(line)
	if err != nil {
		return fmt.Errorf("parse Go build constraint: %w", err)
	}
	collectPositiveGoBuildTags(expr, tags)
	return nil
}

func collectPositiveGoBuildTags(expr constraint.Expr, tags map[string]struct{}) {
	switch typed := expr.(type) {
	case *constraint.TagExpr:
		if typed.Tag != "" {
			tags[typed.Tag] = struct{}{}
		}
	case *constraint.AndExpr:
		collectPositiveGoBuildTags(typed.X, tags)
		collectPositiveGoBuildTags(typed.Y, tags)
	case *constraint.OrExpr:
		collectPositiveGoBuildTags(typed.X, tags)
		collectPositiveGoBuildTags(typed.Y, tags)
	}
}

func sortedGoBuildTags(tags map[string]struct{}) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	for tag := range tags {
		out = append(out, tag)
	}
	slices.Sort(out)
	return out
}

func goBuildFlagsForScope(scope ResolvedLanguageScope) []string {
	tags := strings.TrimSpace(scope.LanguageSpecific[goBuildTagsLanguageSpecificKey])
	if tags == "" {
		return nil
	}
	return []string{"-tags=" + tags}
}

func (a goLanguageAdapter) resolvedDirectoryFilters() []string {
	filters := a.directoryFilters
	if len(filters) == 0 {
		filters = platformconfig.DefaultLSPConfig().GoDirectoryFilters
	}
	return slices.Clone(filters)
}

// EnvPolicy 为 gopls 进程生成与当前 Go scope 匹配的环境覆盖。
// 只覆盖 GOWORK/PATH/GOTOOLCHAIN/GOFLAGS，避免把请求外的环境策略混入 adapter。
func (goLanguageAdapter) EnvPolicy(scope ResolvedLanguageScope) []string {
	env := make([]string, 0, 4)
	mode := scope.LanguageSpecific["goworkMode"]
	switch mode {
	case goworkModeOff:
		env = append(env, "GOWORK=off")
	case goworkModeAuto, goworkModeExplicit:
		if path := scope.LanguageSpecific["goWorkPath"]; path != "" {
			env = append(env, "GOWORK="+path)
		}
	}
	if pathEnv := scope.LanguageSpecific["goToolchainPathEnv"]; pathEnv != "" {
		env = append(env, "PATH="+pathEnv, "GOTOOLCHAIN=local")
	}
	if buildFlags := goBuildFlagsForScope(scope); len(buildFlags) > 0 {
		env = append(env, "GOFLAGS="+goGOFlagsEnvValue(buildFlags))
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func goGOFlagsEnvValue(buildFlags []string) string {
	current := strings.TrimSpace(os.Getenv("GOFLAGS"))
	if current == "" {
		return strings.Join(buildFlags, " ")
	}
	return current + " " + strings.Join(buildFlags, " ")
}

// BootstrapPolicy 声明 Go LSP 启动后需要打开目标文档。
// 这样 gopls 能尽快产生诊断和符号索引。
func (goLanguageAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{
		OpenTarget:                     true,
		TreatMissingDiagnosticsAsEmpty: true,
	}
}

// CacheKeyParts 返回 Go scope 的语言专属缓存维度。
// go.work 模式、模块根和工具链信息都会进入 key，防止不同 workspace 复用错误缓存。
func (goLanguageAdapter) CacheKeyParts(scope ResolvedLanguageScope) map[string]string {
	return copyLanguageSpecific(scope.LanguageSpecific)
}

// CapabilityPolicy 声明 Go adapter 需要真实 LSP client。
// 没有 client 时不能降级成纯文本符号结果。
func (goLanguageAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{RequiresLSPClient: true}
}

type projectLanguageAdapter struct {
	languageIDs                    []string
	command                        ServerCommand
	rootMarkers                    []string
	rootKind                       string
	firstSourceExtensions          []string
	ignoredDirNames                map[string]struct{}
	initOptions                    map[string]any
	envPolicy                      func(ResolvedLanguageScope) []string
	retryEmptyCallHierarchyPrepare bool
}

// LanguageIDs 返回项目型 adapter 负责的 language id 副本。
// 返回副本避免调用方修改注册表内的语言集合。
func (a projectLanguageAdapter) LanguageIDs() []string {
	return append([]string(nil), a.languageIDs...)
}

// ResolveRoot 为 TypeScript、Python 等项目型语言选择项目根。
// 它优先使用 root marker，必要时在 cwd 下查找嵌套项目，最后才退回目录 scope。
func (a projectLanguageAdapter) ResolveRoot(_ context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error) {
	languageID := normalizeLanguageID(scope.LanguageID)
	if languageID == "" {
		return ResolvedLanguageScope{}, fmt.Errorf("adapter requires a resolved language ID")
	}
	targetPath := firstNonEmpty(target, scope.TargetPath)
	root := firstNonEmpty(scope.CWD, filepath.Dir(targetPath))
	if markerRoot, err := findProjectRoot(firstNonEmpty(targetPath, root), a.rootMarkers); err != nil {
		return ResolvedLanguageScope{}, err
	} else if markerRoot != "" {
		root = markerRoot
	} else if a.shouldSearchNestedProjectRoot(root, targetPath) {
		nested, walkErr := findProjectRootWithin(root, a.rootMarkers, a.ignoredDirNames)
		if walkErr != nil {
			return ResolvedLanguageScope{}, walkErr
		}
		if nested != "" {
			root = nested
		}
	}
	normalized, err := normalizeRegistryWorkspaceRoot(root)
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	rootKind := a.rootKind
	if !hasProjectMarker(normalized, a.rootMarkers) {
		rootKind = "dir_fallback"
	}
	languageSpecific, err := a.languageSpecificForResolvedRoot(scope, normalized)
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	return ResolvedLanguageScope{
		LanguageID:            languageID,
		WorkspaceRoot:         normalized,
		LanguageWorkspaceRoot: normalized,
		ProjectRoot:           normalized,
		RootKind:              rootKind,
		LanguageSpecific:      languageSpecific,
	}, nil
}

func (a projectLanguageAdapter) languageSpecificForResolvedRoot(scope LSPToolScope, projectRoot string) (map[string]string, error) {
	if !a.usesJSTSWorkspace() {
		return nil, nil
	}
	installRoot, err := findJSTSPnpmInstallRoot(projectRoot, scope.CWD)
	if err != nil {
		return nil, err
	}
	if installRoot == "" {
		return nil, nil
	}
	return map[string]string{
		jstsPackageManagerKey:  jstsPackageManagerPnpm,
		jstsPnpmInstallRootKey: installRoot,
	}, nil
}

func (a projectLanguageAdapter) usesJSTSWorkspace() bool {
	return slices.ContainsFunc(a.languageIDs, shouldUseJSTSWorkspace)
}

// shouldSearchNestedProjectRoot 判断是否需要在 cwd 下继续查找嵌套项目根。
// 当目标已经是源码文件且落在当前 root 内时不再向下扫描，避免误绑定 sibling 项目。
func (a projectLanguageAdapter) shouldSearchNestedProjectRoot(root, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return true
	}
	if root != "" {
		if rel, err := filepath.Rel(root, target); err == nil && rel != "." && !isParentRelativePath(rel) {
			if info, statErr := os.Stat(target); statErr == nil {
				return info.IsDir()
			}
			return !a.targetUsesSourceExtension(target)
		}
	}
	return true
}

func isParentRelativePath(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func (a projectLanguageAdapter) targetUsesSourceExtension(target string) bool {
	ext := strings.ToLower(filepath.Ext(target))
	if ext == "" {
		return false
	}
	for _, sourceExt := range a.firstSourceExtensions {
		if ext == strings.ToLower(sourceExt) {
			return true
		}
	}
	return false
}

// ServerCommand 返回项目型语言服务的启动命令副本。
// Args 会复制一份，避免调用方修改 adapter 内的配置模板。
func (a projectLanguageAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{Executable: a.command.Executable, Args: append([]string(nil), a.command.Args...)}, nil
}

// InitOptions 返回项目型语言服务的初始化选项副本。
// 选项来自配置，调用方不能通过返回值反向污染 adapter。
func (a projectLanguageAdapter) InitOptions(ResolvedLanguageScope) map[string]any {
	return cloneAnyMap(a.initOptions)
}

// EnvPolicy 返回项目型语言服务需要的最小环境覆盖。
// 默认继承 manager 统一筛选后的环境，只有 adapter 明确声明时才补充语言运行时路径。
func (a projectLanguageAdapter) EnvPolicy(scope ResolvedLanguageScope) []string {
	if a.envPolicy == nil {
		return nil
	}
	return a.envPolicy(scope)
}

func dotnetRootEnvPolicy(ResolvedLanguageScope) []string {
	if strings.TrimSpace(os.Getenv("DOTNET_ROOT")) != "" || strings.TrimSpace(os.Getenv("DOTNET_ROOT_ARM64")) != "" {
		return nil
	}
	for _, root := range dotnetRootCandidates() {
		if dotnetRootUsable(root) {
			return []string{"DOTNET_ROOT=" + root}
		}
	}
	return nil
}

func dotnetRootCandidates() []string {
	return []string{
		"/opt/homebrew/opt/dotnet/libexec",
		"/usr/local/opt/dotnet/libexec",
	}
}

// dotnetRootUsable 判断候选 DOTNET_ROOT 是否同时具备运行时和 SDK 目录。
func dotnetRootUsable(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	if info, err := os.Stat(filepath.Join(root, "shared", "Microsoft.NETCore.App")); err != nil || !info.IsDir() {
		return false
	}
	if info, err := os.Stat(filepath.Join(root, "sdk")); err != nil || !info.IsDir() {
		return false
	}
	return true
}

// BootstrapPolicy 声明项目型语言启动后的文档打开和首选源码扩展。
// ignoredDirNames 会复制返回，供 bootstrap 扫描时跳过依赖和构建产物。
func (a projectLanguageAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{
		OpenTarget:                     true,
		TreatMissingDiagnosticsAsEmpty: true,
		FirstSourceExtensions:          append([]string(nil), a.firstSourceExtensions...),
		IgnoredDirNames:                copyStringSet(a.ignoredDirNames),
	}
}

// CacheKeyParts 用 root kind 和语言 workspace root 区分项目型缓存。
// 同一 cwd 下 marker 命中与目录回退会得到不同 key。
func (a projectLanguageAdapter) CacheKeyParts(scope ResolvedLanguageScope) map[string]string {
	return map[string]string{
		"adapterRootKind": scope.RootKind,
		"adapterRoot":     scope.LanguageWorkspaceRoot,
	}
}

// CapabilityPolicy 声明项目型 adapter 需要真实 LSP client。
// 部分服务会在空 call hierarchy prepare 时重试，策略由配置注入。
func (a projectLanguageAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{
		RequiresLSPClient:              true,
		RetryEmptyCallHierarchyPrepare: a.retryEmptyCallHierarchyPrepare,
	}
}

type documentFallbackAdapter struct {
	languageIDs []string
}

// LanguageIDs 返回文档降级 adapter 覆盖的 language id 副本。
// 这些语言只支持 best-effort 文档符号，不会启动外部 LSP 进程。
func (a documentFallbackAdapter) LanguageIDs() []string {
	return append([]string(nil), a.languageIDs...)
}

// ResolveRoot 为文档降级路径生成最小 scope。
// 它只需要稳定的 workspace root，后续工具会走非 LSP 的符号提取。
func (a documentFallbackAdapter) ResolveRoot(_ context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error) {
	targetPath := firstNonEmpty(target, scope.TargetPath)
	root := scope.CWD
	if strings.TrimSpace(targetPath) != "" {
		root = filepath.Dir(targetPath)
	}
	normalized, err := normalizeRegistryWorkspaceRoot(root)
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	return ResolvedLanguageScope{
		LanguageID:            normalizeLanguageID(scope.LanguageID),
		WorkspaceRoot:         normalized,
		LanguageWorkspaceRoot: normalized,
		ProjectRoot:           normalized,
		RootKind:              "document_fallback",
	}, nil
}

// ServerCommand 对文档降级 adapter 返回空命令。
// 调用方应根据 capability policy 走 fallback，而不是尝试启动进程。
func (documentFallbackAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{}, nil
}

// InitOptions 对文档降级 adapter 不提供初始化选项。
// fallback 路径没有外部 server，因此无需发送 initialize payload。
func (documentFallbackAdapter) InitOptions(ResolvedLanguageScope) map[string]any { return nil }

// EnvPolicy 对文档降级 adapter 不追加环境覆盖。
// 没有进程启动时环境策略不参与执行。
func (documentFallbackAdapter) EnvPolicy(ResolvedLanguageScope) []string { return nil }

// BootstrapPolicy 对文档降级 adapter 返回空策略。
// fallback 符号读取不需要预打开文档。
func (documentFallbackAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{}
}

// CacheKeyParts 对文档降级 adapter 不追加语言专属维度。
// fallback 结果不写入 LSP 文档缓存。
func (documentFallbackAdapter) CacheKeyParts(ResolvedLanguageScope) map[string]string { return nil }

// CapabilityPolicy 声明该 adapter 只能走文档符号降级能力。
// 这阻止 manager 为它创建真实 LSP client。
func (documentFallbackAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{DocumentSymbolFallback: true}
}

// findProjectRoot 从目标路径向上查找包含任一 marker 的项目根。
// marker 未命中返回空字符串，调用方再决定是否走目录回退。
func findProjectRoot(path string, markers []string) (string, error) {
	absPath, err := platformNormalize(path)
	if err != nil {
		return "", err
	}
	startDir, err := resolveStartDir(absPath)
	if err != nil {
		return "", err
	}
	for dir := startDir; dir != "" && dir != "."; dir = filepath.Dir(dir) {
		if hasProjectMarker(dir, markers) {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", nil
}

func findProjectRootWithin(root string, markers []string, ignored map[string]struct{}) (string, error) {
	finder := &projectRootWithinFinder{markers: markerSet(markers), ignored: ignored}
	if err := filepath.WalkDir(root, finder.walk); err != nil {
		return "", err
	}
	return finder.result, nil
}

type projectRootWithinFinder struct {
	markers map[string]struct{}
	ignored map[string]struct{}
	result  string
}

func (f *projectRootWithinFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if d == nil {
		return nil
	}
	if d.IsDir() {
		return projectWalkDirDecision(d.Name(), f.ignored)
	}
	if _, ok := f.markers[d.Name()]; !ok {
		return nil
	}
	f.result = filepath.Dir(path)
	return filepath.SkipAll
}

func projectWalkDirDecision(name string, ignored map[string]struct{}) error {
	if shouldSkipDotOrUnderscoreDir(name) {
		return filepath.SkipDir
	}
	if _, ok := ignored[name]; ok {
		return filepath.SkipDir
	}
	return nil
}

func shouldSkipDotOrUnderscoreDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func findBootstrapFileWithin(root string, extensions []string, ignored map[string]struct{}) (string, error) {
	finder := &bootstrapFileWithinFinder{extensions: extensionSet(extensions), ignored: ignored}
	if err := filepath.WalkDir(root, finder.walk); err != nil {
		return "", err
	}
	return finder.result, nil
}

type bootstrapFileWithinFinder struct {
	extensions map[string]struct{}
	ignored    map[string]struct{}
	result     string
}

func (f *bootstrapFileWithinFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if d == nil {
		return nil
	}
	if d.IsDir() {
		return projectWalkDirDecision(d.Name(), f.ignored)
	}
	if _, ok := f.extensions[strings.ToLower(filepath.Ext(path))]; !ok {
		return nil
	}
	f.result = path
	return filepath.SkipAll
}

func hasProjectMarker(root string, markers []string) bool {
	for _, marker := range markers {
		if fileExists(filepath.Join(root, marker)) {
			return true
		}
	}
	return false
}

func markerSet(markers []string) map[string]struct{} {
	set := make(map[string]struct{}, len(markers))
	for _, marker := range markers {
		set[marker] = struct{}{}
	}
	return set
}

func extensionSet(extensions []string) map[string]struct{} {
	set := make(map[string]struct{}, len(extensions))
	for _, ext := range extensions {
		if normalized := strings.ToLower(strings.TrimSpace(ext)); normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	return set
}

func platformNormalize(path string) (string, error) {
	return normalizeOptionalPath(path, "")
}

func copyStringSet(input map[string]struct{}) map[string]struct{} {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]struct{}, len(input))
	for key := range input {
		output[key] = struct{}{}
	}
	return output
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]any, len(input))
	maps.Copy(output, input)
	return output
}

func defaultJDTLSInitOptions() map[string]any {
	return map[string]any{
		"settings": map[string]any{
			"java": map[string]any{
				"configuration": map[string]any{"updateBuildConfiguration": "automatic"},
				"import": map[string]any{
					"gradle": map[string]any{"enabled": true},
					"maven":  map[string]any{"enabled": true},
				},
			},
		},
		"extendedClientCapabilities": map[string]any{"classFileContentsSupport": true},
	}
}
