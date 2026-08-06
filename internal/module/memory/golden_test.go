package memory

import (
	"flag"
	"testing"

	goldentest "github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

var memoryGoldenOwner = goldentest.NewTestOwner(flag.Bool("update", false, "refresh golden JSON fixtures"))

func TestMemoryPromptGolden(t *testing.T) {
	t.Parallel()

	got := map[string]string{
		"standard":   goldenPromptString(LoadMemoryPrompt(MemoryModeStandard, true, false, true, nil)),
		"skip_index": goldenPromptString(LoadMemoryPrompt(MemoryModeStandard, true, true, true, []string{"Prefer absolute dates in summaries.", "Keep topic names canonical."})),
	}
	goldentest.AssertJSON(t, memoryGoldenOwner, goldentest.Case{
		BaseDir: "testdata/golden",
		Domain:  goldentest.DomainIntegration,
		Name:    "memory_prompt_rules",
	}, got)
}

func goldenPromptString(text *string) string {
	if text == nil {
		return ""
	}
	return *text
}
