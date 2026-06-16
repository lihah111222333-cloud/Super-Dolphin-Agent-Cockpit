package archtest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFrontendAppShellSurfaceStaysSplit 防止 App.jsx 重新吸收路由和 store 订阅模型。
func TestFrontendAppShellSurfaceStaysSplit(t *testing.T) {
	t.Parallel()

	app := readFrontendShellGuardFile(t, "frontend-app", "src", "App.jsx")
	model := readFrontendShellGuardFile(t, "frontend-app", "src", "app", "appShellModel.js")

	for _, forbidden := range []string{
		"const PAGE_ROUTE_BY_ID",
		"const PAGE_ID_BY_ROUTE",
		"function selectAppShellStore",
	} {
		if strings.Contains(app, forbidden) {
			t.Fatalf("App.jsx must not own app shell model token %q", forbidden)
		}
	}
	for _, required := range []string{
		"APP_SHELL_STORE_KEYS",
		"appPageFromPathname",
		"appRouteForPage",
		"selectAppShellStore",
	} {
		if !strings.Contains(model, required) {
			t.Fatalf("appShellModel.js missing required shell model token %q", required)
		}
	}
}

func readFrontendShellGuardFile(t *testing.T, parts ...string) string {
	t.Helper()

	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	path := filepath.Join(append([]string{root}, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
