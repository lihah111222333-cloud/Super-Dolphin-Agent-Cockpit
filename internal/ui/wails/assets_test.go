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
