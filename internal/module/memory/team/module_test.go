package team

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"go.uber.org/fx"
)

type stubPromptAssemblyService struct{}

func (stubPromptAssemblyService) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (stubPromptAssemblyService) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (stubPromptAssemblyService) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (stubPromptAssemblyService) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

func TestTeamSyncProvidedByModule(t *testing.T) {
	var (
		manager *TeamMemoryManager
		guard   *TeamMemoryGuard
		svc     *TeamSyncService
	)
	cfg := newTestConfig(filepath.Join(t.TempDir(), teamMemoryRootDirName))
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() Config { return cfg },
			func() contract.PromptAssemblyService { return stubPromptAssemblyService{} },
			func() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) },
		),
		Module,
		fx.Populate(&manager, &guard, &svc),
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
	if manager == nil {
		t.Fatal("TeamMemoryManager not provided by Module")
	}
	if guard == nil {
		t.Fatal("TeamMemoryGuard not provided by Module")
	}
	if svc == nil {
		t.Fatal("TeamSyncService not provided by Module")
	}
}
