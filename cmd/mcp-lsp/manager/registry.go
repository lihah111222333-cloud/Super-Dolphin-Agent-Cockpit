package manager

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

var (
	ErrUnsupportedLanguage   = errors.New("unsupported language for LSP toolchain")
	ErrUnsupportedCapability = errors.New("unsupported LSP capability")
)

var languageIDByBaseName = map[string]string{
	"containerfile": "dockerfile",
	"dockerfile":    "dockerfile",
	"go.mod":        "gomod",
	"go.sum":        "gosum",
	"go.work":       "gowork",
}

var gitHookShellBaseNames = map[string]struct{}{
	"applypatch-msg":        {},
	"commit-msg":            {},
	"fsmonitor-watchman":    {},
	"post-applypatch":       {},
	"post-checkout":         {},
	"post-commit":           {},
	"post-index-change":     {},
	"post-merge":            {},
	"post-receive":          {},
	"post-rewrite":          {},
	"post-update":           {},
	"pre-applypatch":        {},
	"pre-auto-gc":           {},
	"pre-commit":            {},
	"pre-merge-commit":      {},
	"pre-push":              {},
	"pre-rebase":            {},
	"pre-receive":           {},
	"prepare-commit-msg":    {},
	"proc-receive":          {},
	"push-to-checkout":      {},
	"reference-transaction": {},
	"sendemail-validate":    {},
	"update":                {},
}

var languageIDByExtension = map[string]string{
	".go":         "go",
	".js":         "javascript",
	".jsx":        "javascriptreact",
	".mjs":        "javascript",
	".cjs":        "javascript",
	".ts":         "typescript",
	".tsx":        "typescriptreact",
	".html":       "html",
	".htm":        "html",
	".vue":        "vue",
	".svelte":     "svelte",
	".c":          "c",
	".cc":         "cpp",
	".cpp":        "cpp",
	".cxx":        "cpp",
	".h":          "c",
	".hh":         "cpp",
	".hpp":        "cpp",
	".hxx":        "cpp",
	".m":          "objective-c",
	".mm":         "objective-cpp",
	".swift":      "swift",
	".cs":         "csharp",
	".py":         "python",
	".pyi":        "python",
	".php":        "php",
	".phtml":      "php",
	".rb":         "ruby",
	".rake":       "ruby",
	".kt":         "kotlin",
	".kts":        "kotlin",
	".dart":       "dart",
	".lua":        "lua",
	".rs":         "rust",
	".java":       "java",
	".css":        "css",
	".scss":       "css",
	".sass":       "css",
	".less":       "css",
	".sh":         "shellscript",
	".bash":       "shellscript",
	".zsh":        "shellscript",
	".ksh":        "shellscript",
	".bats":       "shellscript",
	".dockerfile": "dockerfile",
	".tf":         "terraform",
	".tfvars":     "terraform",
	".graphql":    "graphql",
	".gql":        "graphql",
	".prisma":     "prisma",
	".md":         "markdown",
	".markdown":   "markdown",
	".jsonc":      "json",
	".json":       "json",
	".yaml":       "yaml",
	".yml":        "yaml",
}

// Registry 根据语言和目标文件把工具请求路由到对应 LSP Manager。
// 它同时负责安装校验、诊断聚合和启动同步，是工具层进入 LSP runtime 的跨模块入口。
type Registry interface {
	GetManagerForFile(ctx context.Context, filePath string) (Manager, error)
	GetManagerForFileWithLanguage(ctx context.Context, filePath string, languageID string) (Manager, error)
	GetManagerForLanguage(ctx context.Context, languageID string) (Manager, error)
	Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error)
	WaitDiagnosticsStable(ctx context.Context, uris []string) error
	CurrentDiagnosticGeneration() uint64
	BootstrapDocument(ctx context.Context, uri string) error
	Close() error
}

// DiagnosticsReopenRegistry 为显式 diagnostics 请求按 manager 重开目标文档。
type DiagnosticsReopenRegistry interface {
	ReopenDocumentsForDiagnostics(ctx context.Context, uris []string) error
}

// languageConfig 保存单个 language id 的 manager 绑定和安装策略。
// scoped 非空时由生产 ManagerPool 决定具体 workspace/shard manager。
type languageConfig struct {
	manager       Manager
	scoped        ScopedManagerResolver
	skipInstaller bool
}

// Installer 是注册表依赖的最小语言服务安装接口。
type Installer interface {
	EnsureInstalled(ctx context.Context, languageID string) (string, error)
}

