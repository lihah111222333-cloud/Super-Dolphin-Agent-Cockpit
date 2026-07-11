package unified

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// Registry 保存 provider 名称到 driver factory 的映射，并聚合 provider 原生工具描述。
type Registry struct {
	drivers     map[string]contract.DriverFactory
	nativeTools []contract.NativeToolDescriptor
}

// NewRegistry 构建 provider registry，空名称或空 Create 的 factory 会被忽略。
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

// Resolve 根据 provider 名称创建 driver，未知 provider 或 nil driver 都立即返回错误。
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

// Names 返回已注册 provider 名称的稳定排序列表，供 session 恢复按固定顺序扫描。
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

// NativeTools 返回所有 provider 暴露的原生工具描述副本，避免调用方改动 registry 内部切片。
func (r *Registry) NativeTools() []contract.NativeToolDescriptor {
	if r == nil {
		return nil
	}
	out := make([]contract.NativeToolDescriptor, len(r.nativeTools))
	copy(out, r.nativeTools)
	return out
}

// normalizeProviderName 标准化 provider 名称，确保注册、查找和恢复路径一致。
func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
