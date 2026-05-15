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

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
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

type ResolvedLanguageScope struct {
	LanguageID            string
	WorkspaceRoot         string
	LanguageWorkspaceRoot string
	ProjectRoot           string
	RootKind              string
	LanguageSpecific      map[string]string
	WorkspaceFolders      []protocol.WorkspaceFolder
}

type ServerCommand struct {
	Executable string
	Args       []string
}

type BootstrapPolicy struct {
	OpenTarget            bool
	OpenSiblingDocuments  bool
	SiblingExtensions     []string
	FirstSourceExtensions []string
	IgnoredDirNames       map[string]struct{}
}

type ToolCapabilityPolicy struct {
	RequiresLSPClient      bool
	DocumentSymbolFallback bool
}

type LanguageAdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]LanguageAdapter
}

func NewLanguageAdapterRegistry(adapters ...LanguageAdapter) *LanguageAdapterRegistry {
	registry := &LanguageAdapterRegistry{adapters: map[string]LanguageAdapter{}}
	for _, adapter := range adapters {
		registry.Register(adapter)
	}
	return registry
}

func NewDefaultLanguageAdapterRegistry() *LanguageAdapterRegistry {
	return NewLanguageAdapterRegistry(
		goLanguageAdapter{},
		projectLanguageAdapter{
			languageIDs:           []string{"javascript", "typescript", "javascriptreact", "typescriptreact"},
			command:               ServerCommand{Executable: "typescript-language-server", Args: []string{"--stdio"}},
			rootMarkers:           []string{"tsconfig.json", "jsconfig.json", "package.json"},
			rootKind:              "jsts_project",
			firstSourceExtensions: []string{".js", ".jsx", ".ts", ".tsx"},
			ignoredDirNames:       jstsIgnoredDirNames,
		},
		projectLanguageAdapter{
			languageIDs:           []string{"python"},
			command:               ServerCommand{Executable: "pyright-langserver", Args: []string{"--stdio"}},
			rootMarkers:           []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile"},
			rootKind:              "python_project",
			firstSourceExtensions: []string{".py", ".pyi"},
			ignoredDirNames:       pythonIgnoredDirNames,
		},
		projectLanguageAdapter{
			languageIDs:           []string{"rust"},
			command:               ServerCommand{Executable: "rust-analyzer"},
			rootMarkers:           []string{"Cargo.toml"},
			rootKind:              "rust_project",
			firstSourceExtensions: []string{".rs"},
			ignoredDirNames:       rustIgnoredDirNames,
		},
		projectLanguageAdapter{
			languageIDs:           []string{"java"},
			command:               ServerCommand{Executable: "jdtls"},
			rootMarkers:           javaProjectMarkers,
			rootKind:              "java_project",
			firstSourceExtensions: []string{".java"},
			ignoredDirNames:       javaIgnoredDirNames,
			initOptions:           defaultJDTLSInitOptions(),
		},
		projectLanguageAdapter{
			languageIDs:           []string{"css"},
			command:               ServerCommand{Executable: "vscode-css-language-server", Args: []string{"--stdio"}},
			rootMarkers:           []string{"package.json"},
			rootKind:              "css_project",
			firstSourceExtensions: []string{".css"},
			ignoredDirNames:       cssIgnoredDirNames,
		},
		documentFallbackAdapter{languageIDs: []string{"markdown", "json", "yaml"}},
	)
}

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

func (r *LanguageAdapterRegistry) AdapterForLanguage(languageID string) (LanguageAdapter, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[normalizeLanguageID(languageID)]
	return adapter, ok
}

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

type goLanguageAdapter struct{}

func (goLanguageAdapter) LanguageIDs() []string { return []string{"go", "gomod", "gosum", "gowork"} }

