package app

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendAppBuildFeedsAgentTerminalEmbedBundle(t *testing.T) {
	makefile := readFrontendEmbedContractFile(t, "../../Makefile")
	frontendGo := readFrontendEmbedContractFile(t, "../../cmd/agent-terminal/frontend.go")

	requiredMakefileTokens := []string{
		"FRONTEND_APP_DIR := frontend-app",
		"LEGACY_FRONTEND_DIR := cmd/agent-terminal/frontend",
		"frontend-build: frontend-app-build",
		"frontend-app-build: frontend-app-deps",
		"cd $(FRONTEND_APP_DIR) && $(NPM) run build",
		"test -f $(FRONTEND_APP_DIR)/dist/index.html",
		"node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs",
		"test -f $(LEGACY_FRONTEND_DIR)/dist/index.html",
	}
	for _, want := range requiredMakefileTokens {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile frontend-app embed contract missing %q", want)
		}
	}
	assertFrontendEmbedContractTextOrder(t, makefile, "cd $(FRONTEND_APP_DIR) && $(NPM) run build", "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs")
	assertFrontendEmbedContractTextOrder(t, makefile, "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs", "test -f $(LEGACY_FRONTEND_DIR)/dist/index.html")

	if strings.Contains(makefile, "frontend-build: frontend-legacy-build") {
		t.Fatal("frontend-build must not silently switch back to the legacy frontend package")
	}
	requiredFrontendGoTokens := []string{
		"//go:embed all:frontend/dist",
		`fs.Sub(frontendDist, "frontend/dist")`,
		"current React/Vite frontend-app build is copied into this embed path",
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
