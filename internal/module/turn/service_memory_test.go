package turn

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type countingTurnContextProvider struct {
	calls int
}

func (p *countingTurnContextProvider) PrepareTurnContext(context.Context, contract.Session, contract.BuildCtx, string, string) contract.TurnContextPayload {
	p.calls++
	return contract.TurnContextPayload{}
}

// TestPrepareTurnRejectsLegacyMemoryContextProviderWithoutCalling 验证 memory owner 下沉 CLI 后旧 provider 永不调用。
func TestPrepareTurnRejectsLegacyMemoryContextProviderWithoutCalling(t *testing.T) {
	assembly := &stubPromptAssemblyService{turn: contract.TurnAssembly{UserContextText: "assembled user context"}}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly, NewToolResultRuntime()).(*service)
	provider := &countingTurnContextProvider{}
	svc.turnContextProvider = provider

	_, err := svc.PrepareTurn(context.Background(), &stubSession{threadID: "thread-1"}, PrepareInput{Prompt: "please verify memory"})
	if !errors.Is(err, contract.ErrMemoryRuntimeUnsupported) {
		t.Fatalf("PrepareTurn() error = %v, want ErrMemoryRuntimeUnsupported", err)
	}
	if provider.calls != 0 {
		t.Fatalf("legacy turn context provider calls = %d, want 0", provider.calls)
	}
}

// TestPrepareTurnWithoutMemoryContextProviderKeepsNormalPath 验证未注入旧 provider 时默认 turn wiring 仍可用。
func TestPrepareTurnWithoutMemoryContextProviderKeepsNormalPath(t *testing.T) {
	assembly := &stubPromptAssemblyService{turn: contract.TurnAssembly{UserContextText: "assembled user context"}}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly, NewToolResultRuntime()).(*service)

	req, err := svc.PrepareTurn(context.Background(), &stubSession{threadID: "thread-1"}, PrepareInput{Prompt: "continue without memory"})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if len(req.Inputs) != 1 || req.Inputs[0].Content != "continue without memory" {
		t.Fatalf("PrepareTurn() inputs = %#v, want original prompt only", req.Inputs)
	}
}