func (goLanguageAdapter) ResolveRoot(_ context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error) {
	languageID := normalizeLanguageID(scope.LanguageID)
	if languageID == "" {
		return ResolvedLanguageScope{}, fmt.Errorf("go adapter requires a resolved language ID")
	}
	info, err := ResolveGoRoot(GoRootRequest{
		CWD:      scope.CWD,
		FilePath: firstNonEmpty(target, scope.TargetPath, scope.CWD),
		Env:      os.Environ(),
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

func (goLanguageAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{Executable: "gopls"}, nil
}

func (goLanguageAdapter) InitOptions(ResolvedLanguageScope) map[string]any { return nil }

func (goLanguageAdapter) EnvPolicy(scope ResolvedLanguageScope) []string {
	mode := scope.LanguageSpecific["goworkMode"]
	switch mode {
	case goworkModeOff:
		return []string{"GOWORK=off"}
	case goworkModeAuto, goworkModeExplicit:
		if path := scope.LanguageSpecific["goWorkPath"]; path != "" {
			return []string{"GOWORK=" + path}
		}
	}
	return nil
}

func (goLanguageAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{
		OpenTarget:           true,
		OpenSiblingDocuments: true,
		SiblingExtensions:    []string{".go"},
	}
}

func (goLanguageAdapter) CacheKeyParts(scope ResolvedLanguageScope) map[string]string {
	return copyLanguageSpecific(scope.LanguageSpecific)
}

func (goLanguageAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{RequiresLSPClient: true}
}

type projectLanguageAdapter struct {
	languageIDs           []string
	command               ServerCommand
	rootMarkers           []string
	rootKind              string
	firstSourceExtensions []string
	ignoredDirNames       map[string]struct{}
	initOptions           map[string]any
}

func (a projectLanguageAdapter) LanguageIDs() []string {
	return append([]string(nil), a.languageIDs...)
}

func (a projectLanguageAdapter) ResolveRoot(_ context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error) {
	languageID := normalizeLanguageID(scope.LanguageID)
	if languageID == "" {
		return ResolvedLanguageScope{}, fmt.Errorf("adapter requires a resolved language ID")
	}
	root := firstNonEmpty(scope.CWD, filepath.Dir(firstNonEmpty(target, scope.TargetPath)))
	if markerRoot, err := findProjectRoot(firstNonEmpty(target, scope.TargetPath, root), a.rootMarkers); err != nil {
		return ResolvedLanguageScope{}, err
	} else if markerRoot != "" {
		root = markerRoot
	} else if nested := findProjectRootWithin(root, a.rootMarkers, a.ignoredDirNames); nested != "" {
		root = nested
	}
	normalized, err := normalizeRegistryWorkspaceRoot(root)
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	rootKind := a.rootKind
	if !hasProjectMarker(normalized, a.rootMarkers) {
		rootKind = "dir_fallback"
	}
	return ResolvedLanguageScope{
		LanguageID:            languageID,
		WorkspaceRoot:         normalized,
		LanguageWorkspaceRoot: normalized,
		ProjectRoot:           normalized,
		RootKind:              rootKind,
	}, nil
}

func (a projectLanguageAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{Executable: a.command.Executable, Args: append([]string(nil), a.command.Args...)}, nil
}

func (a projectLanguageAdapter) InitOptions(ResolvedLanguageScope) map[string]any {
	return cloneAnyMap(a.initOptions)
}

func (a projectLanguageAdapter) EnvPolicy(ResolvedLanguageScope) []string { return nil }

func (a projectLanguageAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{
		OpenTarget:            true,
		FirstSourceExtensions: append([]string(nil), a.firstSourceExtensions...),
		IgnoredDirNames:       copyStringSet(a.ignoredDirNames),
	}
}

func (a projectLanguageAdapter) CacheKeyParts(scope ResolvedLanguageScope) map[string]string {
	return map[string]string{
		"adapterRootKind": scope.RootKind,
		"adapterRoot":     scope.LanguageWorkspaceRoot,
	}
}

func (a projectLanguageAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{RequiresLSPClient: true}
}

type documentFallbackAdapter struct {
	languageIDs []string
}

func (a documentFallbackAdapter) LanguageIDs() []string {
	return append([]string(nil), a.languageIDs...)
}

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

func (documentFallbackAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{}, nil
}
func (documentFallbackAdapter) InitOptions(ResolvedLanguageScope) map[string]any { return nil }
func (documentFallbackAdapter) EnvPolicy(ResolvedLanguageScope) []string         { return nil }
func (documentFallbackAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{}
}
func (documentFallbackAdapter) CacheKeyParts(ResolvedLanguageScope) map[string]string { return nil }
func (documentFallbackAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{DocumentSymbolFallback: true}
}

var (
	pythonIgnoredDirNames = map[string]struct{}{
		".build-cache": {}, ".git": {}, ".mypy_cache": {}, ".pytest_cache": {},
		".ruff_cache": {}, ".venv": {}, ".workspace": {}, "__pycache__": {}, "node_modules": {}, "vendor": {},
	}
	rustIgnoredDirNames = map[string]struct{}{
		".build-cache": {}, ".git": {}, ".workspace": {}, "node_modules": {}, "target": {}, "vendor": {},
	}
	cssIgnoredDirNames = map[string]struct{}{
		".build-cache": {}, ".git": {}, ".workspace": {}, "dist": {}, "node_modules": {}, "vendor": {},
	}
)

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

func findProjectRootWithin(root string, markers []string, ignored map[string]struct{}) string {
	finder := &projectRootWithinFinder{markers: markerSet(markers), ignored: ignored}
	_ = filepath.WalkDir(root, finder.walk)
	return finder.result
}

type projectRootWithinFinder struct {
	markers map[string]struct{}
	ignored map[string]struct{}
	result  string
}

func (f *projectRootWithinFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil || d == nil {
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
	if strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}
	if _, ok := ignored[name]; ok {
		return filepath.SkipDir
	}
	return nil
}

func findBootstrapFileWithin(root string, extensions []string, ignored map[string]struct{}) string {
	finder := &bootstrapFileWithinFinder{extensions: extensionSet(extensions), ignored: ignored}
	_ = filepath.WalkDir(root, finder.walk)
	return finder.result
}

type bootstrapFileWithinFinder struct {
	extensions map[string]struct{}
	ignored    map[string]struct{}
	result     string
}

func (f *bootstrapFileWithinFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil || d == nil {
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
