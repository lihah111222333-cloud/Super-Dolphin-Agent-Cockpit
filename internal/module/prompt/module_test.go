package prompt

import "testing"

func TestNewServiceRegistersBuiltInSlots(t *testing.T) {
	svc := NewService(&Config{}, nil)
	if len(svc.Sections()) != 12 {
		t.Fatalf("len(Sections()) = %d, want 12", len(svc.Sections()))
	}
}
