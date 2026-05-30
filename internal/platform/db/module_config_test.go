package db

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

func TestNewPoolRejectsEmptyDatabaseURL(t *testing.T) {
	pool, err := NewPool(&config.Config{})
	if err == nil {
		if pool != nil {
			pool.Close()
		}
		t.Fatal("NewPool() error = nil, want empty DATABASE_URL fail-fast")
	}
	if pool != nil {
		t.Fatalf("NewPool() pool = %v, want nil on empty DATABASE_URL", pool)
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("NewPool() error = %v, want DATABASE_URL guidance", err)
	}
}

func TestNewPoolAllowsEmbeddedPostgresGeneratedDatabaseURL(t *testing.T) {
	cfg := &config.Config{DatabaseURL: "postgres://super_dolphin@localhost:55432/super_dolphin?sslmode=disable"}
	cfg.EmbeddedPostgres.Enabled = true
	cfg.EmbeddedPostgres.Owner = true

	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	t.Cleanup(pool.Close)
	if got := pool.Config().ConnConfig.Database; got != "super_dolphin" {
		t.Fatalf("pool database = %q, want embedded generated database", got)
	}
}

func TestCreateDatabaseSQLSanitizesTargetDB(t *testing.T) {
	got, err := createDatabaseSQL(`tenant_"; DROP DATABASE postgres; --`)
	if err != nil {
		t.Fatalf("createDatabaseSQL() error = %v", err)
	}
	want := `CREATE DATABASE "tenant_""; DROP DATABASE postgres; --"`
	if got != want {
		t.Fatalf("createDatabaseSQL() = %q, want %q", got, want)
	}
}

func TestCreateDatabaseSQLRejectsEmptyTargetDB(t *testing.T) {
	if _, err := createDatabaseSQL("  "); err == nil {
		t.Fatal("createDatabaseSQL() error = nil, want empty database name fail-fast")
	}
}
