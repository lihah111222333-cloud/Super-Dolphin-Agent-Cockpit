package prompt

import "testing"

func TestSectionRegistryRejectsDuplicateName(t *testing.T) {
	registry := NewSectionRegistry()
	if err := registry.Register(PromptSection{Name: "identity", Order: 10}); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(PromptSection{Name: "identity", Order: 20}); err == nil {
		t.Fatal("expected duplicate Register() to fail")
	}
}

func TestSectionRegistrySectionsSortedByOrderAndName(t *testing.T) {
	registry := NewSectionRegistry()
	sections := []PromptSection{
		{Name: "style", Order: 20},
		{Name: "identity", Order: 10},
		{Name: "actions", Order: 20},
	}
	for _, section := range sections {
		if err := registry.Register(section); err != nil {
			t.Fatalf("Register(%q) error = %v", section.Name, err)
		}
	}

	got := registry.Sections()
	want := []string{"identity", "actions", "style"}
	if len(got) != len(want) {
		t.Fatalf("len(Sections()) = %d, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("Sections()[%d] = %q, want %q", i, got[i].Name, name)
		}
	}
}
