package archtest_test

import "testing"

func TestSkillDoesNotImportFBSDModule(t *testing.T) {
	root := repoRoot(t)
	assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/module/skill"), []string{internalPrefix("internal/module/fbsd")})
}
