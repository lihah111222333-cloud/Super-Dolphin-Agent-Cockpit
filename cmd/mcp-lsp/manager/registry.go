package manager

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/installer"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

var ErrUnsupportedLanguage = errors.New("unsupported language for LSP toolchain")

var languageIDByBaseName = map[string]string{
	"go.mod":  "gomod",
	"go.sum":  "gosum",
	"go.work": "gowork",
}

var languageIDByExtension = map[string]string{
	".go":       "go",
	".js":       "javascript",
	".jsx":      "javascript",
	".mjs":      "javascript",
	".cjs":      "javascript",
	".ts":       "typescript",
	".tsx":      "typescript",
	".py":       "python",
	".pyi":      "python",
	".rs":       "rust",
	".java":     "java",
	".css":      "css",
	".md":       "markdown",
	".markdown": "markdown",
	".json":     "json",
	".yaml":     "yaml",
	".yml":      "yaml",
}

// Registry route requests to different LSP Managers based on file type.
type Registry interface {
	GetManagerForFile(ctx context.Context, filePath string) (Manager, error)
	GetManagerForLanguage(ctx context.Context, languageID string) (Manager, error)
	Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error)
	WaitDiagnosticsStable(ctx context.Context, uris []string) error
	CurrentDiagnosticGeneration() uint64
	BootstrapDocument(ctx context.Context, uri string) error
	Close() error
}

type languageConfig struct {
	manager Manager
}

type dynamicRegistry struct {
	mu        sync.RWMutex
	managers  map[string]*languageConfig // mapped by language ID
	installer *installer.Provider
}

func NewRegistry(inst *installer.Provider) *dynamicRegistry {
	return &dynamicRegistry{
		managers:  make(map[string]*languageConfig),
		installer: inst,
	}
}

func (r *dynamicRegistry) Register(languageID string, manager Manager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managers[strings.ToLower(languageID)] = &languageConfig{manager: manager}
}

func (r *dynamicRegistry) GetManagerForFile(ctx context.Context, filePath string) (Manager, error) {
	lang := DetectLanguageID(filePath)

	r.mu.RLock()
	config, ok := r.managers[lang]
	r.mu.RUnlock()

	if !ok {
		return nil, ErrUnsupportedLanguage
	}

	if r.installer != nil {
		if _, err := r.installer.EnsureInstalled(ctx, lang); err != nil {
			return nil, err
		}
	}

	return config.manager, nil
}

func (r *dynamicRegistry) GetManagerForLanguage(ctx context.Context, languageID string) (Manager, error) {
	lang := strings.ToLower(strings.TrimSpace(languageID))

	r.mu.RLock()
	config, ok := r.managers[lang]
	r.mu.RUnlock()

	if !ok {
		return nil, ErrUnsupportedLanguage
	}

	if r.installer != nil {
		if _, err := r.installer.EnsureInstalled(ctx, lang); err != nil {
			return nil, err
		}
	}

	return config.manager, nil
}

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

func (r *dynamicRegistry) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	var all []protocol.PublishDiagnosticsParams
	byManager, err := r.groupURIsByManager(ctx, uris)
	if err != nil {
		return nil, err
	}
	if len(byManager) == 0 && len(uris) == 0 {
		for _, mgr := range r.snapshotManagers() {
			items, err := mgr.Diagnostics(ctx, nil)
			if err != nil {
				return nil, err
			}
			all = append(all, items...)
		}
		return all, nil
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

func (r *dynamicRegistry) WaitDiagnosticsStable(ctx context.Context, uris []string) error {
	byManager, err := r.groupURIsByManager(ctx, uris)
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

func (r *dynamicRegistry) CurrentDiagnosticGeneration() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total uint64
	for _, cfg := range r.managers {
		total += cfg.manager.CurrentDiagnosticGeneration()
	}
	return total
}

func (r *dynamicRegistry) BootstrapDocument(ctx context.Context, uri string) error {
	path := strings.TrimPrefix(uri, "file://")
	mgr, err := r.GetManagerForFile(ctx, path)
	if err != nil {
		return err
	}
	return mgr.BootstrapDocument(ctx, uri)
}

func (r *dynamicRegistry) snapshotManagers() []Manager {
	r.mu.RLock()
	defer r.mu.RUnlock()

	managers := make([]Manager, 0, len(r.managers))
	for _, cfg := range r.managers {
		if cfg != nil && cfg.manager != nil {
			managers = append(managers, cfg.manager)
		}
	}
	return managers
}

func (r *dynamicRegistry) groupURIsByManager(ctx context.Context, uris []string) (map[Manager][]string, error) {
	result := make(map[Manager][]string)
	for _, uri := range uris {
		path := strings.TrimPrefix(uri, "file://")
		mgr, err := r.GetManagerForFile(ctx, path)
		if err != nil {
			if errors.Is(err, ErrUnsupportedLanguage) {
				continue
			}
			return nil, err
		}
		result[mgr] = append(result[mgr], uri)
	}
	return result, nil
}
