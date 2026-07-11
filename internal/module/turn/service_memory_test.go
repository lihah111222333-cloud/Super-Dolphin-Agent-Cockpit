package turn

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

type turnContextProviderFunc func(context.Context, contract.Session, contract.BuildCtx, string, string) contract.TurnContextPayload

func (fn turnContextProviderFunc) PrepareTurnContext(ctx context.Context, session contract.Session, buildCtx contract.BuildCtx, threadID, query string) contract.TurnContextPayload {
	return fn(ctx, session, buildCtx, threadID, query)
}

type failingMemoryTurnContextProvider struct {
	err     error
	absPath string
}

func (p failingMemoryTurnContextProvider) PrepareTurnContext(context.Context, contract.Session, contract.BuildCtx, string, string) contract.TurnContextPayload {
	return contract.TurnContextPayload{
		Inputs: []InputItem{{
			Type:    "filecontent",
			Name:    "Memory prefetch error",
			Content: "memory prefetch failed:\n" + p.absPath,
		}},
	}
}

func (p failingMemoryTurnContextProvider) PrepareTurnContextWithError(context.Context, contract.Session, contract.BuildCtx, string, string) (contract.TurnContextPayload, error) {
	return contract.TurnContextPayload{}, p.err
}

func TestPrepareTurnPrependsSyntheticMemoryInputs(t *testing.T) {
	assembly := &stubPromptAssemblyService{turn: contract.TurnAssembly{UserContextText: "assembled user context"}}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly).(*service)
	svc.turnContextProvider = turnContextProviderFunc(func(context.Context, contract.Session, contract.BuildCtx, string, string) contract.TurnContextPayload {
		return contract.TurnContextPayload{
			Inputs: []InputItem{{Type: "filecontent", Content: "Past context transcript:\nUse concise imperative commit messages."}},
			Attachments: []providerdto.AttachmentEnvelope{
				contract.NewRelevantMemoryAttachment(
					"project/commit-style.md",
					"Memory (saved today): project/commit-style.md:",
					"Use concise imperative commit messages.",
					time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
					720,
					false,
				),
			},
		}
	})
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
	if len(req.TurnAssembly.Attachments) != 1 {
		t.Fatalf("len(req.TurnAssembly.Attachments) = %d, want 1", len(req.TurnAssembly.Attachments))
	}
	if got := req.TurnAssembly.Attachments[0].Kind; got != providerdto.AttachmentKindRelevantMemory {
		t.Fatalf("attachment kind = %q, want %q", got, providerdto.AttachmentKindRelevantMemory)
	}
	if assembly.lastTurnInput.UserText != "please verify the cache" {
		t.Fatalf("last turn user text = %q, want original user text", assembly.lastTurnInput.UserText)
	}
}

func TestPrepareTurnReturnsMemoryContextErrorBeforeAssemblingProviderInputs(t *testing.T) {
	assembly := &stubPromptAssemblyService{turn: contract.TurnAssembly{UserContextText: "assembled user context"}}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly).(*service)
	absPath := filepath.Join(t.TempDir(), "memory", "broken.md")
	safeErr := errors.New("memory_prefetch_failed stage=prefetch")
	svc.turnContextProvider = failingMemoryTurnContextProvider{
		err:     safeErr,
		absPath: absPath,
	}
	session := &stubSession{threadID: "thread-1"}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{Prompt: "please verify memory"})
	if err == nil {
		if turnInputsContain(req.Inputs, absPath) {
			t.Fatalf("PrepareTurn() allowed memory error path %q into provider inputs: %#v", absPath, req.Inputs)
		}
		t.Fatalf("PrepareTurn() error = nil, want memory context failure before provider input assembly")
	}
	if !errors.Is(err, safeErr) {
		t.Fatalf("PrepareTurn() error = %v, want %v", err, safeErr)
	}
	if strings.Contains(err.Error(), absPath) {
		t.Fatalf("PrepareTurn() error leaked absolute memory path %q: %v", absPath, err)
	}
	if turnInputsContain(req.Inputs, absPath) {
		t.Fatalf("PrepareTurn() returned inputs containing absolute memory path %q: %#v", absPath, req.Inputs)
	}
}

func turnInputsContain(inputs []InputItem, needle string) bool {
	for _, input := range inputs {
		if strings.Contains(input.Name, needle) || strings.Contains(input.Content, needle) {
			return true
		}
	}
	return false
}
