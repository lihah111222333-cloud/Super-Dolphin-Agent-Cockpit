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
