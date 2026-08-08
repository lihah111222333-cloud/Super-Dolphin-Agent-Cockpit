package app

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendAppBuildFeedsAgentTerminalEmbedBundle(t *testing.T) {
	makefile := readFrontendEmbedContractFile(t, "../../Makefile")
	packageJSON := readFrontendEmbedContractFile(t, "../../frontend-app/package.json")
	frontendGo := readFrontendEmbedContractFile(t, "../../cmd/agent-terminal/frontend.go")

	requiredMakefileTokens := []string{
		"FRONTEND_APP_DIR := frontend-app",
		"EMBEDDED_FRONTEND_DIR := cmd/agent-terminal/web-dist",
		"frontend-build: frontend-app-build",
		"frontend-app-build: frontend-app-deps",
		"cd $(FRONTEND_APP_DIR) && $(NPM) run build",
		"test -f $(FRONTEND_APP_DIR)/dist/index.html",
		"test -f $(EMBEDDED_FRONTEND_DIR)/index.html",
	}
	for _, want := range requiredMakefileTokens {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile frontend-app embed contract missing %q", want)
		}
	}
	if strings.Contains(makefile, "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs") {
		t.Fatal("Makefile must not duplicate the npm build frontend embed sync")
	}
	if !strings.Contains(packageJSON, `"build": "vite build --configLoader runner && node scripts/sync-frontend-dist.mjs"`) {
		t.Fatal("frontend-app npm build must own the frontend embed sync")
	}
	assertFrontendEmbedContractTextOrder(t, makefile, "cd $(FRONTEND_APP_DIR) && $(NPM) run build", "test -f $(EMBEDDED_FRONTEND_DIR)/index.html")

	if strings.Contains(makefile, "frontend-build: frontend-legacy-build") {
		t.Fatal("frontend-build must not silently switch back to the legacy frontend package")
	}
	if strings.Contains(makefile, "frontend-legacy-build") || strings.Contains(makefile, "LEGACY_FRONTEND_DIR") {
		t.Fatal("Makefile must not keep legacy frontend package build targets")
	}
	requiredFrontendGoTokens := []string{
		"//go:embed all:web-dist",
		`fs.Sub(frontendDist, "web-dist")`,
		"当前 React/Vite frontend-app 复制到 web-dist",
	}
	for _, want := range requiredFrontendGoTokens {
		if !strings.Contains(frontendGo, want) {
			t.Fatalf("cmd/agent-terminal/frontend.go embed contract missing %q", want)
		}
	}
}

func readFrontendEmbedContractFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func assertFrontendEmbedContractTextOrder(t *testing.T, text, first, second string) {
	t.Helper()
	firstIndex := strings.Index(text, first)
	if firstIndex < 0 {
		t.Fatalf("missing first text %q", first)
	}
	secondIndex := strings.Index(text, second)
	if secondIndex < 0 {
		t.Fatalf("missing second text %q", second)
	}
	if firstIndex >= secondIndex {
		t.Fatalf("expected %q before %q", first, second)
	}
}
