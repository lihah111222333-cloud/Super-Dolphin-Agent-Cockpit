package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/taskcredential"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"go.uber.org/fx"
)

// TestCanonicalOwnerFxGraph 验证三类 canonical owner 可在同一 Fx 图解析。
func TestCanonicalOwnerFxGraph(t *testing.T) {
	err := fx.ValidateApp(
		fx.Provide(func() *sql.DB { return nil }),
		canonicalOwnerModules(),
	)
	if err != nil {
		t.Fatalf("canonical owner Fx graph: %v", err)
	}
}

// TestCanonicalOwnerFxGraphConstructsStores 验证构造器实际返回三个 owner 实例。
func TestCanonicalOwnerFxGraphConstructsStores(t *testing.T) {
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "owners.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatalf("canonical owner migration: %v", err)
	}
	var identities *identity.Store
	var events *eventstore.Store
	var waits *accesswait.Store
	var eventStatus accesswait.EventSourceStatusReader
	var roomGate contract.RoomCapabilityGate
	app := fx.New(
		fx.NopLogger,
		fx.Supply(db),
		canonicalOwnerModules(),
		fx.Populate(&identities, &events, &waits, &eventStatus, &roomGate),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("canonical owner Fx construction: %v", err)
	}
	if identities == nil || events == nil || waits == nil || eventStatus == nil || roomGate == nil {
		t.Fatalf("canonical owner instances = identity:%p event:%p accesswait:%p status:%T room:%T", identities, events, waits, eventStatus, roomGate)
	}
}

// TestCanonicalOwnerFxGraphConstructsTaskPorts 验证两类 task candidate 只以窄接口
// 从同一 identity/event owner 构造，不注册 unified tools 或 provider effect。
func TestCanonicalOwnerFxGraphConstructsTaskPorts(t *testing.T) {
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "task-ports.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatalf("task port migration: %v", err)
	}
	var resolver taskcredential.CurrentTaskPrincipalResolver
	var producer taskeventsource.TaskEventProducer
	app := fx.New(
		fx.NopLogger,
		fx.Supply(db),
		canonicalOwnerModules(),
		fx.Populate(&resolver, &producer),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("task owner Fx construction: %v", err)
	}
	if resolver == nil || producer == nil {
		t.Fatalf("task owner ports = resolver:%T producer:%T, want both non-nil", resolver, producer)
	}
}

// TestTaskIdentityEventOwnerProvidersFailClosed 锁定 owner adapter 的 nil 依赖拒绝。
func TestTaskIdentityEventOwnerProvidersFailClosed(t *testing.T) {
	var identityStore *identity.Store
	if reader, err := provideTaskPrincipalIdentityReader(identityStore); err == nil || reader != nil {
		t.Fatalf("provideTaskPrincipalIdentityReader(typed nil) = (%T,%v), want fail-closed", reader, err)
	}
	var eventStore *eventstore.Store
	if appender, err := provideTaskEventAppender(eventStore); err == nil || appender != nil {
		t.Fatalf("provideTaskEventAppender(typed nil) = (%T,%v), want fail-closed", appender, err)
	}
	if resolver, err := provideTaskPrincipalResolver(nil); err == nil || resolver != nil {
		t.Fatalf("provideTaskPrincipalResolver(nil) = (%T,%v), want fail-closed", resolver, err)
	}
	if producer, err := provideTaskEventProducer(nil); err == nil || producer != nil {
		t.Fatalf("provideTaskEventProducer(nil) = (%T,%v), want fail-closed", producer, err)
	}
}

// TestCanonicalOwnerFxGraphRejectsNilDatabase 验证 owner 图不会在数据库缺失时
// 静默退化为内存实现或构造半可用 task port。
func TestCanonicalOwnerFxGraphRejectsNilDatabase(t *testing.T) {
	var db *sql.DB
	var resolver taskcredential.CurrentTaskPrincipalResolver
	app := fx.New(
		fx.NopLogger,
		fx.Supply(db),
		canonicalOwnerModules(),
		fx.Populate(&resolver),
	)
	startCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Start(startCtx); err == nil {
		_ = app.Stop(context.Background())
		t.Fatal("canonical owner Fx graph accepted typed-nil database")
	}
}
