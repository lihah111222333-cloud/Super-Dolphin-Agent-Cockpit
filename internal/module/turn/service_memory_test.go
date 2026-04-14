package turn

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestPrepareTurnPrependsSyntheticMemoryInputs(t *testing.T) {
	assembly := &stubPromptAssemblyService{turn: contract.TurnAssembly{UserContextText: "assembled user context"}}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly).(*service)
	svc.prepareMemoryInputs = func(context.Context, contract.Session, contract.BuildCtx, string, string) []InputItem {
		return []InputItem{{Type: "filecontent", Content: "Memory (saved today): project/commit-style.md:\nUse concise imperative commit messages."}}
	}
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{Prompt: "please verify the cache"})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if len(req.Inputs) != 2 {
		t.Fatalf("len(req.Inputs) = %d, want 2", len(req.Inputs))
	}
	if req.Inputs[0].Type != "filecontent" {
		t.Fatalf("first input = %#v, want synthetic filecontent", req.Inputs[0])
	}
	if req.Inputs[1].Content != "please verify the cache" {
		t.Fatalf("second input = %#v, want original prompt text", req.Inputs[1])
	}
	if assembly.lastTurnInput.UserText != "please verify the cache" {
		t.Fatalf("last turn user text = %q, want original user text", assembly.lastTurnInput.UserText)
	}
}
