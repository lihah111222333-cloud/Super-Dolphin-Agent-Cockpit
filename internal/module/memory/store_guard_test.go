package memory

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardDiskStoreCreateRejectsSecretOnTeamPath(t *testing.T) {
	store := newTestTeamGuardedDiskStore(t)

	_, err := store.Create(testMemoryEntry(
		"Release Date",
		"Keep the shared milestone stable",
		MemoryTypeProject,
		"Release milestone is stable.\nWhy: partner travel is booked.\nHow to apply: token = \"ghp_abcdefghijklmnopqrstuvwxyz1234567890AB\"",
	))
	if !errors.Is(err, ErrTeamMemSecretDetected) {
		t.Fatalf("Create() error = %v, want %v", err, ErrTeamMemSecretDetected)
	}
	if errors.Is(err, ErrForbiddenMemoryContent) {
		t.Fatalf("Create() error = %v, want team guard to run before generic content validation", err)
	}
	var secretErr *TeamMemSecretError
	if !errors.As(err, &secretErr) {
		t.Fatalf("Create() error = %T, want *TeamMemSecretError", err)
	}
	if !strings.HasPrefix(secretErr.Path, store.Root()+string(filepath.Separator)) {
		t.Fatalf("Create() path = %q, want prefix %q", secretErr.Path, store.Root()+string(filepath.Separator))
	}
}

func TestGuardDiskStoreUpdateRejectsSecretOnTeamPath(t *testing.T) {
	store := newTestTeamGuardedDiskStore(t)
	if _, err := store.Create(testMemoryEntry(
		"Release Date",
		"Keep the shared milestone stable",
		MemoryTypeProject,
		"Release milestone is 2026-05-01.\nWhy: partner travel is booked.\nHow to apply: keep plans aligned with the shared date.",
	)); err != nil {
		t.Fatalf("Create(safe) error = %v", err)
	}

	_, err := store.Update(testMemoryEntry(
		"Release Date",
		"Keep the shared milestone stable",
		MemoryTypeProject,
		"Release milestone is still shared.\nWhy: partner travel is booked.\nHow to apply: api_key = \"sk-proj-abcdefghijklmnopqrstuvwxyz1234567890\"",
	))
	if !errors.Is(err, ErrTeamMemSecretDetected) {
		t.Fatalf("Update() error = %v, want %v", err, ErrTeamMemSecretDetected)
	}
	if errors.Is(err, ErrForbiddenMemoryContent) {
		t.Fatalf("Update() error = %v, want team guard to run before generic content validation", err)
	}
}

func TestGuardDiskStoreCreateUsesGenericValidationOutsideTeamPath(t *testing.T) {
	store := newTestDiskStore(t, newTestMemoryRoot(t))

	_, err := store.Create(testMemoryEntry(
		"Private Secret",
		"Do not persist this",
		MemoryTypeUser,
		"token = \"ghp_abcdefghijklmnopqrstuvwxyz1234567890AB\"",
	))
	if !errors.Is(err, ErrForbiddenMemoryContent) {
		t.Fatalf("Create() error = %v, want %v", err, ErrForbiddenMemoryContent)
	}
	if errors.Is(err, ErrTeamMemSecretDetected) {
		t.Fatalf("Create() error = %v, want non-team path to bypass team guard", err)
	}
}

func newTestTeamGuardedDiskStore(t *testing.T) *diskStore {
	t.Helper()
	autoRoot := filepath.Join(t.TempDir(), "automem")
	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         newTestGitProjectRoot(t),
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	store, err := newDiskStoreWithGuard(
		filepath.Join(autoRoot, teamMemoryRootDirName),
		NewTeamMemoryGuard(NewTeamMemoryManager(cfg)),
		nil,
	)
	if err != nil {
		t.Fatalf("NewDiskStoreWithGuard(team) error = %v", err)
	}
	return store
}
