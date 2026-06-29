package wails

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveClipboardImageRouteReturnsPath(t *testing.T) {
	t.Parallel()

	png := validClipboardPNG()
	payload, err := json.Marshal(map[string]any{
		"base64Payload":  base64.StdEncoding.EncodeToString(png),
		"_aoClientKind":  "web-debug-shim",
		"_aoClientRoute": "/chat",
	})
	if err != nil {
		t.Fatalf("Marshal(ui/saveClipboardImage payload) error = %v", err)
	}
	server := newWailsRPCServer(t, &App{})
	raw, err := server.Dispatch(context.Background(), "ui/saveClipboardImage", payload)
	if err != nil {
		t.Fatalf("Dispatch(ui/saveClipboardImage) error = %v", err)
	}

	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/saveClipboardImage) error = %v", err)
	}
	if result.Path == "" {
		t.Fatal("ui/saveClipboardImage path is empty")
	}
	defer os.Remove(result.Path)
	if got, err := os.ReadFile(result.Path); err != nil || !bytes.Equal(got, png) {
		t.Fatalf("saved clipboard image = %v, %v; want png bytes, nil", got, err)
	}
}

func TestSaveTextFileRoutePromptsAndWritesChosenPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "final-report.md")
	var capturedDefaultPath string
	server := newWailsRPCServer(t, &App{
		saveDirectoryInvoker: func(defaultPath string) (string, error) {
			capturedDefaultPath = defaultPath
			return dir, nil
		},
	})

	raw, err := server.Dispatch(context.Background(), "ui/saveTextFile", json.RawMessage(`{
		"defaultPath":"/workspace/project",
		"defaultFilename":"reports/final-report.md",
		"content":"# Final report\nready",
		"_aoClientKind":"desktop-wails",
		"_aoClientRoute":"/shared-files"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/saveTextFile) error = %v", err)
	}

	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/saveTextFile) error = %v", err)
	}
	if result.Path != target {
		t.Fatalf("ui/saveTextFile path = %q, want %q", result.Path, target)
	}
	if capturedDefaultPath != "/workspace/project" {
		t.Fatalf("captured defaultPath = %q, want /workspace/project", capturedDefaultPath)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", target, err)
	}
	if string(got) != "# Final report\nready" {
		t.Fatalf("saved text = %q, want full content", got)
	}
}

func TestSaveTextFileRouteRejectsExistingTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "final-report.md")
	if err := os.WriteFile(target, []byte("existing"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	server := newWailsRPCServer(t, &App{
		saveDirectoryInvoker: func(defaultPath string) (string, error) {
			return dir, nil
		},
	})

	_, err := server.Dispatch(context.Background(), "ui/saveTextFile", json.RawMessage(`{
		"defaultFilename":"final-report.md",
		"content":"new content"
	}`))
	if err == nil {
		t.Fatal("Dispatch(ui/saveTextFile) error = nil, want existing target rejection")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("ReadFile(%q) error = %v", target, readErr)
	}
	if string(got) != "existing" {
		t.Fatalf("existing file content = %q, want unchanged", got)
	}
}

func TestSaveTextFileRouteTreatsCancelAsEmptyPath(t *testing.T) {
	t.Parallel()

	server := newWailsRPCServer(t, &App{
		saveDirectoryInvoker: func(defaultPath string) (string, error) {
			return "", nil
		},
	})

	raw, err := server.Dispatch(context.Background(), "ui/saveTextFile", json.RawMessage(`{
		"defaultPath":"/workspace/project",
		"defaultFilename":"final-report.md",
		"content":"# Final report"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/saveTextFile) error = %v", err)
	}

	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/saveTextFile) error = %v", err)
	}
	if result.Path != "" {
		t.Fatalf("ui/saveTextFile path = %q, want empty cancel path", result.Path)
	}
}
