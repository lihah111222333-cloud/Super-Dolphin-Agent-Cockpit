package wails

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewRPCHandlersRegistersPathOpenRoute(t *testing.T) {
	t.Parallel()

	handlers := NewRPCHandlers(&App{}, nil, nil).Handlers
	if _, ok := handlers["ui/path/open"]; !ok {
		t.Fatal("handler ui/path/open is not registered")
	}
}

func TestResolvePathOpenTargetAllowsProjectFilesAndDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirTarget, dirInfo, err := resolvePathOpenTarget(context.Background(), "src", []string{root})
	if err != nil {
		t.Fatalf("resolvePathOpenTarget(directory) error = %v", err)
	}
	if !dirInfo.IsDir() {
		t.Fatal("resolvePathOpenTarget(directory) info is not a directory")
	}
	if dirTarget.Relative != "src" {
		t.Fatalf("resolvePathOpenTarget(directory) relative = %q, want src", dirTarget.Relative)
	}

	fileTarget, fileInfo, err := resolvePathOpenTarget(context.Background(), "src/main.go", []string{root})
	if err != nil {
		t.Fatalf("resolvePathOpenTarget(file) error = %v", err)
	}
	if fileInfo.IsDir() {
		t.Fatal("resolvePathOpenTarget(file) info is a directory")
	}
	if fileTarget.Abs != file {
		t.Fatalf("resolvePathOpenTarget(file) abs = %q, want %q", fileTarget.Abs, file)
	}
}

func TestResolvePathOpenTargetRejectsOutsideProject(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := resolvePathOpenTarget(context.Background(), filepath.Join("..", "outside", "secret.txt"), []string{root}); err == nil {
		t.Fatal("resolvePathOpenTarget(outside relative path) error = nil, want rejection")
	}
	if _, _, err := resolvePathOpenTarget(context.Background(), secret, []string{root}); err == nil {
		t.Fatal("resolvePathOpenTarget(outside absolute path) error = nil, want rejection")
	}
	if _, _, err := resolvePathOpenTarget(context.Background(), " ", []string{root}); err == nil {
		t.Fatal("resolvePathOpenTarget(empty path) error = nil, want rejection")
	}
}

func TestCodeOpenArgsUsesGotoOnlyForPositiveLine(t *testing.T) {
	t.Parallel()

	if got, want := codeOpenArgs("src", 0, 0), []string{"src"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("codeOpenArgs(directory) = %#v, want %#v", got, want)
	}
	if got, want := codeOpenArgs("src/main.go", 12, 0), []string{"-g", "src/main.go:12:1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("codeOpenArgs(file line) = %#v, want %#v", got, want)
	}
}
