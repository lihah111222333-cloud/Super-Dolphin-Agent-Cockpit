package observability

import (
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

func TestModuleProvidesExplicitDisabledService(t *testing.T) {
	t.Setenv("OBS_TRACING_ENABLED", "0")
	var svc *Service
	app := fxtest.New(t,
		Module,
		fx.Supply(&config.Config{ProjectRoot: t.TempDir()}),
		fx.Populate(&svc),
	)
	app.RequireStart().RequireStop()
	status := svc.Status()
	if status.Enabled {
		t.Fatalf("Enabled = true, want disabled")
	}
	if status.DisabledReason == "" {
		t.Fatalf("DisabledReason is empty")
	}
}

func TestModuleEnabledFailsFastForInvalidProjectRoot(t *testing.T) {
	t.Setenv("OBS_TRACING_ENABLED", "1")
	var svc *Service
	app := fx.New(
		Module,
		fx.Supply(&config.Config{ProjectRoot: string('/')}),
		fx.Populate(&svc),
		fx.NopLogger,
	)
	if err := app.Err(); err == nil {
		t.Fatalf("fx.New succeeded, want fail-fast invalid project root error")
	}
}

func TestDisabledServiceIsQueryableAndDoesNotRecord(t *testing.T) {
	sink := &recordingSink{}
	svc := NewDisabledService(Config{DisabledReason: "unit disabled"})
	if err := svc.Record(t.Context(), TraceEvent{TraceID: "trace", Status: StatusError}); err != nil {
		t.Fatalf("Record disabled: %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("disabled service wrote %d events", len(sink.events))
	}
	if got := svc.Query(t.Context(), Query{}); len(got.Events) != 0 {
		t.Fatalf("disabled query events = %d, want 0", len(got.Events))
	}
	if status := svc.Status(); status.Enabled || status.DisabledReason != "unit disabled" {
		t.Fatalf("status = %+v", status)
	}
}