// InstallerWithDetails 可返回安装来源状态，便于注册表记录最终二进制路径。
type InstallerWithDetails interface {
	EnsureInstalledDetailed(ctx context.Context, languageID string) (installer.InstallResult, error)
}

// BinaryPathSetter 允许 manager 接收安装器解析出的 LSP 二进制路径。
type BinaryPathSetter interface {
	SetBinaryPath(path string)
}

type dynamicRegistry struct {
	mu        sync.RWMutex
	managers  map[string]*languageConfig // mapped by language ID
	installer Installer
}

type registryResolver = dynamicRegistry

// UnsupportedDiagnosticsFilesError 标记显式诊断请求里无法路由到 LSP 的文件。
// 它保留 ErrUnsupportedLanguage 作为 unwrap，工具层可据此组装 error envelope。
type UnsupportedDiagnosticsFilesError struct {
	Files []string
}

// Error 返回包含逐文件 unsupported 列表的诊断路由错误。
func (e *UnsupportedDiagnosticsFilesError) Error() string {
	return fmt.Sprintf("%s: unsupported_files=%q", ErrUnsupportedLanguage, e.Files)
}

// Unwrap 暴露 ErrUnsupportedLanguage，便于调用方用 errors.Is 分类处理。
func (e *UnsupportedDiagnosticsFilesError) Unwrap() error {
	return ErrUnsupportedLanguage
}

// NewRegistry 初始化带默认安装器的动态注册表。
// 生产路径会通过安装器校验语言服务二进制，再把请求路由给对应 manager。
func NewRegistry(inst *installer.Provider) *dynamicRegistry {
	if inst == nil {
		return NewRegistryWithInstaller(nil)
	}
	return NewRegistryWithInstaller(inst)
}

// NewRegistryWithInstaller 初始化可注入安装器的动态注册表。
// 测试可传 nil 关闭安装流程，生产路径会在解析 manager 前校验二进制。
func NewRegistryWithInstaller(inst Installer) *dynamicRegistry {
	return &dynamicRegistry{
		managers:  make(map[string]*languageConfig),
		installer: inst,
	}
}

// Register 为需要安装校验的语言登记 manager。
// 首次解析该语言时会运行安装器，失败会阻断工具调用。
func (r *dynamicRegistry) Register(languageID string, manager Manager, scoped ...ScopedManagerResolver) {
	r.register(languageID, manager, false, scoped...)
}

// RegisterNoInstall 为无需二进制校验的语言登记 manager。
// 适用于内建或文档型语言服务，避免为无需二进制的 adapter 触发安装器。
func (r *dynamicRegistry) RegisterNoInstall(languageID string, manager Manager, scoped ...ScopedManagerResolver) {
	r.register(languageID, manager, true, scoped...)
}

func (r *dynamicRegistry) register(languageID string, manager Manager, skipInstaller bool, scoped ...ScopedManagerResolver) {
	var resolver ScopedManagerResolver
	for _, candidate := range scoped {
		if candidate != nil {
			resolver = candidate
			break
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managers[strings.ToLower(languageID)] = &languageConfig{manager: manager, scoped: resolver, skipInstaller: skipInstaller}
}

// GetManagerForFile 根据文件名推断语言并解析 manager。
// 仅返回 manager 本体，调用方不需要 scope 元数据时使用。
func (r *dynamicRegistry) GetManagerForFile(ctx context.Context, filePath string) (Manager, error) {
	scoped, err := r.ResolveManagerForFile(ctx, filePath)
	if err != nil {
		return nil, err
	}
	return scoped.Manager, nil
}

// ResolveManagerForFile 根据文件名推断语言并解析 scoped manager。
// scoped 结果会携带 ManagerPool 解析出的缓存和诊断作用域。
func (r *registryResolver) ResolveManagerForFile(ctx context.Context, filePath string) (ScopedManager, error) {
	return r.resolveManagerForTarget(ctx, DetectLanguageID(filePath), filePath, "")
}

// GetManagerForFileWithLanguage 使用显式 language id 解析文件 manager。
// languageID 为空时仍回退到文件名推断，保持旧调用兼容。
func (r *dynamicRegistry) GetManagerForFileWithLanguage(ctx context.Context, filePath string, languageID string) (Manager, error) {
	scoped, err := r.ResolveManagerForFileWithLanguage(ctx, filePath, languageID)
	if err != nil {
		return nil, err
	}
	return scoped.Manager, nil
}

// ResolveManagerForFileWithLanguage 返回显式语言下的 scoped manager。
// 它会在返回前执行安装校验和 ManagerPool scope 解析。
func (r *registryResolver) ResolveManagerForFileWithLanguage(ctx context.Context, filePath string, languageID string) (ScopedManager, error) {
	lang := strings.ToLower(strings.TrimSpace(languageID))
	if lang == "" {
		lang = DetectLanguageID(filePath)
	}
	return r.resolveManagerForTarget(ctx, lang, filePath, "")
}

func (r *dynamicRegistry) resolveManagerForTarget(ctx context.Context, lang, targetPath, targetURI string) (ScopedManager, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))

	r.mu.RLock()
	config, ok := r.managers[lang]
	r.mu.RUnlock()

	if !ok {
		return ScopedManager{}, ErrUnsupportedLanguage
	}

	if err := r.ensureInstalled(ctx, lang, config); err != nil {
		return ScopedManager{}, err
	}

	return r.scopedManagerForConfig(ctx, config, lang, targetPath, targetURI)
}

