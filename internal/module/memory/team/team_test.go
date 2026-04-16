package team

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestTeamMemoryManagerDefaultsToDisabled(t *testing.T) {
	manager := NewTeamMemoryManager(newTestConfig(filepath.Join(t.TempDir(), teamMemoryRootDirName)))

	if manager.IsTeamMemoryEnabled() {
		t.Fatalf("IsTeamMemoryEnabled() = true, want false")
	}
	if got := manager.GetTeamMemPath(); got != "" {
		t.Fatalf("GetTeamMemPath() = %q, want empty", got)
	}
}

func TestValidateTeamMemWritePath(t *testing.T) {
	manager := NewTeamMemoryManager(nil)

	if err := manager.ValidateTeamMemWritePath(""); !errors.Is(err, ErrInvalidTeamMemWritePath) {
		t.Fatalf("ValidateTeamMemWritePath(empty) error = %v, want %v", err, ErrInvalidTeamMemWritePath)
	}
	if err := manager.ValidateTeamMemWritePath("/tmp/team/MEMORY.md"); !errors.Is(err, ErrTeamMemoryDisabled) {
		t.Fatalf("ValidateTeamMemWritePath(valid) error = %v, want %v", err, ErrTeamMemoryDisabled)
	}
}

func TestValidateTeamMemKey(t *testing.T) {
	manager := NewTeamMemoryManager(nil)

	if err := manager.ValidateTeamMemKey("../secret.md"); !errors.Is(err, ErrInvalidTeamMemKey) {
		t.Fatalf("ValidateTeamMemKey(traversal) error = %v, want %v", err, ErrInvalidTeamMemKey)
	}
	if err := manager.ValidateTeamMemKey("team/MEMORY.md"); !errors.Is(err, ErrTeamMemoryDisabled) {
		t.Fatalf("ValidateTeamMemKey(valid) error = %v, want %v", err, ErrTeamMemoryDisabled)
	}
}
