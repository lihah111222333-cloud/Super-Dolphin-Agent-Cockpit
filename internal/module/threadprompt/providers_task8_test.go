package threadprompt

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestAvailableExpertsProviderDoesNotCapTotalCandidatesAtDefaultRosterSize(t *testing.T) {
	t.Parallel()

	templates := task8UserExpertTemplates(9)
	provider := AvailableExpertsProvider{catalog: newRuntimeCatalog(&fakePromptStore{templates: templates}, nil)}
	text := requireAvailableExpertsText(t, provider, "多个专家一起处理")
	for _, template := range templates {
		if !strings.Contains(text, template.PromptKey) {
			t.Fatalf("Resolve() = %q, want %s visible; available_experts must not cap total candidates at 8", text, template.PromptKey)
		}
	}
}

func TestAvailableExpertsProviderKeepsUserOwnedRetiredDuplicateKeys(t *testing.T) {
	t.Parallel()

	provider := AvailableExpertsProvider{catalog: newRuntimeCatalog(&fakePromptStore{
		templates: task8UserOwnedRetiredDuplicateTemplates(),
	}, nil)}
	text := requireAvailableExpertsText(t, provider, "实现、重构并补测试")
	for _, want := range []string{"main/code-generate", "main/code-refactor", "main/code-test"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Resolve() = %q, want user-owned %s retained", text, want)
		}
	}
}

func task8UserExpertTemplates(count int) []PromptTemplate {
	templates := make([]PromptTemplate, 0, count)
	for idx := 1; idx <= count; idx++ {
		key := fmt.Sprintf("user/expert-%02d", idx)
		templates = append(templates, expertTemplate(key, idx, fmt.Sprintf("用户专家 %02d", idx)))
	}
	return templates
}

func task8UserOwnedRetiredDuplicateTemplates() []PromptTemplate {
	return []PromptTemplate{
		{PromptKey: "main/code-generate", Title: "User Code Generate", WhenToUse: "用户创建的新功能实现专家。", Enabled: true, CreatedBy: "rpc.prompts", UpdatedBy: "rpc.prompts"},
		{PromptKey: "main/code-refactor", Title: "User Code Refactor", WhenToUse: "用户更新的重构专家。", Enabled: true, CreatedBy: "system.seed", UpdatedBy: "rpc.prompts"},
		{PromptKey: "main/code-test", Title: "User Code Test", WhenToUse: "用户手工编辑的测试专家。", Enabled: true, CreatedBy: "system.seed", UpdatedBy: "system.seed", ManuallyEdited: true},
	}
}

func requireAvailableExpertsText(t *testing.T, provider AvailableExpertsProvider, prompt string) string {
	t.Helper()
	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn: &contract.TurnInput{UserText: prompt, CWD: "/repo/a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want expert list")
	}
	return *text
}
