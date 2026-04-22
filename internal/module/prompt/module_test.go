package prompt

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"
	"go.uber.org/fx"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

type promptHandlerCollector struct {
	fx.In

	Maps []handler.Map `group:"rpc_handlers"`
}

type noopPromptStore struct{}

func (noopPromptStore) List(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	return []promptstore.PromptTemplate{}, nil
}

func (noopPromptStore) WithTx(_ context.Context, fn func(promptstore.Store) error) error {
	return fn(noopPromptStore{})
}

func (noopPromptStore) Get(context.Context, string) (*promptstore.PromptTemplate, error) {
	return nil, platformdb.ErrNotFound
}

func (noopPromptStore) Delete(context.Context, string) error { return nil }

func (noopPromptStore) InsertVersion(context.Context, promptstore.PromptTemplateVersion) (int64, error) {
	return 0, nil
}

func (noopPromptStore) Upsert(context.Context, promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	template := promptstore.PromptTemplate{}
	return &template, nil
}

func (noopPromptStore) ListSectionsByTemplateID(context.Context, int64) ([]promptstore.PromptTemplateSection, error) {
	return nil, nil
}

func (noopPromptStore) UpsertSection(_ context.Context, section promptstore.PromptTemplateSection) (*promptstore.PromptTemplateSection, error) {
	copy := section
	return &copy, nil
}

func (noopPromptStore) DeleteSection(context.Context, int64, string) error {
	return nil
}

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
		fx.Supply(&platformconfig.Config{}),
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
	for _, method := range []string{"prompts/list", "prompts/write", "prompts/delete"} {
		if merged[method] == nil {
			t.Fatalf("handler %q not registered", method)
		}
	}
}
