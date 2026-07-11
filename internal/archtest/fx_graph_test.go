package archtest_test

import (
	"testing"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app"
)

func TestFxValidateApp(t *testing.T) {
	if err := fx.ValidateApp(app.Module); err != nil {
		t.Fatalf("fx.ValidateApp(app.Module): %v", err)
	}
}
