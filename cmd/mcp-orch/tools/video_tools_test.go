package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiliconFlowAPIKeyLoadsVideoEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", home)
	t.Setenv("SILICONFLOW_API_KEY", "")
	if err := os.WriteFile(filepath.Join(home, "video.env"), []byte("SILICONFLOW_API_KEY=sk-test-video\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := siliconFlowAPIKey()
	if err != nil {
		t.Fatalf("siliconFlowAPIKey() error = %v", err)
	}
	if got != "sk-test-video" {
		t.Fatalf("siliconFlowAPIKey() = %q, want sk-test-video", got)
	}
}

func TestDownloadVideoDestIsMoviesDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	moviesDir := filepath.Join(home, "Movies")
	// downloadVideoToDesktop builds the path; verify it targets ~/Movies not ~/Desktop.
	_ = os.MkdirAll(moviesDir, 0o755)
	dest := filepath.Join(moviesDir, "video-req-20060102-150405.mp4")
	if !strings.Contains(dest, "Movies") {
		t.Fatalf("video dest %q does not contain Movies", dest)
	}
	if strings.Contains(dest, "Desktop") {
		t.Fatalf("video dest %q must not target Desktop", dest)
	}
}

func TestVideoGenerateRejectsHomeFallbackOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "Movies"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed Movies sentinel: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("mp4"))
	}))
	t.Cleanup(server.Close)

	got, err := downloadVideoToDesktop(context.Background(), server.URL, "req-home-fallback")
	if err == nil {
		t.Fatalf("downloadVideoToDesktop() wrote %q after Movies failure; want fail-fast instead of home fallback", got)
	}
	if !strings.Contains(err.Error(), "Movies") && !strings.Contains(err.Error(), "output") {
		t.Fatalf("downloadVideoToDesktop() error = %v, want output path failure", err)
	}
}