// GetManagerForLanguage 按 language id 解析 manager。
// 调用方已经知道语言时使用，仍会执行安装校验。
func (r *dynamicRegistry) GetManagerForLanguage(ctx context.Context, languageID string) (Manager, error) {
	scoped, err := r.ResolveManagerForLanguage(ctx, languageID)
	if err != nil {
		return nil, err
	}
	return scoped.Manager, nil
}

// ResolveManagerForLanguage 按 language id 解析 scoped manager。
// 没有目标文件时仍会构造可信工具 scope，用于诊断和缓存审计。
func (r *registryResolver) ResolveManagerForLanguage(ctx context.Context, languageID string) (ScopedManager, error) {
	lang := strings.ToLower(strings.TrimSpace(languageID))

	r.mu.RLock()
	config, ok := r.managers[lang]
	r.mu.RUnlock()

	if !ok {
		return ScopedManager{}, ErrUnsupportedLanguage
	}

	if err := r.ensureInstalled(ctx, lang, config); err != nil {
		return ScopedManager{}, err
	}

	return r.scopedManagerForConfig(ctx, config, lang, "", "")
}

// ensureInstalled 在首次解析 manager 前确认语言服务二进制可用。
// skipInstaller 或无安装器时直接返回；安装失败会阻断工具调用。
func (r *dynamicRegistry) ensureInstalled(ctx context.Context, lang string, config *languageConfig) error {
	if r.installer == nil || config == nil || config.skipInstaller {
		return nil
	}
	path, err := r.ensureInstalledPath(ctx, lang)
	if err != nil {
		return err
	}
	if setter, ok := config.manager.(BinaryPathSetter); ok {
		setter.SetBinaryPath(path)
	}
	return nil
}

func (r *dynamicRegistry) ensureInstalledPath(ctx context.Context, lang string) (string, error) {
	if detailed, ok := r.installer.(InstallerWithDetails); ok {
		result, err := detailed.EnsureInstalledDetailed(ctx, lang)
		if err != nil {
			return "", err
		}
		return result.Path, nil
	}
	return r.installer.EnsureInstalled(ctx, lang)
}

// scopedManagerForConfig 根据语言配置和可信工具上下文构造 scoped manager。
// 没有 scoped resolver 时返回静态 manager，有 resolver 时交给 ManagerPool 选择实例。
func (r *dynamicRegistry) scopedManagerForConfig(ctx context.Context, config *languageConfig, lang, targetPath, targetURI string) (ScopedManager, error) {
	if config == nil || config.manager == nil {
		return ScopedManager{}, ErrUnsupportedLanguage
	}
	scope, err := registryToolScope(ctx, lang, targetPath, targetURI)
	if err != nil {
		return ScopedManager{}, err
	}
	if config.scoped == nil {
		return ScopedManager{Manager: config.manager, ResolvedScope: ResolvedToolScope{ToolScope: scope}}, nil
	}
	scoped, err := config.scoped.ForToolScope(scope)
	if err != nil {
		return ScopedManager{}, err
	}
	scoped.Manager = ManagerWithResolvedScope(scoped.Manager, scoped.ResolvedScope)
	return scoped, nil
}

