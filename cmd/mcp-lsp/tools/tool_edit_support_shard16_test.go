package tools

import (
	"strings"
	"testing"
)

func TestReplaceRangePatchAllowsEmptyReplacementDeletion(t *testing.T) {
	content := "alpha\nremove me\nomega\n"
	plan, err := buildReplacePlan(content, EditRequest{
		Patch: strings.Join([]string{
			"@@",
			"-remove me",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("build patch plan: %v", err)
	}
	if plan.updatedContent != "alpha\nomega\n" {
		t.Fatalf("updated content = %q, want deletion", plan.updatedContent)
	}
}
