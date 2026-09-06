package main

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/kelindar/event"
	directpeerstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"go.uber.org/fx"
)

// TestDirectPeerProjectorFxProviderProjectsOutbox 验证共享 Fx provider 会投影并确认 durable direct-peer outbox。
func TestDirectPeerProjectorFxProviderProjectsOutbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db := openDirectPeerProjectorTestDB(t)
	store, err := directpeerstore.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-1", true, true); err != nil {
		t.Fatal(err)
	}
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	published := make(chan directpeerstore.PublishedEvent, 1)
	cancelSubscription := event.Subscribe(dispatcher, func(item directpeerstore.PublishedEvent) { published <- item })
	t.Cleanup(cancelSubscription)

	var group struct {
		fx.In
		Runners []platformrunner.Runner `group:"runners"`
	}
	app := fx.New(
		fx.NopLogger,
		fx.Supply(store, dispatcher, slog.New(slog.NewTextHandler(io.Discard, nil))),
		fx.Provide(fx.Annotate(newDirectPeerEventProjector, fx.As(new(platformrunner.Runner)), fx.ResultTags(`group:"runners"`))),
		fx.Populate(&group),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if len(group.Runners) != 1 {
		t.Fatalf("projector runners=%d want=1", len(group.Runners))
	}

	runDone := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() { runDone <- group.Runners[0].Run(ctx) })
	select {
	case item := <-published:
		if item.Event.EventType != "grant.active" || item.Event.EventID == "" {
			t.Fatalf("published event=%#v", item.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("direct-peer projector did not publish the pending event")
	}
	waitForDirectPeerProjectorOutboxProjected(t, db)
	cancel()
	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("projector Run() error=%v, want context.Canceled", err)
	}
	assertDirectPeerProjectorOutboxProjected(t, db)
}

// TestDirectPeerProjectorFxWiringIsSinglePerGraph 锁定 legacy 与 standalone 各自只注册一个投影 runner。
func TestDirectPeerProjectorFxWiringIsSinglePerGraph(t *testing.T) {
	legacy := parseDirectPeerProjectorTestFile(t, "fx.go")
	legacyBuilder := findFuncDecl(legacy, "newMCPOrchApp")
	if got := countRunnerProviderAnnotations(legacyBuilder, "newDirectPeerEventProjector"); got != 1 {
		t.Fatalf("legacy projector providers=%d want=1", got)
	}
	standalone := parseDirectPeerProjectorTestFile(t, "fx_standalone.go")
	standaloneBuilder := findFuncDecl(standalone, "newStandaloneAppWithPrincipalIssuer")
	if got := countRunnerProviderAnnotations(standaloneBuilder, "newStandaloneDirectPeerEventProjector"); got != 1 {
		t.Fatalf("standalone projector providers=%d want=1", got)
	}
}

func openDirectPeerProjectorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "direct-peer-projector.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertDirectPeerProjectorOutboxProjected(t *testing.T, db *sql.DB) {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM orchestration_direct_peer_event_outbox`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "projected" {
		t.Fatalf("direct-peer outbox status=%q want=projected", status)
	}
}

func waitForDirectPeerProjectorOutboxProjected(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := db.QueryRow(`SELECT status FROM orchestration_direct_peer_event_outbox`).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "projected" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	assertDirectPeerProjectorOutboxProjected(t, db)
}

func parseDirectPeerProjectorTestFile(t *testing.T, name string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func countRunnerProviderAnnotations(fn *ast.FuncDecl, provider string) int {
	if fn == nil {
		return 0
	}
	count := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "Annotate") || len(call.Args) == 0 {
			return true
		}
		ident, ok := call.Args[0].(*ast.Ident)
		if ok && ident.Name == provider && callHasRunnerGroupTag(call.Args[1:]) {
			count++
		}
		return true
	})
	return count
}
