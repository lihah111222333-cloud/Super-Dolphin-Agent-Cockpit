package multilsp

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/protocol"
)

// LanguageAdapter owns every language-specific policy that the generic
// manager/pool/cache pipeline needs. The manager consumes only the resolved
// scope, command, env, bootstrap, cache, and capability outputs.
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

// ResolvedLanguageScope describes a multilsp API type.
type ResolvedLanguageScope struct {
	LanguageID            string
	WorkspaceRoot         string
	LanguageWorkspaceRoot string
	ProjectRoot           string
	RootKind              string
	LanguageSpecific      map[string]string
	WorkspaceFolders      []protocol.WorkspaceFolder
}

// ServerCommand describes a multilsp API type.
type ServerCommand struct {
	Executable string
	Args       []string
}

// BootstrapPolicy describes a multilsp API type.
type BootstrapPolicy struct {
	OpenTarget            bool
	OpenSiblingDocuments  bool
	SiblingExtensions     []string
	FirstSourceExtensions []string
	IgnoredDirNames       map[string]struct{}
}

// ToolCapabilityPolicy describes a multilsp API type.
type ToolCapabilityPolicy struct {
	RequiresLSPClient              bool
	DocumentSymbolFallback         bool
	RetryEmptyCallHierarchyPrepare bool
}

// LanguageAdapterRegistry describes a multilsp API type.
type LanguageAdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]LanguageAdapter
}

// NewLanguageAdapterRegistry 创建语言适配器注册表。
func NewLanguageAdapterRegistry(adapters ...LanguageAdapter) *LanguageAdapterRegistry {
	registry := &LanguageAdapterRegistry{adapters: map[string]LanguageAdapter{}}
	for _, adapter := range adapters {
		registry.Register(adapter)
	}
	return registry
}

// NewDefaultLanguageAdapterRegistry 创建default语言适配器注册表。
func NewDefaultLanguageAdapterRegistry() *LanguageAdapterRegistry {
	return NewLanguageAdapterRegistryFromConfig(platformconfig.DefaultLSPConfig())
}

// Register 注册LSP。
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

// AdapterForLanguage 为语言处理适配器。
func (r *LanguageAdapterRegistry) AdapterForLanguage(languageID string) (LanguageAdapter, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[normalizeLanguageID(languageID)]
	return adapter, ok
}

// LanguageIDs 处理语言ids。
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

// LanguageIDs 处理语言ids。
func (goLanguageAdapter) LanguageIDs() []string { return []string{"go", "gomod", "gosum", "gowork"} }

// ResolveRoot 解析根目录。
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
	return ResolvedLanguageScope{
		LanguageID:            languageID,
		WorkspaceRoot:         parts.WorkspaceRoot,
		LanguageWorkspaceRoot: parts.LanguageWorkspaceRoot,
		ProjectRoot:           parts.ProjectRoot,
		RootKind:              parts.RootKind,
		LanguageSpecific:      copyLanguageSpecific(parts.LanguageSpecific),
		WorkspaceFolders:      workspaceFolders(info),
	}, nil
}

// ServerCommand 处理服务端命令。
func (goLanguageAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{Executable: "gopls"}, nil
}

// InitOptions 处理init选项。
func (a goLanguageAdapter) InitOptions(ResolvedLanguageScope) map[string]any {
	return map[string]any{
		"semanticTokens":   true,
		"directoryFilters": a.resolvedDirectoryFilters(),
	}
}

func (a goLanguageAdapter) resolvedDirectoryFilters() []string {
	filters := a.directoryFilters
	if len(filters) == 0 {
		filters = platformconfig.DefaultLSPConfig().GoDirectoryFilters
	}
	return slices.Clone(filters)
}

// EnvPolicy 处理env策略。
func (goLanguageAdapter) EnvPolicy(scope ResolvedLanguageScope) []string {
	env := make([]string, 0, 3)
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
	if len(env) == 0 {
		return nil
	}
	return env
}

// BootstrapPolicy 处理启动策略。
func (goLanguageAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{
		OpenTarget: true,
	}
}

// CacheKeyParts 处理缓存键parts。
func (goLanguageAdapter) CacheKeyParts(scope ResolvedLanguageScope) map[string]string {
	return copyLanguageSpecific(scope.LanguageSpecific)
}

// CapabilityPolicy 处理capability策略。
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
	retryEmptyCallHierarchyPrepare bool
}

// LanguageIDs 处理语言ids。
func (a projectLanguageAdapter) LanguageIDs() []string {
	return append([]string(nil), a.languageIDs...)
}

// ResolveRoot 解析根目录。
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

// shouldSearchNestedProjectRoot 判断searchnested项目根目录是否可用。
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

// ServerCommand 处理服务端命令。
func (a projectLanguageAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{Executable: a.command.Executable, Args: append([]string(nil), a.command.Args...)}, nil
}

// InitOptions 处理init选项。
func (a projectLanguageAdapter) InitOptions(ResolvedLanguageScope) map[string]any {
	return cloneAnyMap(a.initOptions)
}

// EnvPolicy 处理env策略。
func (a projectLanguageAdapter) EnvPolicy(ResolvedLanguageScope) []string { return nil }

// BootstrapPolicy 处理启动策略。
func (a projectLanguageAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{
		OpenTarget:            true,
		FirstSourceExtensions: append([]string(nil), a.firstSourceExtensions...),
		IgnoredDirNames:       copyStringSet(a.ignoredDirNames),
	}
}

// CacheKeyParts 处理缓存键parts。
func (a projectLanguageAdapter) CacheKeyParts(scope ResolvedLanguageScope) map[string]string {
	return map[string]string{
		"adapterRootKind": scope.RootKind,
		"adapterRoot":     scope.LanguageWorkspaceRoot,
	}
}

// CapabilityPolicy 处理capability策略。
func (a projectLanguageAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{
		RequiresLSPClient:              true,
		RetryEmptyCallHierarchyPrepare: a.retryEmptyCallHierarchyPrepare,
	}
}

type documentFallbackAdapter struct {
	languageIDs []string
}

// LanguageIDs 处理语言ids。
func (a documentFallbackAdapter) LanguageIDs() []string {
	return append([]string(nil), a.languageIDs...)
}

// ResolveRoot 解析根目录。
func (a documentFallbackAdapter) ResolveRoot(_ context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error) {
	root := firstNonEmpty(scope.CWD, filepath.Dir(firstNonEmpty(target, scope.TargetPath)))
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

// ServerCommand 处理服务端命令。
func (documentFallbackAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{}, nil
}

// InitOptions 处理init选项。
func (documentFallbackAdapter) InitOptions(ResolvedLanguageScope) map[string]any { return nil }

// EnvPolicy 处理env策略。
func (documentFallbackAdapter) EnvPolicy(ResolvedLanguageScope) []string { return nil }

// BootstrapPolicy 处理启动策略。
func (documentFallbackAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{}
}

// CacheKeyParts 处理缓存键parts。
func (documentFallbackAdapter) CacheKeyParts(ResolvedLanguageScope) map[string]string { return nil }

// CapabilityPolicy 处理capability策略。
func (documentFallbackAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{DocumentSymbolFallback: true}
}

// findProjectRoot 查找项目根目录。
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
