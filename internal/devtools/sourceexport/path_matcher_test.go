package sourceexport

import "testing"

func TestClassifyPathUsesDefaultDenyAndDenyPrecedence(t *testing.T) {
	policy := validPolicy()
	policy.AllowRules = []PathRule{
		{Kind: "file", Pattern: "README.md"},
		{Kind: "glob", Pattern: "cmd/**"},
		{Kind: "glob", Pattern: "docs/**"},
	}
	policy.DenyRules = []PathRule{{Kind: "glob", Pattern: "docs/plans/**"}}

	tests := []struct {
		name     string
		path     string
		decision pathDecision
		code     Code
	}{
		{name: "exact file", path: "README.md", decision: pathAllowed},
		{name: "nested glob", path: "cmd/mcp-lsp/tools/factory.go", decision: pathAllowed},
		{name: "single child glob", path: "cmd/main.go", decision: pathAllowed},
		{name: "deny wins", path: "docs/plans/private.md", decision: pathDenied},
		{name: "unclassified", path: "private.txt", code: CodeUnclassifiedPath},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := classifyPath(policy, tt.path)
			if tt.code != "" {
				assertErrorCode(t, err, tt.code)
				return
			}
			if err != nil {
				t.Fatalf("classifyPath() error = %v", err)
			}
			if decision != tt.decision {
				t.Fatalf("decision = %v, want %v", decision, tt.decision)
			}
		})
	}
}

func TestMatchPathRuleDoesNotTreatDoubleStarAsSingleSegment(t *testing.T) {
	rule := PathRule{Kind: "glob", Pattern: ".agents/skills/**"}
	for _, filePath := range []string{
		".agents/skills/backend/SKILL.md",
		".agents/skills/backend/references/testing.md",
	} {
		matched, err := matchPathRule(rule, filePath)
		if err != nil {
			t.Fatalf("matchPathRule(%q) error = %v", filePath, err)
		}
		if !matched {
			t.Fatalf("matchPathRule(%q) = false, want true", filePath)
		}
	}
}

func TestForbiddenFileNameMatchesBasenameAndGlob(t *testing.T) {
	policy := validPolicy()
	policy.ForbiddenFileNames = []string{".env", "*.db-wal", "CLAUDE.md"}
	tests := map[string]bool{
		".env":                   true,
		"nested/.env":            true,
		"state/super.db-wal":     true,
		"docs/CLAUDE.md":         true,
		"frontend/package.json":  false,
		"docs/database-guide.md": false,
	}
	for filePath, want := range tests {
		if got := isForbiddenFileName(policy, filePath); got != want {
			t.Fatalf("isForbiddenFileName(%q) = %v, want %v", filePath, got, want)
		}
	}
}
