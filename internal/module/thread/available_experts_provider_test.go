package thread

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestBuildStartAssemblyInputCarriesPromptKey(t *testing.T) {
	t.Parallel()

	input := buildStartAssemblyInput(StartRequest{
		PromptKey: "main/launch-fav",
		Prompt:    "hello",
	}, "thread-1", contract.BuildCtx{})

	if input.PromptKey != "main/launch-fav" {
		t.Fatalf("PromptKey = %q, want main/launch-fav", input.PromptKey)
	}
}

func TestFoldRouterOutputIntoAssemblyInputPreservesPromptKey(t *testing.T) {
	t.Parallel()

	assemblyInput := contract.StartInput{PromptKey: "pre-router"}
	req := &StartRequest{PromptKey: "main/routed"}

	foldRouterOutputIntoAssemblyInput(&assemblyInput, req)

	if assemblyInput.PromptKey != "main/routed" {
		t.Fatalf("PromptKey = %q, want main/routed", assemblyInput.PromptKey)
	}
}
