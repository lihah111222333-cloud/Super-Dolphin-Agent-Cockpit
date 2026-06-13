package embeddedpg

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestStartNoopsWhenDisabled(t *testing.T) {
	if err := Start(context.Background(), contract.EmbeddedPostgresConfig{}); err != nil {
		t.Fatalf("Start(disabled) error = %v", err)
	}
}

func TestStartNoopsWhenNotOwner(t *testing.T) {
	cfg := contract.EmbeddedPostgresConfig{
		Enabled: true,
		Owner:   false,
		BinDir:  filepath.Join(t.TempDir(), "missing-bin"),
	}
	if err := Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start(non-owner) error = %v", err)
	}
}

func TestStopNoopsWhenNotOwner(t *testing.T) {
	cfg := contract.EmbeddedPostgresConfig{
		Enabled: true,
		Owner:   false,
		BinDir:  filepath.Join(t.TempDir(), "missing-bin"),
	}
	if err := Stop(context.Background(), cfg); err != nil {
		t.Fatalf("Stop(non-owner) error = %v", err)
	}
}
