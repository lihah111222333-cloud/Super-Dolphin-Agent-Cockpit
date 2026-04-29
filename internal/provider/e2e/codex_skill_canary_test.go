package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	turnpkg "github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/pkg/skilltool"
)

func TestCodexProgressiveDisclosureCanaryReadiness_E2E(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	writeCanarySkill(t, cwd)

	skillSvc := skillpkg.NewService(cwd)
	promptSvc := promptpkg.NewService(&promptpkg.Config{
		EnableSkillProgressiveDisclosure: true,
		EmitSkillCatalogMetaInstructions: true,
	}, nil)
	catalogProvider := promptpkg.NewSkillCatalogProviderWithOptions(
		skillSvc,
		nil,
		12000,
		promptpkg.SkillCatalogOptions{EmitMetaInstructions: true},
	)
	if err := promptSvc.RegisterDynamicProvider(catalogProvider); err != nil {
		t.Fatalf("RegisterDynamicProvider(skill_catalog) error = %v", err)
	}

	assembly, err := promptSvc.AssembleStart(ctx, promptpkg.StartInput{
		BaseInstructions:   "legacy base",
		Provider:           "codex",
		CWD:                cwd,
		LaunchSkillNames:   []string{"demo-canary"},
		ForceLaunchSkills:  true,
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	assertTextContains(t, assembly.BaseInstructions, "legacy base")
	assertTextContains(t, assembly.BaseInstructions, "demo canary summary")
	assertTextContains(t, assembly.BaseInstructions, "skill_expand_body")
	assertTextContains(t, assembly.BaseInstructions, "skill_read_resource")
	assertTextNotContains(t, assembly.BaseInstructions, "FULL_BODY_CANARY_MARKER")

	hostTools := toolbridge.NewSkillHostTools(skillSvc)
	if hostTools == nil {
		t.Fatal("NewSkillHostTools() returned nil")
	}
	dynamicTools := codexDynamicToolsFromHost(hostTools)

	recorder := &codexRPCRecorder{}
	t.Setenv("CODEX_APP_SERVER_URL", startCodexRPCServer(t, recorder))
	factory := codexapp.NewDriverFactory(nil, nil, nil, nil, nil, nil)
	factory.SetListTools(func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return dynamicTools, nil
	})

	session, err := factory.Create().StartSession(ctx, dto.StartSessionRequest{
		AgentID: "agent-canary",
		CWD:     cwd,
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      assembly.BaseInstructions,
			DeveloperInstructions: assembly.DeveloperInstructions,
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	threadStart := recorder.threadStartParamsSnapshot()
	assertDynamicToolNames(t, threadStart, []string{skilltool.ToolNameExpandBody, skilltool.ToolNameReadResource})
	baseInstructions, _ := threadStart["baseInstructions"].(string)
	assertTextContains(t, baseInstructions, "demo canary summary")
	assertTextNotContains(t, baseInstructions, "FULL_BODY_CANARY_MARKER")

	turnSvc := turnpkg.NewServiceWithPromptAssemblyAndTurnContext(nil, nil, nil, skillSvc, nil)
	turnReq, err := turnSvc.PrepareTurn(ctx, session, turnpkg.PrepareInput{
		Prompt:               "Use demo-canary for this request",
		CWD:                  cwd,
		Skills:               []dto.SkillRef{{Name: "demo-canary", Source: dto.SkillSourceManual}},
		ManualSkillSelection: true,
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if len(turnReq.Skills) != 1 {
		t.Fatalf("PrepareTurn() skills = %#v", turnReq.Skills)
	}
	if turnReq.Skills[0].Mode != dto.SkillModeUnspecified {
		t.Fatalf("PrepareTurn() Mode = %q, want Unspecified marker before codex adapter", turnReq.Skills[0].Mode)
	}

	if _, err := session.StartTurn(ctx, turnReq); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	turnStart := recorder.paramsSnapshot("turn/start", 0)
	assertStringSliceParam(t, turnStart, "selectedSkills", []string{"demo-canary"})
	if got, _ := turnStart["manualSkillSelection"].(bool); !got {
		t.Fatalf("manualSkillSelection = %#v, want true", turnStart["manualSkillSelection"])
	}
	turnSkillText := findTurnInputTextContaining(t, turnStart, "[skill:demo-canary]")
	assertTextContains(t, turnSkillText, "摘要: demo canary summary")
	assertTextContains(t, turnSkillText, `Call skill_expand_body("demo-canary")`)
	assertTextNotContains(t, turnSkillText, "FULL_BODY_CANARY_MARKER")

	expandArgs, _ := json.Marshal(map[string]any{"name": "demo-canary"})
	expandedAny, err := hostTools.CallHostTool(ctx, toolbridge.HostToolCall{
		Name:      skilltool.ToolNameExpandBody,
		Arguments: expandArgs,
		CWD:       cwd,
		AgentID:   "agent-canary",
		ThreadID:  session.ThreadID(),
		TurnID:    "provider-turn-1",
		CallID:    "call-canary",
	})
	if err != nil {
		t.Fatalf("CallHostTool(skill_expand_body) error = %v", err)
	}
	expanded, ok := expandedAny.(skillpkg.ExpandBodyResult)
	if !ok {
		t.Fatalf("CallHostTool result = %T, want skill.ExpandBodyResult", expandedAny)
	}
	assertTextContains(t, expanded.Content, "FULL_BODY_CANARY_MARKER")
}

func writeCanarySkill(t *testing.T, cwd string) {
	t.Helper()
	skillDir := filepath.Join(cwd, ".agent", "skills", "demo-canary")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll skill dir: %v", err)
	}
	body := "---\n" +
		"name: demo-canary\n" +
		"description: demo canary description\n" +
		"summary: demo canary summary\n" +
		"trust: user\n" +
		"---\n" +
		"# Demo Canary\n\n" +
		"FULL_BODY_CANARY_MARKER\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile SKILL.md: %v", err)
	}
}

func codexDynamicToolsFromHost(hostTools *toolbridge.SkillHostTools) []codexprotocol.DynamicToolSchema {
	tools := hostTools.ListHostTools()
	out := make([]codexprotocol.DynamicToolSchema, 0, len(tools))
	for _, tool := range tools {
		out = append(out, codexprotocol.DynamicToolSchema{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	return out
}

func findTurnInputTextContaining(t *testing.T, params map[string]any, needle string) string {
	t.Helper()
	inputs, ok := params["input"].([]any)
	if !ok {
		t.Fatalf("input = %#v, want array", params["input"])
	}
	for _, raw := range inputs {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("input item = %#v, want object", raw)
		}
		text, _ := item["text"].(string)
		if strings.Contains(text, needle) {
			return text
		}
	}
	t.Fatalf("no input text containing %q in %#v", needle, inputs)
	return ""
}

func assertStringSliceParam(t *testing.T, params map[string]any, key string, want []string) {
	t.Helper()
	raw, ok := params[key].([]any)
	if !ok {
		t.Fatalf("%s = %#v, want array", key, params[key])
	}
	got := make([]string, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		if !ok {
			t.Fatalf("%s item = %#v, want string", key, item)
		}
		got = append(got, value)
	}
	if len(got) != len(want) {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %#v, want %#v", key, got, want)
		}
	}
}

func assertTextContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("text missing %q:\n%s", want, text)
	}
}

func assertTextNotContains(t *testing.T, text, forbidden string) {
	t.Helper()
	if strings.Contains(text, forbidden) {
		t.Fatalf("text unexpectedly contains %q:\n%s", forbidden, text)
	}
}
