package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestBuildGatePlanRoutesEveryIndexedProjectMapSurface(t *testing.T) {
	for _, path := range []string{
		"docs/架构/skeleton-code-guard.md",
		"frontend-app/package.json",
		"scripts/test_with_guard.sh",
		"README.md",
	} {
		t.Run(path, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{path})
			assertStringSetContains(t, plan.RequiredGates, "project-map:check")
		})
	}

	plan := mustBuildGatePlan(t, []string{".githooks/pre-commit"})
	assertStringSetOmits(t, plan.RequiredGates, "project-map:check")
}

func TestProjectMapRoutingCoversGeneratorIndexedRoots(t *testing.T) {
	generator, err := os.ReadFile(filepath.Join("..", "generate_ai_project_map.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range javascriptStringArray(t, string(generator), `const INDEXED_TOP_LEVEL_DIRS = new Set`) {
		if !projectMapRelevant(root + "/gate-fixture.txt") {
			t.Errorf("project-map generator root %q is absent from gate routing", root)
		}
	}
	for _, file := range javascriptStringArray(t, string(generator), `const INDEXED_ROOT_FILES = new Set`) {
		if !projectMapRelevant(file) {
			t.Errorf("project-map generator root file %q is absent from gate routing", file)
		}
	}
}

func javascriptStringArray(t *testing.T, source, declaration string) []string {
	t.Helper()
	block := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(declaration) + `\(\[\s*(.*?)\s*\]\);`).FindStringSubmatch(source)
	if len(block) != 2 {
		t.Fatalf("generator declaration %q was not found", declaration)
	}
	matches := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(block[1], -1)
	if len(matches) == 0 {
		t.Fatalf("generator declaration %q has no string entries", declaration)
	}
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, match[1])
	}
	return values
}
