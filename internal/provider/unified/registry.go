package unified

import (
	"fmt"
	"slices"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type Registry struct {
	drivers    map[string]contract.DriverFactory
	skillPorts map[string]contract.SkillInjectionPort
}

func NewRegistry(params RegistryParams) *Registry {
	drivers := make(map[string]contract.DriverFactory, len(params.Drivers))
	for _, factory := range params.Drivers {
		name := normalizeProviderName(factory.Name)
		if name == "" || factory.Create == nil {
			continue
		}
		drivers[name] = factory
	}
	skillPorts := make(map[string]contract.SkillInjectionPort, len(params.SkillPorts))
	for _, descriptor := range params.SkillPorts {
		name := normalizeProviderName(descriptor.Name)
		if name == "" || descriptor.Port == nil {
			continue
		}
		skillPorts[name] = descriptor.Port
	}
	return &Registry{drivers: drivers, skillPorts: skillPorts}
}

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

func (r *Registry) ResolveSkillInjectionPort(provider string) (contract.SkillInjectionPort, bool) {
	if r == nil {
		return nil, false
	}
	port, ok := r.skillPorts[normalizeProviderName(provider)]
	if !ok || port == nil {
		return nil, false
	}
	return port, true
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
