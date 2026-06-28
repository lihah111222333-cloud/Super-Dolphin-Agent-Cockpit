package wails

import "testing"

func TestAssetHandlerFromFailsFastOnInvalidViteDevURL(t *testing.T) {
	t.Setenv("VITE_DEV_URL", "://bad-dev-url")

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("expected invalid VITE_DEV_URL to fail fast")
		}
	}()

	_ = AssetHandlerFrom(FrontendFS{})
}

func TestViteDevProxyRejectsNonLoopbackURL(t *testing.T) {
	t.Setenv("VITE_DEV_URL", "http://192.0.2.10:5175")

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("expected non-loopback VITE_DEV_URL to fail fast")
		}
	}()

	_ = AssetHandlerFrom(FrontendFS{})
}

func TestAssetHandlerFromRejectsViteDevURLInProductionMode(t *testing.T) {
	t.Setenv("VITE_DEV_URL", "http://127.0.0.1:5175")

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected production VITE_DEV_URL to fail fast")
		}
		if recoveredMessage(recovered) != "invalid VITE_DEV_URL: production mode rejects dev URL" {
			t.Fatalf("panic = %v, want production mode rejection", recovered)
		}
	}()

	_ = AssetHandlerFrom(FrontendFS{})
}

func TestAssetHandlerFromAllowsViteDevURLInDebugMode(t *testing.T) {
	t.Setenv("VITE_DEV_URL", "http://127.0.0.1:5175")

	if handler := AssetHandlerFromForMode(FrontendFS{}, true); handler == nil {
		t.Fatal("debug VITE_DEV_URL handler is nil")
	}
}
