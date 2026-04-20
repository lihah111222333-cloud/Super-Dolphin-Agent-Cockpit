package skill

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestListSkillsUnionsProjectGlobalAndLegacyRoots(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	if err := writeProjectSkillRoot(projectSkillsRoot); err != nil {
		t.Fatalf("prepare project root: %v", err)
	}
	writeTestSkill(t, projectSkillsRoot, "project-local", "---\nname: project-local\nsummary: local\n---\nbody")
	writeTestSkill(t, systemRoot, "system-global", "---\nname: system-global\nsummary: global\n---\nbody")
	writeScopedSystemSkill(t, systemRoot, projectRoot, "legacy-user", "---\nname: legacy-user\nsummary: legacy\n---\nbody")
	writeScopedSystemSkill(t, systemRoot, filepath.Join(t.TempDir(), "repo-b"), "legacy-other", "---\nname: legacy-other\nsummary: other\n---\nbody")

	svc := &service{
		root:              systemRoot,
		projectRoot:       projectRoot,
		projectSkillsRoot: projectSkillsRoot,
		http:              &http.Client{},
	}

	skills, err := svc.ListSkills(skillTestContext(projectRoot))
	if err != nil {
		t.Fatalf("ListSkills() error = %v", err)
	}

	gotTrust := make(map[string]TrustScope, len(skills))
	for _, item := range skills {
		gotTrust[item.Name] = item.Trust
	}

	if gotTrust["project-local"] != TrustProject {
		t.Fatalf("project-local trust = %q, want project", gotTrust["project-local"])
	}
	if gotTrust["system-global"] != TrustUser {
		t.Fatalf("system-global trust = %q, want user", gotTrust["system-global"])
	}
	if gotTrust["legacy-user"] != TrustUser {
		t.Fatalf("legacy-user trust = %q, want user", gotTrust["legacy-user"])
	}
	if _, ok := gotTrust["legacy-other"]; ok {
		t.Fatalf("legacy-other leaked into cwd-scoped listing: %#v", gotTrust)
	}
}

func writeProjectSkillRoot(root string) error {
	return os.MkdirAll(root, 0o755)
}
