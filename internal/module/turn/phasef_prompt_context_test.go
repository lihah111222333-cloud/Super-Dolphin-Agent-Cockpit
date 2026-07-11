package turn

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestMergePrepareInputRuntimeHydratesOutputStyleAndScratchpad(t *testing.T) {
	input := mergePrepareInputRuntime(PrepareInput{}, map[string]any{
		"outputStyleConfig": map[string]any{
			"name":                   "Explanatory",
			"prompt":                 "Explain each decision.",
			"keepCodingInstructions": true,
		},
		"summary":       "brief",
		"scratchpadDir": "/tmp/agent/scratchpad",
	})
	if input.OutputStyleConfig == nil || input.OutputStyleConfig.Name != "Explanatory" || input.OutputStyleConfig.Prompt != "Explain each decision." {
		t.Fatalf("OutputStyleConfig = %#v", input.OutputStyleConfig)
	}
	if input.OutputStyleConfig.KeepCodingInstructions == nil || !*input.OutputStyleConfig.KeepCodingInstructions {
		t.Fatalf("KeepCodingInstructions = %#v", input.OutputStyleConfig)
	}
	if input.Summary != "brief" {
		t.Fatalf("Summary = %q, want runtime config value", input.Summary)
	}
	if input.ScratchpadDir != "/tmp/agent/scratchpad" {
		t.Fatalf("ScratchpadDir = %q, want runtime config value", input.ScratchpadDir)
	}
}

func TestBuildPrepareInputClonesPromptContextFields(t *testing.T) {
	style := &contract.OutputStyleConfig{Name: "Terse", Prompt: "Answer directly."}
	input := buildPrepareInput(prepareInputSpec{
		OutputStyleConfig: style,
		ScratchpadDir:     "/tmp/session/scratchpad",
	}, prepareSkillSpec{}, nil)
	if input.OutputStyleConfig == nil || input.OutputStyleConfig.Name != "Terse" {
		t.Fatalf("OutputStyleConfig = %#v", input.OutputStyleConfig)
	}
	if input.OutputStyleConfig == style {
		t.Fatal("OutputStyleConfig shares pointer with spec, want clone")
	}
	if input.ScratchpadDir != "/tmp/session/scratchpad" {
		t.Fatalf("ScratchpadDir = %q, want cloned value", input.ScratchpadDir)
	}
}
