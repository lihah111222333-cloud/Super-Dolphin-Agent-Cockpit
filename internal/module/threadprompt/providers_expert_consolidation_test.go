package threadprompt

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestAvailableExpertsProviderUsesConsolidatedDeveloperExpertRoster(t *testing.T) {
	t.Parallel()

	provider := AvailableExpertsProvider{catalog: newRuntimeCatalog(&fakePromptStore{
		templates: []PromptTemplate{
			expertTemplate("main/code-review", 30, "代码审查、diff 风险评估、回归与安全问题检查"),
			expertTemplate("main/code-debug", 30, "错误排查、panic/exception/traceback 分析、最小复现定位"),
			expertTemplate("main/code-task", 20, "通用编程实现、重构、解释代码、补测试，覆盖合并后的日常开发任务。"),
			expertTemplate("main/planning", 20, "阶段化规格、实施计划、依赖、风险和用户确认"),
		},
	}, nil)}

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn: &contract.TurnInput{UserText: "实现功能、重构、解释代码并补测试", CWD: "/repo/a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want consolidated developer experts")
	}
	for _, want := range []string{"main/code-task", "main/code-review", "main/code-debug", "main/planning"} {
		if !strings.Contains(*text, want) {
			t.Fatalf("Resolve() = %q, want %q visible", *text, want)
		}
	}
	for _, retired := range []string{"main/code-generate", "main/code-refactor", "main/code-test", "main/code-explain"} {
		if strings.Contains(*text, retired) {
			t.Fatalf("Resolve() = %q, want retired duplicate %q absent", *text, retired)
		}
	}
}
