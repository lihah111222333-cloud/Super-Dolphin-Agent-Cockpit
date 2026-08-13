package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	editpkg "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/edit"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
)

func TestBuildPatchReplacePlanUsesSectionAnchorOnlyForScope(t *testing.T) {
	content := "old\n## Trusted workspace\nold\n"
	plan, err := buildReplacePlan(content, EditRequest{
		Patch: "@@ section\n ## Trusted workspace\n@@ change\n-old\n+new\n",
	})
	if err != nil {
		t.Fatalf("build patch plan: %v", err)
	}
	if plan.updatedContent != "old\n## Trusted workspace\nnew\n" {
		t.Fatalf("updated content = %q", plan.updatedContent)
	}
	if plan.resolvedLSPLine != 3 {
		t.Fatalf("resolved line = %d, want changed line 3", plan.resolvedLSPLine)
	}
	if plan.replaced != "old\n" || plan.replacement != "new\n" {
		t.Fatalf("reported replacement = %q -> %q", plan.replaced, plan.replacement)
	}
	if strings.Contains(plan.matchedBy, "section_anchor") {
		t.Fatalf("section anchor leaked into changed match mode: %q", plan.matchedBy)
	}
}

func TestBuildPatchReplacePlanReportsActualScopedMatchMode(t *testing.T) {
	content := "value\n## section\n  value\n"
	plan, err := buildReplacePlan(content, EditRequest{
		Patch: "@@ section\n ## section\n@@ change\n-value\n+updated\n",
	})
	if err != nil {
		t.Fatalf("build patch plan: %v", err)
	}
	if plan.matchedBy != "trim_both" {
		t.Fatalf("matched_by = %q, want trim_both", plan.matchedBy)
	}
}

func TestBuildPatchReplacePlanAcceptsMultipleSections(t *testing.T) {
	content := strings.Join([]string{
		"3. inspect repository",
		"4. old instruction",
		"current shell matches client",
		"platform probe",
		"selected platform is trusted",
		"GO_AGENT_LSP_ROOT=<project>",
		"",
	}, "\n")
	patch := strings.Join([]string{
		" 3. inspect repository",
		"-4. old instruction",
		"+4. ask for trusted cwd",
		"@@ platform section",
		" current shell matches client",
		"@@ insert scope rules",
		" selected platform is trusted",
		"+trusted cwd must be explicit",
		"@@ update root contract",
		"-GO_AGENT_LSP_ROOT=<project>",
		"+GO_AGENT_LSP_ROOT=<trusted-cwd>",
		"",
	}, "\n")

	plan, err := buildReplacePlan(content, EditRequest{Patch: patch})
	if err != nil {
		t.Fatalf("build patch plan: %v", err)
	}
	want := strings.Join([]string{
		"3. inspect repository",
		"4. ask for trusted cwd",
		"current shell matches client",
		"platform probe",
		"selected platform is trusted",
		"trusted cwd must be explicit",
		"GO_AGENT_LSP_ROOT=<trusted-cwd>",
		"",
	}, "\n")
	if plan.updatedContent != want {
		t.Fatalf("updated content = %q, want %q", plan.updatedContent, want)
	}
}

func TestReplaceRangeSectionAnchorFailureLeavesFileUnchanged(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.txt")
	original := "## section\nold\n"
	if err := os.WriteFile(inside, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: inside,
		Patch:    "@@ section\n ## section\n@@ first change\n-old\n+new\n@@ missing change\n-missing\n+replacement\n",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(testToolContext(root), input)
	if !errors.Is(err, editpkg.ErrSequenceNotFound) {
		t.Fatalf("edit error = %v, want ErrSequenceNotFound", err)
	}
	assertFileContent(t, inside, original)
}

func TestReplaceRangeSubstringSectionAnchorFailureLeavesFileUnchanged(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.txt")
	original := "prefix ## section\nold\n"
	if err := os.WriteFile(inside, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: inside,
		Patch:    "@@ section\n ## section\n@@ change\n-old\n+new\n",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(testToolContext(root), input)
	if !errors.Is(err, editpkg.ErrSequenceNotFound) {
		t.Fatalf("edit error = %v, want ErrSequenceNotFound", err)
	}
	assertFileContent(t, inside, original)
}
