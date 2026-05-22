package threadprompt

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func TestAvailableExpertsProviderRendersRosterRepairExperts(t *testing.T) {
	t.Parallel()

	provider := AvailableExpertsProvider{catalog: NewRuntimeCatalog(&fakePromptStore{
		templates: []promptstore.PromptTemplate{
			expertTemplate("main/git-ops", 20, "Git diff/log/blame、commit message、冲突解决、revert/cherry-pick"),
			expertTemplate("main/docs", 20, "README、API 文档、注释、changelog、技术文档结构化"),
			expertTemplate("main/orchestrator", 20, "多 agent 协作、拆分任务、并行子任务、跨领域协调和结果汇总"),
		},
	}, nil)}

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn:     &contract.TurnInput{UserText: "拆分任务以及更新文档和 git commit", CWD: "/repo/a"},
		BuildCtx: contract.BuildCtx{CWD: "/repo/a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want roster repair experts")
	}
	for _, want := range []string{"main/git-ops", "main/docs", "main/orchestrator"} {
		if !strings.Contains(*text, "prompt_key='"+want+"'") {
			t.Fatalf("Resolve() = %q, want %s", *text, want)
		}
	}
}
