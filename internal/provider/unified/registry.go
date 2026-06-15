package unified

import (
	"fmt"
	"slices"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type Registry struct {
	drivers     map[string]contract.DriverFactory
	nativeTools []contract.NativeToolDescriptor
}

// NewRegistry 创建注册表。
func NewRegistry(params RegistryParams) *Registry {
	drivers := make(map[string]contract.DriverFactory, len(params.Drivers))
	var nativeTools []contract.NativeToolDescriptor
	for _, factory := range params.Drivers {
		name := normalizeProviderName(factory.Name)
		if name == "" || factory.Create == nil {
			continue
		}
		drivers[name] = factory
		nativeTools = append(nativeTools, factory.NativeTools...)
	}
	return &Registry{drivers: drivers, nativeTools: nativeTools}
}

// Resolve 解析unified provider。
func (r *Registry) Resolve(provider string) (contract.Driver, error) {
	if r == nil {
		return nil, fmt.Errorf("unknown provider: %q", provider)
	}
	factory, ok := r.drivers[normalizeProviderName(provider)]
	if !ok || factory.Create == nil {
		return nil, fmt.Errorf("unknown provider: %q", provider)
	}
	driver := factory.Create()
	if driver == nil {
		return nil, fmt.Errorf("provider %q factory returned nil driver", provider)
	}
	return driver, nil
}

// Names 处理名称。
func (r *Registry) Names() []string {
	if r == nil || len(r.drivers) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.drivers))
	for name := range r.drivers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// NativeTools returns the aggregated native tool descriptors from all
// registered providers. Order follows provider registration order.
// NativeTools 处理native工具。
func (r *Registry) NativeTools() []contract.NativeToolDescriptor {
	if r == nil {
		return nil
	}
	out := make([]contract.NativeToolDescriptor, len(r.nativeTools))
	copy(out, r.nativeTools)
	return out
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
