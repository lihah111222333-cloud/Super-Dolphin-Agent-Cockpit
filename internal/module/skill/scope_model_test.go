package skill

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/ownerperms"
)

func TestResolveCanonicalRoots_ProjectAndPersonal(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	got := resolveCanonicalRoots(project, home)

	assertPath(t, got.Project, filepath.Join(project, ".agent", "skills"))
	assertPath(t, got.Personal["user"], filepath.Join(home, ".super-dolphin", "skills", "personal", "user"))
	assertPath(t, got.Personal["agent"], filepath.Join(home, ".super-dolphin", "skills", "personal", "agent"))
	assertPath(t, got.Personal["imported"], filepath.Join(home, ".super-dolphin", "skills", "personal", "imported"))
	if _, ok := got.Personal["hub"]; ok {
		t.Fatalf("hub root is catalog-only and must not be an active canonical root: %+v", got.Personal)
	}
}

func TestNormalizeSkillScopeRejectsNewSystemWrites(t *testing.T) {
	_, _, err := normalizeSkillTarget("system", "")
	if !errors.Is(err, ErrSkillSystemScopeRemoved) {
		t.Fatalf("normalizeSkillTarget(system) error = %v, want ErrSkillSystemScopeRemoved", err)
	}
}

func TestDefaultSuperDolphinHome(t *testing.T) {
	override := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv("SUPER_DOLPHIN_HOME", override)
	if got := defaultSuperDolphinHome(); got != override {
		t.Fatalf("defaultSuperDolphinHome() with override = %q, want %q", got, override)
	}

	home := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got, want := defaultSuperDolphinHome(), filepath.Join(home, ".super-dolphin"); got != want {
		t.Fatalf("defaultSuperDolphinHome() fallback = %q, want %q", got, want)
	}
}

func TestDefaultSuperDolphinHomeDoesNotFallbackToTemp(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	if got := defaultSuperDolphinHome(); got != "" {
		t.Fatalf("defaultSuperDolphinHome() without env/home = %q, want empty fail-fast path", got)
	}
}

func TestResolveOwnerIdentityStableAndScoped(t *testing.T) {
	home := t.TempDir()

	first, err := resolveOwnerIdentity(home, "uid:501", "default-profile")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity first: %v", err)
	}
	second, err := resolveOwnerIdentity(home, "uid:501", "default-profile")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity second: %v", err)
	}
	if first.OwnerKey != second.OwnerKey {
		t.Fatalf("owner key changed for same home/uid/profile: %q != %q", first.OwnerKey, second.OwnerKey)
	}
	assertOwnerKeyDoesNotLeak(t, first.OwnerKey, home, "uid:501", "default-profile")
	assertOwnerSaltMode(t, first.SaltPath)
}

func TestResolveOwnerIdentityChangesByProfileAndUID(t *testing.T) {
	home := t.TempDir()

	first, err := resolveOwnerIdentity(home, "uid:501", "default-profile")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity first: %v", err)
	}
	differentProfile, err := resolveOwnerIdentity(home, "uid:501", "work-profile")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity different profile: %v", err)
	}
	if differentProfile.OwnerKey == first.OwnerKey {
		t.Fatalf("owner key did not change for different profile: %q", first.OwnerKey)
	}
	differentUID, err := resolveOwnerIdentity(home, "uid:502", "default-profile")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity different uid: %v", err)
	}
	if differentUID.OwnerKey == first.OwnerKey {
		t.Fatalf("owner key did not change for different uid: %q", first.OwnerKey)
	}
}

func TestResolveOwnerIdentityDoesNotLeakRawInputs(t *testing.T) {
	home := t.TempDir()

	got, err := resolveOwnerIdentity(home, "uid:501", "default-profile")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	assertOwnerKeyDoesNotLeak(t, got.OwnerKey, home, "uid:501", "default-profile")
}

func TestResolveOwnerIdentityCreatesOwnerOnlySalt(t *testing.T) {
	home := t.TempDir()

	got, err := resolveOwnerIdentity(home, "501", "default")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	assertOwnerSaltMode(t, got.SaltPath)
}

func assertOwnerKeyDoesNotLeak(t *testing.T, ownerKey string, rawValues ...string) {
	t.Helper()
	if !strings.HasPrefix(ownerKey, "sd_owner:") {
		t.Fatalf("owner key = %q, want sd_owner prefix", ownerKey)
	}
	for _, raw := range rawValues {
		if strings.Contains(ownerKey, raw) {
			t.Fatalf("owner key %q leaks raw identity fragment %q", ownerKey, raw)
		}
	}
}

func assertOwnerSaltMode(t *testing.T, saltPath string) {
	t.Helper()
	if saltPath == "" {
		t.Fatalf("salt path is empty")
	}
	info, err := os.Stat(saltPath)
	if err != nil {
		t.Fatalf("stat salt: %v", err)
	}
	if err := ownerperms.ValidateOwnerIdentitySaltPermissions(saltPath, info); err != nil {
		t.Fatalf("salt permissions are not owner-only: %v", err)
	}
}

func TestResolveOwnerIdentityFailsClosedForBadSalt(t *testing.T) {
	t.Run("directory at salt path", assertOwnerIdentityFailsClosedForSaltDir)
	t.Run("overly broad permissions", assertOwnerIdentityFailsClosedForBroadSalt)
}

func assertOwnerIdentityFailsClosedForSaltDir(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "owner_identity.salt"), 0o700); err != nil {
		t.Fatalf("mkdir salt path: %v", err)
	}

	if _, err := resolveOwnerIdentity(home, "501", "default"); err == nil {
		t.Fatalf("resolveOwnerIdentity succeeded with unusable salt path")
	}
}

func assertOwnerIdentityFailsClosedForBroadSalt(t *testing.T) {
	home := t.TempDir()
	saltPath := filepath.Join(home, "owner_identity.salt")
	if err := os.WriteFile(saltPath, []byte("salt"), 0o644); err != nil {
		t.Fatalf("write salt: %v", err)
	}

	if _, err := resolveOwnerIdentity(home, "501", "default"); err == nil {
		t.Fatalf("resolveOwnerIdentity succeeded with overly broad salt permissions")
	}
}

func assertPath(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
