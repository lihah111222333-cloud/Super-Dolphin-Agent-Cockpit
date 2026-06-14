package manager

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/installer"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

var (
	ErrUnsupportedLanguage   = errors.New("unsupported language for LSP toolchain")
	ErrUnsupportedCapability = errors.New("unsupported LSP capability")
)

var languageIDByBaseName = map[string]string{
	"go.mod":  "gomod",
	"go.sum":  "gosum",
	"go.work": "gowork",
}

var languageIDByExtension = map[string]string{
	".go":       "go",
	".js":       "javascript",
	".jsx":      "javascriptreact",
	".mjs":      "javascript",
	".cjs":      "javascript",
	".ts":       "typescript",
	".tsx":      "typescriptreact",
	".py":       "python",
	".pyi":      "python",
	".rs":       "rust",
	".java":     "java",
	".css":      "css",
	".sh":       "shellscript",
	".bash":     "shellscript",
	".zsh":      "shellscript",
	".ksh":      "shellscript",
	".bats":     "shellscript",
	".md":       "markdown",
	".markdown": "markdown",
	".json":     "json",
	".yaml":     "yaml",
	".yml":      "yaml",
}

// Registry route requests to different LSP Managers based on file type.
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

type languageConfig struct {
	manager       Manager
	scoped        ScopedManagerResolver
	skipInstaller bool
	binaryPath    string
}

type Installer interface {
	EnsureInstalled(ctx context.Context, languageID string) (string, error)
}

type InstallerWithDetails interface {
	EnsureInstalledDetailed(ctx context.Context, languageID string) (installer.InstallResult, error)
}

type BinaryPathSetter interface {
	SetBinaryPath(path string)
}

type dynamicRegistry struct {
	mu        sync.RWMutex
	managers  map[string]*languageConfig // mapped by language ID
	installer Installer
}

// NewRegistry 创建注册表。
func NewRegistry(inst *installer.Provider) *dynamicRegistry {
	if inst == nil {
		return NewRegistryWithInstaller(nil)
	}
	return NewRegistryWithInstaller(inst)
}

// NewRegistryWithInstaller 创建带installer的注册表。
func NewRegistryWithInstaller(inst Installer) *dynamicRegistry {
	return &dynamicRegistry{
		managers:  make(map[string]*languageConfig),
		installer: inst,
	}
}

// Register 注册LSP。
func (r *dynamicRegistry) Register(languageID string, manager Manager, scoped ...ScopedManagerResolver) {
	r.register(languageID, manager, false, scoped...)
}

// RegisterNoInstall 注册no安装。
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

// GetManagerForFile 为文件读取manager。
func (r *dynamicRegistry) GetManagerForFile(ctx context.Context, filePath string) (Manager, error) {
	scoped, err := r.ResolveManagerForFile(ctx, filePath)
	if err != nil {
		return nil, err
	}
	return scoped.Manager, nil
}

// ResolveManagerForFile 为文件解析manager。
func (r *dynamicRegistry) ResolveManagerForFile(ctx context.Context, filePath string) (ScopedManager, error) {
	return r.resolveManagerForTarget(ctx, DetectLanguageID(filePath), filePath, "")
}

// GetManagerForFileWithLanguage 读取带语言的manager文件。
func (r *dynamicRegistry) GetManagerForFileWithLanguage(ctx context.Context, filePath string, languageID string) (Manager, error) {
	scoped, err := r.ResolveManagerForFileWithLanguage(ctx, filePath, languageID)
	if err != nil {
		return nil, err
	}
	return scoped.Manager, nil
}

// ResolveManagerForFileWithLanguage 解析带语言的manager文件。
func (r *dynamicRegistry) ResolveManagerForFileWithLanguage(ctx context.Context, filePath string, languageID string) (ScopedManager, error) {
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

// GetManagerForLanguage 为语言读取manager。
func (r *dynamicRegistry) GetManagerForLanguage(ctx context.Context, languageID string) (Manager, error) {
	scoped, err := r.ResolveManagerForLanguage(ctx, languageID)
	if err != nil {
		return nil, err
	}
	return scoped.Manager, nil
}

// ResolveManagerForLanguage 为语言解析manager。
func (r *dynamicRegistry) ResolveManagerForLanguage(ctx context.Context, languageID string) (ScopedManager, error) {
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

// ensureInstalled 确保安装状态。
func (r *dynamicRegistry) ensureInstalled(ctx context.Context, lang string, config *languageConfig) error {
	if r.installer == nil || config == nil || config.skipInstaller {
		return nil
	}
	path, err := r.ensureInstalledPath(ctx, lang)
	if err != nil {
		return err
	}
	config.binaryPath = path
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

// scopedManagerForConfig 为配置处理scopedmanager。
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

// Close 关闭 LSP 管理器资源。
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
	if languageID, ok := languageIDByBaseName[strings.ToLower(filepath.Base(path))]; ok {
		return languageID
	}
	ext := strings.ToLower(filepath.Ext(path))
	if languageID, ok := languageIDByExtension[ext]; ok {
		return languageID
	}
	return strings.TrimPrefix(ext, ".")
}

// Diagnostics 汇总匹配 manager 返回的诊断。
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

// WaitDiagnosticsStable 等待诊断稳定状态。
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

// CurrentDiagnosticGeneration 处理当前诊断代际。
func (r *dynamicRegistry) CurrentDiagnosticGeneration() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total uint64
	for _, cfg := range r.managers {
		total += cfg.manager.CurrentDiagnosticGeneration()
	}
	return total
}

// BootstrapDocument 确保文档已打开并完成启动检查。
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

// addCurrentScopedManagers 添加当前scopedmanagers。
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

// groupURIsByManager 按manager处理groupuris。
func (r *dynamicRegistry) groupURIsByManager(ctx context.Context, uris []string) (map[Manager][]string, error) {
	result := make(map[Manager][]string)
	for _, uri := range uris {
		path := strings.TrimPrefix(uri, "file://")
		scoped, err := r.resolveManagerForTarget(ctx, DetectLanguageID(path), path, uri)
		if err != nil {
			if errors.Is(err, ErrUnsupportedLanguage) {
				continue
			}
			return nil, err
		}
		result[scoped.Manager] = append(result[scoped.Manager], uri)
	}
	return result, nil
}

// registryToolScope 处理注册表工具作用域。
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
