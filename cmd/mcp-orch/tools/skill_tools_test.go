package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

type stubSkillService struct {
	skillmodule.Service
	listSkills func(context.Context) ([]skillmodule.SkillInfo, error)
	expand     func(context.Context, skillmodule.SkillExpandParams) (skillmodule.SkillExpandResult, error)
}

func (s stubSkillService) ListSkills(ctx context.Context) ([]skillmodule.SkillInfo, error) {
	if s.listSkills == nil {
		return nil, nil
	}
	return s.listSkills(ctx)
}

func (s stubSkillService) Expand(ctx context.Context, p skillmodule.SkillExpandParams) (skillmodule.SkillExpandResult, error) {
	if s.expand == nil {
		return skillmodule.SkillExpandResult{}, nil
	}
	return s.expand(ctx, p)
}

func TestSkillToolDefinitionsExposeExpectedSchemas(t *testing.T) {
	defs := skillToolDefinitions(nil)
	if len(defs) != 2 {
		t.Fatalf("len(skillToolDefinitions) = %d, want 2", len(defs))
	}
	list := mustToolDefinition(t, defs, "skill_list")
	expand := mustToolDefinition(t, defs, "skill_expand")

	listProps := list.InputSchema["properties"].(map[string]any)
	if _, ok := listProps["keyword"]; !ok {
		t.Fatalf("skill_list schema missing keyword: %#v", list.InputSchema)
	}
	if !strings.Contains(list.Description, "omits internal fields") {
		t.Fatalf("skill_list description = %q", list.Description)
	}

	expandProps := expand.InputSchema["properties"].(map[string]any)
	for _, key := range []string{"name", "section", "max_bytes"} {
		if _, ok := expandProps[key]; !ok {
			t.Fatalf("skill_expand schema missing %q: %#v", key, expand.InputSchema)
		}
	}
	required := expand.InputSchema["required"].([]string)
	if len(required) != 1 || required[0] != "name" {
		t.Fatalf("skill_expand required = %#v, want [name]", required)
	}
	if !strings.Contains(expand.Description, "full SKILL.md body") || !strings.Contains(expand.Description, "20000") {
		t.Fatalf("skill_expand description = %q", expand.Description)
	}
}

func TestHandleSkillListRedactsInternalFieldsAndFiltersKeyword(t *testing.T) {
	svc := newSkillToolTestService(t)
	handler := HandleSkillList(svc)

	value, err := handler(context.Background(), mustRawInput(t, skillListInput{Keyword: "rpc"}))
	if err != nil {
		t.Fatalf("HandleSkillList() error = %v", err)
	}
	result, ok := value.([]skillListDTO)
	if !ok || len(result) != 1 || result[0].Name != "rpc-tracing" {
		t.Fatalf("HandleSkillList() result = %#v", value)
	}
	raw, err := json.Marshal(result[0])
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(raw), `"dir"`) || strings.Contains(string(raw), `"trigger_words"`) {
		t.Fatalf("skill_list leaked internal fields: %s", raw)
	}
}

func TestHandleSkillExpandReturnsExpandResult(t *testing.T) {
	svc := newSkillToolTestService(t)
	handler := HandleSkillExpand(svc)

	value, err := handler(context.Background(), mustRawInput(t, skillExpandInput{Name: "rpc-tracing", Section: "references/api.md"}))
	if err != nil {
		t.Fatalf("HandleSkillExpand() error = %v", err)
	}
	result, ok := value.(skillmodule.SkillExpandResult)
	if !ok {
		t.Fatalf("HandleSkillExpand() type = %T, want skill.SkillExpandResult", value)
	}
	if result.Section != "references/api.md" || result.Content != "api docs" || !strings.HasSuffix(result.Path, filepath.Join("references", "api.md")) {
		t.Fatalf("HandleSkillExpand() result = %+v", result)
	}
}

func TestHandleSkillExpandMapsInvalidParamsToToolError(t *testing.T) {
	svc := newSkillToolTestService(t)
	handler := HandleSkillExpand(svc)
	zero := int64(0)

	value, err := handler(context.Background(), mustRawInput(t, skillExpandInput{Name: "rpc-tracing", MaxBytes: &zero}))
	if err != nil {
		t.Fatalf("HandleSkillExpand() error = %v", err)
	}
	result := requireToolErrorResult(t, value)
	if !strings.Contains(result.Content[0].Text, "invalid params") {
		t.Fatalf("tool error text = %q, want invalid params", result.Content[0].Text)
	}
}

func TestHandleSkillExpandMapsNotFoundToToolErrorWithAvailableSkills(t *testing.T) {
	svc := newSkillToolTestService(t)
	handler := HandleSkillExpand(svc)

	value, err := handler(context.Background(), mustRawInput(t, skillExpandInput{Name: "missing"}))
	if err != nil {
		t.Fatalf("HandleSkillExpand() error = %v", err)
	}
	result := requireToolErrorResult(t, value)
	if !strings.Contains(result.Content[0].Text, `skill "missing" not found`) || !strings.Contains(result.Content[0].Text, "rpc-tracing") {
		t.Fatalf("tool error text = %q", result.Content[0].Text)
	}
}

func TestHandleSkillExpandBubblesUnexpectedErrors(t *testing.T) {
	handler := HandleSkillExpand(stubSkillService{expand: func(context.Context, skillmodule.SkillExpandParams) (skillmodule.SkillExpandResult, error) {
		return skillmodule.SkillExpandResult{}, errors.New("boom")
	}})

	_, err := handler(context.Background(), mustRawInput(t, skillExpandInput{Name: "rpc-tracing"}))
	if err == nil || err.Error() != "boom" {
		t.Fatalf("HandleSkillExpand() error = %v", err)
	}
}

func mustToolDefinition(t *testing.T, defs []ToolDefinition, name string) ToolDefinition {
	t.Helper()
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("tool definition %q not found", name)
	return ToolDefinition{}
}

func requireToolErrorResult(t *testing.T, value any) toolErrorResult {
	t.Helper()
	result, ok := value.(toolErrorResult)
	if !ok || !result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("tool error result = %#v", value)
	}
	return result
}

func newSkillToolTestService(t *testing.T) skillmodule.Service {
	t.Helper()
	t.Setenv("SKILLS_ROOT", t.TempDir())
	projectRoot := t.TempDir()
	writeSkillToolTestFile(t, filepath.Join(projectRoot, ".agent", "skills", "rpc-tracing", "SKILL.md"), "---\nname: rpc-tracing\ndescription: Inspect RPC flow\nsummary: Inspect RPC flow in detail\n---\n# Overview\nintro\n")
	writeSkillToolTestFile(t, filepath.Join(projectRoot, ".agent", "skills", "rpc-tracing", "references", "api.md"), "api docs")
	writeSkillToolTestFile(t, filepath.Join(projectRoot, ".agent", "skills", "build-docs", "SKILL.md"), "---\nname: build-docs\ndescription: Build docs\nsummary: Build the documentation\n---\n# Overview\ndocs\n")
	return skillmodule.NewService(projectRoot)
}

func writeSkillToolTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
