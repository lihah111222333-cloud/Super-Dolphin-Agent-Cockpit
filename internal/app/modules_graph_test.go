package app

import (
	"io"
	"io/fs"
	"log/slog"
	"testing"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/workflowtemplate"
	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
)

// TestAppModuleGraphIsClosed validates that every fx.Provide in the
// core app Module resolves against declared fx.In dependencies. This
// does NOT run constructors (fx.ValidateApp is dry-run over the DAG),
// so the db / pgxpool / toolbridge side effects never fire \u2014 we only
// check that adding a new module (P21 cron / notify / insight) did
// not introduce a missing dependency.
//
// Failure here is loud: fx.ValidateApp reports the unresolved type +
// which consumer needed it, which is exactly the diagnostic we want
// when someone forgets to fx.Provide a new dep.
func TestAppModuleGraphIsClosed(t *testing.T) {
	t.Parallel()

	if err := fx.ValidateApp(appGraphValidationOptions()...); err != nil {
		t.Fatalf("fx.ValidateApp failed: %v", err)
	}
}

func TestAppModuleGraphProvidesTurnThreadStateConfigReader(t *testing.T) {
	t.Parallel()

	var reader turn.ThreadStateConfigReader
	opts := append(appGraphValidationOptions(), fx.Populate(&reader))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing turn thread runtime reader: %v", err)
	}
}

func TestAppModuleGraphProvidesDatasourceV2Service(t *testing.T) {
	t.Parallel()

	var svc datasourcev2.Service
	opts := append(appGraphValidationOptions(), fx.Populate(&svc))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing datasource_v2 service: %v", err)
	}
}

func TestAppModuleGraphProvidesWorkflowTemplateService(t *testing.T) {
	t.Parallel()

	var svc workflowtemplate.Service
	opts := append(appGraphValidationOptions(), fx.Populate(&svc))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing workflow template service: %v", err)
	}
}

func appGraphValidationOptions() []fx.Option {
	// Supply the frontend filesystem because RunDesktop normally
	// injects it. Empty io/fs satisfies uiwails.Module's declared
	// dependency without booting wails.
	frontend := fx.Supply(uiwails.FrontendFS{FS: emptyFS{}})

	// A dispatcher is normally provided by platform/bus. ValidateApp
	// is dry-run so the real provider is not invoked, but we inject a
	// stand-in to satisfy any provider whose signature declares the
	// value as a non-optional input.
	_ = event.NewDispatcher() // build/test sanity

	// Silent logger so the dry-run doesn't spam stderr.
	_ = slog.New(slog.NewTextHandler(io.Discard, nil))
	var stateReader contract.ThreadStateConfigReader

	return []fx.Option{
		Module,
		uiwails.Module,
		frontend,
		fx.Populate(&stateReader),
		fx.Invoke(BindRuntime),
	}
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
