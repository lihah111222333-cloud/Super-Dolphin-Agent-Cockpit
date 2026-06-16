package identity

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestValidateNameAcceptsOnlyBoundedIdentifierNames verifies skill names stay portable.
func TestValidateNameAcceptsOnlyBoundedIdentifierNames(t *testing.T) {
	t.Parallel()

	if got, ok := ValidateName(" skill_1 "); !ok || got != "skill_1" {
		t.Fatalf("ValidateName() = %q, %v; want skill_1, true", got, ok)
	}

	invalid := []string{"-skill", "skill name", strings.Repeat("a", 65)}
	for _, name := range invalid {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, ok := ValidateName(name); ok || got != "" {
				t.Fatalf("ValidateName(%q) = %q, %v; want empty, false", name, got, ok)
			}
		})
	}
}

// TestNormalizeUsesExplicitNameOrSafeLegacyDisplayName verifies legacy display names slug safely.
func TestNormalizeUsesExplicitNameOrSafeLegacyDisplayName(t *testing.T) {
	t.Parallel()

	name, displayName, ok := Normalize(" skill_1 ", " Skill One ")
	if !ok || name != "skill_1" || displayName != "Skill One" {
		t.Fatalf("Normalize(valid) = %q, %q, %v", name, displayName, ok)
	}

	name, displayName, ok = Normalize("My Skill", "")
	if !ok || name != "my-skill" || displayName != "My Skill" {
		t.Fatalf("Normalize(legacy) = %q, %q, %v", name, displayName, ok)
	}

	if _, _, ok := Normalize("", "Display Only"); ok {
		t.Fatal("Normalize(display-only) ok = true, want false")
	}
}

// TestRewriteFrontmatterUpsertsNameAndDisplayName verifies identity metadata is rewritten in place.
func TestRewriteFrontmatterUpsertsNameAndDisplayName(t *testing.T) {
	t.Parallel()

	content := "---\ndescription: old\ntitle: Old Title\n---\nbody"
	got, ok := RewriteFrontmatter(content, "new-skill", `Skill "Name"`)
	if !ok {
		t.Fatal("RewriteFrontmatter() ok = false, want true")
	}
	want := "---\nname: new-skill\ndescription: old\ndisplay_name: \"Skill \\\"Name\\\"\"\n---\nbody"
	if got != want {
		t.Fatalf("RewriteFrontmatter() = %q, want %q", got, want)
	}
}

// TestCanonicalNameForAliasMatchesDisplayName verifies display names resolve to canonical skill names.
func TestCanonicalNameForAliasMatchesDisplayName(t *testing.T) {
	t.Parallel()

	skills := []contract.SkillInfo{{Name: "go-code", DisplayName: "Go Code"}}
	got, ok := CanonicalNameForAlias("go code", skills)
	if !ok || got != "go-code" {
		t.Fatalf("CanonicalNameForAlias() = %q, %v; want go-code, true", got, ok)
	}
}
