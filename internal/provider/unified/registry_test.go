package unified_test

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
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
