package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkillMirrorManifestRoundTripPreservesPersonalTypes(t *testing.T) {
	manifest := SkillMirrorManifest{
		Version:         1,
		Manager:         "super-dolphin",
		Scope:           skillScopePersonal,
		Provider:        "claude",
		CanonicalRootID: "sd_owner:abc123",
		GeneratedAt:     time.Date(2026, 5, 16, 10, 11, 12, 0, time.UTC),
		Skills: map[string]SkillMirrorEntry{
			"user-build":     personalMirrorEntry("personal/user/build", personalSkillTypeUser),
			"agent-build":    personalMirrorEntry("personal/agent/build", personalSkillTypeAgent),
			"imported-build": personalMirrorEntry("personal/imported/build", personalSkillTypeImported),
		},
	}
	path := filepath.Join(t.TempDir(), skillMirrorManifestFile)

	if err := writeSkillMirrorManifest(path, manifest); err != nil {
		t.Fatalf("writeSkillMirrorManifest: %v", err)
	}
	got, err := readSkillMirrorManifest(path)
	if err != nil {
		t.Fatalf("readSkillMirrorManifest: %v", err)
	}

	for key, want := range manifest.Skills {
		if got.Skills[key].PersonalType != want.PersonalType {
			t.Fatalf("%s personal_type = %q, want %q", key, got.Skills[key].PersonalType, want.PersonalType)
		}
		if got.Skills[key].CanonicalID != want.CanonicalID {
			t.Fatalf("%s canonical_id changed: %+v", key, got.Skills[key])
		}
	}
}

func TestSkillMirrorManifestPersonalDoesNotLeakRawOwnerOrMirrorPaths(t *testing.T) {
	rawHome := filepath.Join(t.TempDir(), "home-user")
	rawProfile := filepath.Join(rawHome, "Profiles", "default")
	rawMirrorRoot := filepath.Join(rawHome, ".claude", "skills")
	manifest := SkillMirrorManifest{
		Version:         1,
		Manager:         "super-dolphin",
		Scope:           skillScopePersonal,
		Provider:        "codex",
		CanonicalRootID: "sd_owner:secretless",
		GeneratedAt:     time.Date(2026, 5, 16, 10, 11, 12, 0, time.UTC),
		Skills: map[string]SkillMirrorEntry{
			"build": personalMirrorEntry("personal/user/build", personalSkillTypeUser),
		},
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	body := string(data)
	for _, leaked := range []string{rawHome, rawProfile, rawMirrorRoot, os.Getenv("USER"), "501"} {
		if strings.TrimSpace(leaked) != "" && strings.Contains(body, leaked) {
			t.Fatalf("personal manifest leaked %q in %s", leaked, body)
		}
	}
	if !strings.Contains(body, `"canonical_root_id":"sd_owner:secretless"`) {
		t.Fatalf("personal manifest must store owner_key canonical_root_id: %s", body)
	}
}

func TestSkillMirrorManifestRejectsSymlinkFile(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	path := filepath.Join(dir, skillMirrorManifestFile)
	if err := os.Symlink(outside, path); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink manifest: %v", err)
	}
	if _, err := readSkillMirrorManifest(path); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("readSkillMirrorManifest symlink error = %v, want symlink rejection", err)
	}
	if err := writeSkillMirrorManifest(path, SkillMirrorManifest{Version: 1, Manager: "super-dolphin"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("writeSkillMirrorManifest symlink error = %v, want symlink rejection", err)
	}
}

func TestSkillMirrorManifestRejectsUnsafePersonalCanonicalIDs(t *testing.T) {
	for _, tc := range []struct {
		name         string
		canonicalID  string
		personalType string
	}{
		{name: "traversal", canonicalID: "../build", personalType: personalSkillTypeUser},
		{name: "wrong_prefix", canonicalID: "providers/claude/skills/build", personalType: personalSkillTypeUser},
		{name: "provider_layout", canonicalID: ".claude/skills/build", personalType: personalSkillTypeUser},
		{name: "invalid_type", canonicalID: "personal/admin/build", personalType: "admin"},
		{name: "catalog_only_hub_type", canonicalID: "personal/hub/build", personalType: personalSkillTypeHub},
		{name: "mismatch_type", canonicalID: "personal/user/build", personalType: personalSkillTypeAgent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := SkillMirrorManifest{
				Version:         1,
				Manager:         "super-dolphin",
				Scope:           skillScopePersonal,
				Provider:        "claude",
				CanonicalRootID: "sd_owner:abc123",
				GeneratedAt:     time.Date(2026, 5, 16, 10, 11, 12, 0, time.UTC),
				Skills: map[string]SkillMirrorEntry{
					"build": personalMirrorEntry(tc.canonicalID, tc.personalType),
				},
			}
			if err := validateSkillMirrorManifest(manifest); err == nil {
				t.Fatalf("validateSkillMirrorManifest accepted canonical_id=%q personal_type=%q", tc.canonicalID, tc.personalType)
			}
		})
	}
}

func personalMirrorEntry(canonicalID, personalType string) SkillMirrorEntry {
	return SkillMirrorEntry{
		CanonicalID:   canonicalID,
		CanonicalHash: "canonical-" + personalType,
		MirrorHash:    "mirror-" + personalType,
		SourceType:    skillScopePersonal,
		PersonalType:  personalType,
		Owned:         true,
	}
}
