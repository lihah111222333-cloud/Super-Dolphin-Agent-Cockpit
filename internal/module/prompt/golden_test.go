package prompt

import (
	"context"
	"testing"

	goldentest "github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestStartAssemblyGolden(t *testing.T) {
	t.Setenv(envPromptStartCurrentDate, "2026-04-17")

	svc := NewService(&Config{}, nil)
	registerGoldenPromptProvider(t, svc, DynamicSectionMemory, "# Memory\n- Durable user preferences only.\n- Never store secrets.")
	registerGoldenPromptProvider(t, svc, DynamicSectionEnvInfoSimple, "# Environment\n- Primary working directory: /repo\n- Git root: /repo\n- Provider: codex\n- Model: gpt-5.5\n- Language server status: enabled (file, grep)")

	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Name:                  "Feature Thread",
		BaseInstructions:      "legacy base tail",
		DeveloperInstructions: "developer tail",
		Provider:              "codex",
		CWD:                   "/repo",
		GitRoot:               "/repo",
		Language:              "Chinese",
		Model:                 "gpt-5.5",
		EnabledTools:          []string{"grep", "file"},
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	assembly.Snapshot.Generation = 0
	goldentest.AssertJSON(t, goldentest.Case{
		BaseDir: "testdata/golden",
		Domain:  goldentest.DomainIntegration,
		Name:    "start_assembly",
	}, assembly)
}

func registerGoldenPromptProvider(t *testing.T, svc Service, name, text string) {
	t.Helper()

	registerDynamicProviderForTest(t, svc, DynamicTextProvider{
		Name: name,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			value := text
			return &value, nil
		},
	})
}
