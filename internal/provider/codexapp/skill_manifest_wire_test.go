package codexapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// TestStartAssembly_PrependsSkillManifest verifies that when a populated
// skilllibrary.Store is wired in, startAssemblyInstructions prepends the
// "## 可用 skills" header to baseInstructions.
//
// We exercise this at the buildSkillManifest level (verifying behavior
// without needing to construct a full driver), since the wiring itself
// is a thin call we verify via integration. Full driver construction is
// covered by existing driver tests.
func TestStartAssembly_ManifestRendersFromStore(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()

	// Pre-populate library with one fake skill so List() returns content
	skillDir := filepath.Join(libDir, "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: demo\ndescription: a demo\n---\n# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := skilllibrary.WriteMeta(skillDir, skilllibrary.SkillMeta{
		Name: "demo", Origin: skilllibrary.OriginBuiltin, Version: "1",
	}); err != nil {
		t.Fatal(err)
	}

	// Sanity: store can list it
	store := skilllibrary.NewStore(libDir)
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("List = %d, want 1", len(entries))
	}

	manifest := buildSkillManifest(entries, 8192)
	if !strings.Contains(manifest, "## 可用 skills") {
		t.Errorf("manifest missing header: %s", manifest)
	}
	if !strings.Contains(manifest, "demo") {
		t.Errorf("manifest missing skill name")
	}

	// Suppress unused import if cacheDir is not referenced
	_ = cacheDir
	_ = skillforge.ListEmbeddedSkillNames // keep import alive if needed
}
