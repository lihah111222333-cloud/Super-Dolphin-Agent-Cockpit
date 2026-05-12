package modelregistry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const DefaultPath = "cmd/mcp-orch/tools/modelregistry/models.yaml"

type ProviderModels struct {
	Provider string   `json:"provider" yaml:"provider"`
	Models   []string `json:"models" yaml:"models"`
}

type Registry interface {
	ListProviders() []ProviderModels
	LookupProvider(name string) (ProviderModels, bool)
}

type FileRegistry struct {
	path string

	mu        sync.RWMutex
	providers []ProviderModels
}

type StaticRegistry struct {
	providers []ProviderModels
}

type fileConfig struct {
	Providers []ProviderModels `yaml:"providers"`
}

func NewDefaultRegistry() (*FileRegistry, error) {
	return NewFileRegistry(DefaultRegistryPath())
}

func NewFileRegistry(path string) (*FileRegistry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("model registry path is empty")
	}
	registry := &FileRegistry{path: path}
	if err := registry.Reload(); err != nil {
		return nil, err
	}
	return registry, nil
}

func NewStaticRegistry(providers []ProviderModels) StaticRegistry {
	return StaticRegistry{providers: cloneProviders(providers)}
}

func (r *FileRegistry) ListProviders() []ProviderModels {
	_ = r.Reload()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneProviders(r.providers)
}

func (r *FileRegistry) LookupProvider(name string) (ProviderModels, bool) {
	_ = r.Reload()
	return lookupProvider(r.snapshot(), name)
}

func (r *FileRegistry) Path() string {
	return r.path
}

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

func (r StaticRegistry) ListProviders() []ProviderModels {
	return cloneProviders(r.providers)
}

func (r StaticRegistry) LookupProvider(name string) (ProviderModels, bool) {
	return lookupProvider(r.providers, name)
}

func DefaultRegistryPath() string {
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
		Provider: provider.Provider,
		Models:   append([]string(nil), provider.Models...),
	}
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
