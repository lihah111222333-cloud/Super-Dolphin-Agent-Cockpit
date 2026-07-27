package main

import "testing"

// TestCurrentDocumentationNavigationUsesLiveLifecycleRoots 锁定当前导航不再指向死目录或历史权威。
func TestCurrentDocumentationNavigationUsesLiveLifecycleRoots(t *testing.T) {
	tests := []struct {
		path      string
		forbidden []string
		required  []string
	}{
		{
			path:      "../AGENTS.md",
			forbidden: []string{"`docs/decisions/*.md`"},
			required:  []string{"`docs/adr/*.md`"},
		},
		{
			path:      "../docs/契约/README.md",
			forbidden: []string{"`docs/decisions`"},
			required:  []string{"`docs/adr`"},
		},
		{
			path: "../docs/契约/fix-workflow-convention.md",
			forbidden: []string{
				"`docs/decisions`",
				"`docs/li/",
				"`docs/plans/",
				"`docs/reviews/",
			},
			required: []string{
				"`docs/work/plans/",
				"`docs/archive/reviews/",
				"`docs/adr/",
			},
		},
		{
			path:      "../docs/契约/mcp-service-convention.md",
			forbidden: []string{"docs/decisions/ADR-003-mcp-input-enum-validation.md"},
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			body := readRepoFile(t, test.path)
			for _, forbidden := range test.forbidden {
				assertScriptDoesNotContain(t, body, forbidden)
			}
			for _, required := range test.required {
				assertScriptContains(t, body, required)
			}
		})
	}
}
