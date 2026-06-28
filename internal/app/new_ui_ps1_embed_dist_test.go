package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewUIPowerShellScriptBuildsEmbeddedFrontendDist(t *testing.T) {
	text := readRootScriptForEmbedDistTest(t, "../../run-new-ui-desktop.ps1")

	required := []string{
		`function Test-PathTreeNewerThanFile`,
		`function Ensure-EmbeddedFrontendDist`,
		`$embeddedIndex = Join-Path $ProjectDir 'cmd\agent-terminal\frontend\dist\index.html'`,
		`'frontend-app\scripts\sync-frontend-dist.mjs'`,
		`Write-Host '  -> building embedded frontend dist'`,
		`& $npm run build`,
		`& node (Join-Path $FrontendAppDir 'scripts\sync-frontend-dist.mjs')`,
		`throw "embedded frontend dist missing after sync: $embeddedIndex"`,
		`Ensure-EmbeddedFrontendDist`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.ps1 missing %q", want)
		}
	}
	assertTextOrderForEmbedDistTest(t, text, `function Test-PathTreeNewerThanFile`, `function Ensure-EmbeddedFrontendDist`)
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	startupOrder := "Ensure-NodeDeps -Dir $FrontendAppDir\n    Ensure-EmbeddedFrontendDist\n    Ensure-PeerBinaries"
	if !strings.Contains(normalized, startupOrder) {
		t.Fatalf("run-new-ui-desktop.ps1 must build embedded frontend dist between node deps and peer binaries")
	}
	assertTextOrderForEmbedDistTest(t, text, `Wait-ForHttp -Url $env:FRONTEND_DEVSERVER_URL -Label 'frontend-app vite'`, `$script:DesktopProcess = Start-Process`)
}

func readRootScriptForEmbedDistTest(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	return string(data)
}

func assertTextOrderForEmbedDistTest(t *testing.T, text, first, second string) {
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
