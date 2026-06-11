package prompt

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

type promptHandlerCollector struct {
	fx.In

	Maps []handler.Map `group:"rpc_handlers"`
}

type noopPromptStore struct{ promptstore.Store }

func TestNewServiceRegistersBuiltInSlots(t *testing.T) {
	svc := NewService(&Config{}, nil)
	want := len(StaticSections()) + len(DynamicSlotNames())
	if len(svc.Sections()) != want {
		t.Fatalf("len(Sections()) = %d, want %d", len(svc.Sections()), want)
	}
}

func TestNewPromptHandlersExposeLegacyPromptsMethods(t *testing.T) {
	t.Parallel()

	var collected promptHandlerCollector
	app := fx.New(
		fx.NopLogger,
		fx.Supply(&contract.Config{}),
		fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil))),
		fx.Provide(func() promptstore.Store { return noopPromptStore{} }),
		Module,
		fx.Populate(&collected),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("app.Stop() error = %v", err)
		}
	}()

	merged := handler.Map{}
	for _, handlers := range collected.Maps {
		for method, fn := range handlers {
			merged[method] = fn
		}
	}
	for _, method := range []string{"prompts/list", "prompt-assets/list", "prompts/get", "prompts/write", "prompts/delete"} {
		if merged[method] == nil {
			t.Fatalf("handler %q not registered", method)
		}
	}
}
