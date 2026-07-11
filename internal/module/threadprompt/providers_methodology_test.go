package threadprompt

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestAvailableExpertsProviderKeepsPlanningReviewDebugDistinct(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		templates: []PromptTemplate{
			{PromptKey: "main/planning", Title: "任务规划师", WhenToUse: "阶段化规格、实施计划、依赖、用户确认、验收点回链", Enabled: true, Tags: mustJSONTags("scope.global", "intent:expert")},
			{PromptKey: "main/code-review", Title: "代码审核专家", WhenToUse: "findings-first、严重等级、file:line、证据类型、缺测试", Enabled: true, Tags: mustJSONTags("scope.global", "intent:expert")},
			{PromptKey: "main/code-debug", Title: "调试专家", WhenToUse: "错误证据、最小复现、根因定位、验证闭环", Enabled: true, Tags: mustJSONTags("scope.global", "intent:expert")},
			{PromptKey: "main/code-task", Title: "通用编程助手", WhenToUse: "通用编程实现、重构、解释代码、补测试", Enabled: true, Tags: mustJSONTags("scope.global", "intent:expert")},
		},
	}
	provider := AvailableExpertsProvider{catalog: newRuntimeCatalog(store, nil)}

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{Prompt: "帮我做实施计划，review diff，再 debug panic", PromptKey: "main/default"},
		BuildCtx: contract.BuildCtx{CWD: "/repo-a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want expert list")
	}
	for _, want := range []string{"main/planning", "main/code-review", "main/code-debug", "main/code-task"} {
		if !strings.Contains(*text, want) {
			t.Fatalf("Resolve() = %q, want %q visible", *text, want)
		}
	}
}
