package memory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

func TestNewConfigFallsBackToProjectRoot(t *testing.T) {
	t.Setenv(envMemoryRoot, "")
	cfg := NewConfig(&platformconfig.Config{ProjectRoot: t.TempDir()})
	if cfg == nil || cfg.RootDir == "" {
		t.Fatalf("expected non-empty root dir, got %#v", cfg)
	}
}

func TestServiceEnsureRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	svc := NewService(&Config{RootDir: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.EnsureRoot(context.Background()); err != nil {
		t.Fatalf("EnsureRoot() error = %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", root, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", root)
	}
}

func TestServiceEnsureRootUsesAutoMemPathOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	override := filepath.Join(t.TempDir(), "override", "memory")
	svc := NewService(&Config{
		RootDir:             root,
		ProjectRoot:         filepath.Join(t.TempDir(), "project"),
		AutoMemPathOverride: override,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.EnsureRoot(context.Background()); err != nil {
		t.Fatalf("EnsureRoot() error = %v", err)
	}
	info, err := os.Stat(override)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", override, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", override)
	}
}

func TestRootManagerEnsureRootDelegatesToService(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root-manager")
	svc := NewService(&Config{RootDir: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager := NewRootManager(svc)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := manager.EnsureRoot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureRoot() error = %v, want %v", err, context.Canceled)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %q to stay absent, got err=%v", root, err)
	}
	if got := manager.RootDir(); got != root {
		t.Fatalf("RootDir() = %q, want %q", got, root)
	}
}
