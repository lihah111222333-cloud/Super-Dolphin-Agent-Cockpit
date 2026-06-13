package modelregistry

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"gopkg.in/yaml.v3"
)

const DefaultPath = "cmd/mcp-orch/tools/modelregistry/models.yaml"
const EnvRegistryPath = "SUPER_DOLPHIN_MODEL_REGISTRY"

type ProviderModels struct {
	Provider          string   `json:"provider" yaml:"provider"`
	Models            []string `json:"models" yaml:"models"`
	Available         *bool    `json:"available,omitempty" yaml:"-"`
	UnavailableReason string   `json:"unavailable_reason,omitempty" yaml:"-"`
}

type Registry interface {
	ListProviders() ([]ProviderModels, error)
	LookupProvider(name string) (ProviderModels, bool, error)
}

type FileRegistry struct {
	path   string
	logger *slog.Logger

	mu        sync.RWMutex
	providers []ProviderModels
}

type StaticRegistry struct {
	providers []ProviderModels
}

type fileConfig struct {
	Providers []ProviderModels `yaml:"providers"`
}

type FileRegistryOption func(*fileRegistryConfig)

type fileRegistryConfig struct {
	logger *slog.Logger
}

// WithLogger 设置日志器。
func WithLogger(logger *slog.Logger) FileRegistryOption {
	return func(cfg *fileRegistryConfig) {
		cfg.logger = logger
	}
}

// NewDefaultRegistry 创建default注册表。
func NewDefaultRegistry(opts ...FileRegistryOption) (*FileRegistry, error) {
	return NewFileRegistry(DefaultRegistryPath(), opts...)
}

// NewFileRegistry 创建文件注册表。
func NewFileRegistry(path string, opts ...FileRegistryOption) (*FileRegistry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("model registry path is empty")
	}
	cfg := fileRegistryConfig{logger: pkglogger.Get()}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.logger == nil {
		cfg.logger = pkglogger.Get()
	}
	registry := &FileRegistry{path: path, logger: cfg.logger}
	if err := registry.Reload(); err != nil {
		return nil, err
	}
	return registry, nil
}

// NewStaticRegistry 创建static注册表。
func NewStaticRegistry(providers []ProviderModels) StaticRegistry {
	return StaticRegistry{providers: cloneProviders(providers)}
}

// ListProviders 列出providers。
func (r *FileRegistry) ListProviders() ([]ProviderModels, error) {
	if err := r.Reload(); err != nil {
		return nil, err
	}
	return r.snapshot(), nil
}

// LookupProvider 按 provider 名称查找模型配置。
func (r *FileRegistry) LookupProvider(name string) (ProviderModels, bool, error) {
	if err := r.Reload(); err != nil {
		return ProviderModels{}, false, err
	}
	provider, ok := lookupProvider(r.snapshot(), name)
	return provider, ok, nil
}

// Path 处理路径。
func (r *FileRegistry) Path() string {
	return r.path
}

// Reload 重新加载注册表配置。
func (r *FileRegistry) Reload() error {
	providers, err := loadFile(r.path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.providers = providers
	r.mu.Unlock()
	return nil
}

func (r *FileRegistry) snapshot() []ProviderModels {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneProviders(r.providers)
}

// ListProviders 列出providers。
func (r StaticRegistry) ListProviders() ([]ProviderModels, error) {
	return cloneProviders(r.providers), nil
}

// LookupProvider 按 provider 名称查找模型配置。
func (r StaticRegistry) LookupProvider(name string) (ProviderModels, bool, error) {
	provider, ok := lookupProvider(r.providers, name)
	return provider, ok, nil
}

// DefaultRegistryPath 返回默认注册表路径。
func DefaultRegistryPath() string {
	if path := strings.TrimSpace(os.Getenv(EnvRegistryPath)); path != "" {
		return path
	}
	if fileExists(DefaultPath) {
		return DefaultPath
	}
	if fileExists("models.yaml") {
		return "models.yaml"
	}
	return findDefaultPathFromWorkingDir()
}

func loadFile(path string) ([]ProviderModels, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read model registry %s: %w", path, err)
	}
	return parse(raw)
}

func parse(raw []byte) ([]ProviderModels, error) {
	var cfg fileConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse model registry yaml: %w", err)
	}
	if err := validateProviders(cfg.Providers); err != nil {
		return nil, err
	}
	return cloneProviders(cfg.Providers), nil
}

// validateProviders 校验providers。
func validateProviders(providers []ProviderModels) error {
	if len(providers) == 0 {
		return fmt.Errorf("model registry has no providers")
	}
	for i, provider := range providers {
		if strings.TrimSpace(provider.Provider) == "" {
			return fmt.Errorf("model registry provider %d has empty name", i)
		}
		if len(provider.Models) == 0 {
			return fmt.Errorf("model registry provider %q has no models", provider.Provider)
		}
		if err := validateModels(provider); err != nil {
			return err
		}
	}
	return nil
}

func validateModels(provider ProviderModels) error {
	for i, model := range provider.Models {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("model registry provider %q model %d is empty", provider.Provider, i)
		}
	}
	return nil
}

func lookupProvider(providers []ProviderModels, name string) (ProviderModels, bool) {
	name = strings.TrimSpace(name)
	for _, provider := range providers {
		if provider.Provider == name {
			return cloneProvider(provider), true
		}
	}
	return ProviderModels{}, false
}

func cloneProviders(providers []ProviderModels) []ProviderModels {
	out := make([]ProviderModels, len(providers))
	for i, provider := range providers {
		out[i] = cloneProvider(provider)
	}
	return out
}

func cloneProvider(provider ProviderModels) ProviderModels {
	return ProviderModels{
		Provider:          provider.Provider,
		Models:            append([]string(nil), provider.Models...),
		Available:         cloneBoolPointer(provider.Available),
		UnavailableReason: provider.UnavailableReason,
	}
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func findDefaultPathFromWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return DefaultPath
	}
	for {
		candidate := filepath.Join(wd, DefaultPath)
		if fileExists(candidate) {
			return candidate
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return DefaultPath
		}
		wd = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