// Close 关闭全部已注册 manager 并清空注册表。
// 多个关闭错误会合并返回，调用方可一次性看到所有资源释放失败。
func (r *dynamicRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for _, cfg := range r.managers {
		if err := cfg.manager.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	r.managers = make(map[string]*languageConfig)
	return errors.Join(errs...)
}

// DetectLanguageID 根据文件名或扩展名判断 LSP language id。
func DetectLanguageID(path string) string {
	base := strings.ToLower(filepath.Base(path))
	if languageID, ok := languageIDByBaseName[base]; ok {
		return languageID
	}
	if isGitHookShellPath(path, base) {
		return "shellscript"
	}
	ext := strings.ToLower(filepath.Ext(path))
	if languageID, ok := languageIDByExtension[ext]; ok {
		return languageID
	}
	return strings.TrimPrefix(ext, ".")
}

func isGitHookShellPath(path string, base string) bool {
	dir := "/" + filepath.ToSlash(strings.ToLower(filepath.Dir(path))) + "/"
	if strings.Contains(dir, "/.githooks/") {
		return filepath.Ext(base) == ""
	}
	if !strings.Contains(dir, "/.git/hooks/") {
		return false
	}
	_, ok := gitHookShellBaseNames[base]
	return ok
}

// Diagnostics 按 manager 分组读取诊断后合并结果。
// uris 为空时会遍历当前 scoped managers，覆盖已打开文档的诊断面。
func (r *dynamicRegistry) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	var all []protocol.PublishDiagnosticsParams
	byManager, err := r.managersForDiagnostics(ctx, uris)
	if err != nil {
		return nil, err
	}
	for mgr, subset := range byManager {
		items, err := mgr.Diagnostics(ctx, subset)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

// ReopenDocumentsForDiagnostics 按 manager 分组强制重开显式诊断目标。
// 测试 registry 可实现窄接口；生产 registry 直接复用内部诊断路由，避免扩大核心结构体方法面。
func ReopenDocumentsForDiagnostics(ctx context.Context, registry Registry, uris []string) error {
	if reopener, ok := registry.(DiagnosticsReopenRegistry); ok {
		return reopener.ReopenDocumentsForDiagnostics(ctx, uris)
	}
	r, ok := registry.(*dynamicRegistry)
	if !ok {
		return fmt.Errorf("%w: diagnostics document reopen registry", ErrUnsupportedCapability)
	}
	byManager, err := r.managersForDiagnostics(ctx, uris)
	if err != nil {
		return err
	}
	for manager, subset := range byManager {
		reopener, ok := manager.(DiagnosticDocumentReopener)
		if !ok {
			return fmt.Errorf("%w: diagnostics document reopen", ErrUnsupportedCapability)
		}
		for _, uri := range subset {
			if err := reopener.ReopenDocumentForDiagnostics(ctx, uri); err != nil {
				return fmt.Errorf("reopen diagnostics document %s: %w", uri, err)
			}
		}
	}
	return nil
}

// WaitDiagnosticsStable 等待目标 URI 所属 manager 的诊断稳定。
// 任一 manager 返回错误都会立即上抛，避免把未就绪状态当作空诊断。
func (r *dynamicRegistry) WaitDiagnosticsStable(ctx context.Context, uris []string) error {
	byManager, err := r.managersForDiagnostics(ctx, uris)
	if err != nil {
		return err
	}
	for mgr, subset := range byManager {
		if err := mgr.WaitDiagnosticsStable(ctx, subset); err != nil {
			return err
		}
	}
	return nil
}

// CurrentDiagnosticGeneration 汇总所有 manager 的诊断代际。
// 该值用于检测是否还有异步诊断刷新，不能作为持久化 ID。
func (r *dynamicRegistry) CurrentDiagnosticGeneration() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total uint64
	for _, cfg := range r.managers {
		total += cfg.manager.CurrentDiagnosticGeneration()
	}
	return total
}

// BootstrapDocument 根据 URI 选择 manager 并执行文档启动同步。
// 语言不支持会直接返回错误，避免调用方误以为 LSP 已准备好。
func (r *dynamicRegistry) BootstrapDocument(ctx context.Context, uri string) error {
	path := strings.TrimPrefix(uri, "file://")
	scoped, err := r.resolveManagerForTarget(ctx, DetectLanguageID(path), path, uri)
	if err != nil {
		return err
	}
	return scoped.Manager.BootstrapDocument(ctx, uri)
}

func (r *dynamicRegistry) managersForDiagnostics(ctx context.Context, uris []string) (map[Manager][]string, error) {
	if len(uris) == 0 {
		return r.currentScopedManagers(ctx)
	}
	return r.groupURIsByManager(ctx, uris)
}

func (r *dynamicRegistry) currentScopedManagers(ctx context.Context) (map[Manager][]string, error) {
	result := make(map[Manager][]string)
	seenScopedKeys := map[string]struct{}{}
	configs := r.snapshotLanguageConfigs()
	for lang, cfg := range configs {
		if err := r.addCurrentScopedManagers(ctx, result, seenScopedKeys, lang, cfg); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// addCurrentScopedManagers 收集某个语言当前活跃的 scoped managers。
// seen 用 manager/scope key 去重，避免同一实例在诊断聚合中重复读取。
func (r *dynamicRegistry) addCurrentScopedManagers(ctx context.Context, result map[Manager][]string, seen map[string]struct{}, lang string, cfg *languageConfig) error {
	if cfg == nil || cfg.manager == nil {
		return nil
	}
	if cfg.scoped == nil {
		result[cfg.manager] = nil
		return nil
	}
	scope, err := registryToolScope(ctx, lang, "", "")
	if err != nil {
		return err
	}
	scopedManagers, err := cfg.scoped.CurrentManagersForToolScope(scope)
	if err != nil {
		return err
	}
	for _, scoped := range scopedManagers {
		if scoped.Manager == nil || scopedManagerSeen(seen, scoped.ResolvedScope) {
			continue
		}
		result[ManagerWithResolvedScope(scoped.Manager, scoped.ResolvedScope)] = nil
	}
	return nil
}

func scopedManagerSeen(seen map[string]struct{}, scope ResolvedToolScope) bool {
	dedupeKey := scope.ManagerKey
	if dedupeKey == "" {
		dedupeKey = scope.ScopeKey + "\x00" + scope.WorkspaceKey
	}
	if dedupeKey == "" {
		return false
	}
	if _, ok := seen[dedupeKey]; ok {
		return true
	}
	seen[dedupeKey] = struct{}{}
	return false
}

func (r *dynamicRegistry) snapshotLanguageConfigs() map[string]*languageConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	configs := make(map[string]*languageConfig, len(r.managers))
	maps.Copy(configs, r.managers)
	return configs
}

// groupURIsByManager 将 URI 分配给支持其语言的 manager。
// 不支持的语言会跳过，其他错误直接返回给 diagnostics 调用方。
func (r *dynamicRegistry) groupURIsByManager(ctx context.Context, uris []string) (map[Manager][]string, error) {
	result := make(map[Manager][]string)
	unsupported := make([]string, 0)
	for _, uri := range uris {
		path := strings.TrimPrefix(uri, "file://")
		scoped, err := r.resolveManagerForTarget(ctx, DetectLanguageID(path), path, uri)
		if err != nil {
			if errors.Is(err, ErrUnsupportedLanguage) {
				unsupported = append(unsupported, uri)
				continue
			}
			return nil, err
		}
		result[scoped.Manager] = append(result[scoped.Manager], uri)
	}
	if len(unsupported) > 0 {
		return nil, &UnsupportedDiagnosticsFilesError{Files: unsupported}
	}
	return result, nil
}

// registryToolScope 从可信上下文构造注册表路由 scope。
// CWD 缺失时必须从 context 严格解析 workspace root，不能使用进程当前目录兜底。
func registryToolScope(ctx context.Context, lang, targetPath, targetURI string) (ToolScope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	trusted, _ := common.ToolScopeFromContext(ctx)
	if trusted.CWD == "" {
		root, err := common.WorkspaceRootFromContextStrict(ctx)
		if err != nil {
			return ToolScope{}, err
		}
		trusted.CWD = root
	}
	if trusted.Family == "" {
		trusted.Family = "lsp"
	}
	return ToolScope{
		AgentID:  trusted.AgentID,
		ThreadID: trusted.ThreadID,
		TurnID:   trusted.TurnID,
		CallID:   trusted.CallID,
		CWD:      trusted.CWD,
		WorkspaceRoots: append(
			[]string(nil),
			trusted.WorkspaceRoots...,
		),
		Family:     trusted.Family,
		LanguageID: strings.ToLower(strings.TrimSpace(lang)),
		TargetPath: strings.TrimSpace(targetPath),
		TargetURI:  strings.TrimSpace(targetURI),
	}, nil
}
