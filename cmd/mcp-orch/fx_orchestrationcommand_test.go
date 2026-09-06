package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"go.uber.org/fx"
)

func TestCanonicalOwnerFxGraphConstructsCommandOwner(t *testing.T) {
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "command-owner.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatalf("command owner migration: %v", err)
	}
	var store *orchestrationcommand.Store
	var coordinator *orchestrationcommand.Coordinator
	var verifier orchestrationcommand.ReceiptVerifier
	app := fx.New(
		fx.NopLogger,
		fx.Supply(db),
		canonicalOwnerModules(),
		fx.Populate(&store, &coordinator, &verifier),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("command owner Fx construction: %v", err)
	}
	if store == nil || coordinator == nil || verifier == nil {
		t.Fatalf("command owner instances = store:%p coordinator:%p verifier:%T", store, coordinator, verifier)
	}
	if _, ok := verifier.(*orchestrationcommand.UnavailableReceiptVerifier); !ok {
		t.Fatalf("default command receipt verifier = %T, want *UnavailableReceiptVerifier", verifier)
	}
}

func TestCanonicalOwnerFxGraphRejectsNilCommandDatabase(t *testing.T) {
	var db *sql.DB
	var store *orchestrationcommand.Store
	app := fx.New(
		fx.NopLogger,
		fx.Supply(db),
		canonicalOwnerModules(),
		fx.Populate(&store),
	)
	if err := app.Start(t.Context()); err == nil {
		_ = app.Stop(t.Context())
		t.Fatal("canonical owner Fx graph accepted typed-nil command database")
	}
}
