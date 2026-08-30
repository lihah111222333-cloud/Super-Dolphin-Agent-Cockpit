package prompt

import "testing"

func TestNewServiceRegistersBuiltInSlots(t *testing.T) {
	svc := NewService(&Config{}, nil)
	want := len(StaticSections()) + len(DynamicSlotNames())
	if len(svc.Sections()) != want {
		t.Fatalf("len(Sections()) = %d, want %d", len(svc.Sections()), want)
	}
}
