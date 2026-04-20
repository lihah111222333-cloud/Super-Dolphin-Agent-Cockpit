package unified_test

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func TestRegistry_Resolve_Known(t *testing.T) {
	driver := &mockDriver{name: "test", session: &mockSession{threadID: "thread-1"}}
	registry := unified.NewRegistry(unified.RegistryParams{
		Drivers: []contract.DriverFactory{{Name: "test", Create: func() contract.Driver { return driver }}},
	})
	got, err := registry.Resolve("test")
	if err != nil || got != driver {
		t.Fatalf("resolve known mismatch: driver=%v err=%v", got, err)
	}
}

func TestRegistry_Resolve_Unknown(t *testing.T) {
	registry := unified.NewRegistry(unified.RegistryParams{})
	if _, err := registry.Resolve("unknown"); err == nil {
		t.Fatal("expected unknown provider error")
	}
}

func TestRegistry_Resolve_NilFactory(t *testing.T) {
	registry := unified.NewRegistry(unified.RegistryParams{
		Drivers: []contract.DriverFactory{{Name: "test", Create: func() contract.Driver { return nil }}},
	})
	if _, err := registry.Resolve("test"); err == nil {
		t.Fatal("expected nil driver error")
	}
}

func TestRegistry_Names(t *testing.T) {
	registry := unified.NewRegistry(unified.RegistryParams{
		Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return &mockDriver{name: "codex", session: &mockSession{}} }},
			{Name: " Claude ", Create: func() contract.Driver { return &mockDriver{name: "claude", session: &mockSession{}} }},
		},
	})
	got := registry.Names()
	if len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("unexpected names: %v", got)
	}
}

func TestRegistry_ResolveSkillInjectionPort_NormalizesProviderName(t *testing.T) {
	port := stubSkillInjectionPort{}
	registry := unified.NewRegistry(unified.RegistryParams{
		SkillPorts: []contract.SkillInjectionPortDescriptor{{Name: " Claude ", Port: port}},
	})
	got, ok := registry.ResolveSkillInjectionPort(" claude ")
	if !ok || got != port {
		t.Fatalf("ResolveSkillInjectionPort() = (%v, %v), want (%v, true)", got, ok, port)
	}
}

func TestRegistry_ResolveSkillInjectionPort_MissingPortFallback(t *testing.T) {
	registry := unified.NewRegistry(unified.RegistryParams{
		SkillPorts: []contract.SkillInjectionPortDescriptor{{Name: "codex", Port: nil}},
	})
	if got, ok := registry.ResolveSkillInjectionPort("codex"); ok || got != nil {
		t.Fatalf("ResolveSkillInjectionPort(codex) = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := registry.ResolveSkillInjectionPort("missing"); ok || got != nil {
		t.Fatalf("ResolveSkillInjectionPort(missing) = (%v, %v), want (nil, false)", got, ok)
	}
}

type stubSkillInjectionPort struct{}

func (stubSkillInjectionPort) InjectL1Manifest(baseInstructions, manifest string) string {
	return baseInstructions + manifest
}

func (stubSkillInjectionPort) BuildTurnSection([]dto.SkillRef) (string, bool) {
	return "", false
}

func (stubSkillInjectionPort) ReservedTokens() int { return 0 }
