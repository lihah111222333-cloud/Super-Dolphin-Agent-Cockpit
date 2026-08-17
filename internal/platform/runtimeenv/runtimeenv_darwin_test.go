//go:build darwin

package runtimeenv

import (
	"path/filepath"
	"reflect"
	"testing"
)

// 该测试依赖 macOS .app 主程序布局，不能通过宿主运行时 Skip 隐藏平台约束。
func TestPackagedRuntimeFromExecutableDetectsMacOSAppMainBinary(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(app, "Contents", "Resources")
	writePackagedRuntimeFixture(t, resources, runtimeGOOS()+"-"+runtimeGOARCH())

	got, ok := PackagedRuntimeFromExecutable(filepath.Join(app, "Contents", "MacOS", "agent-terminal"), "/Users/alice")
	if !ok {
		t.Fatal("PackagedRuntimeFromExecutable ok = false, want true")
	}
	if got.ResourcesDir != resources {
		t.Fatalf("ResourcesDir = %q", got.ResourcesDir)
	}
	if got.BinDir != filepath.Join(resources, "bin") {
		t.Fatalf("BinDir = %q", got.BinDir)
	}
	if got.MigrationsDir != filepath.Join(resources, "internal", "platform", "db", "sqlite", "migrations") {
		t.Fatalf("MigrationsDir = %q", got.MigrationsDir)
	}
	if _, ok := reflect.TypeFor[PackagedRuntime]().FieldByName("PostgresRoot"); ok {
		t.Fatalf("PackagedRuntime exposes PostgresRoot after SQLite switch: %#v", got)
	}
	if got.AppDataDir != "/Users/alice/Library/Application Support/Super Dolphin" {
		t.Fatalf("AppDataDir = %q", got.AppDataDir)
	}
}

// 该测试验证 macOS 资源目录中的 sidecar 不会被误判为主程序。
func TestPackagedRuntimeFromExecutableRejectsMacOSResourcePeerBinary(t *testing.T) {
	_, ok := PackagedRuntimeFromExecutable("/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch", "/Users/alice")
	if ok {
		t.Fatal("PackagedRuntimeFromExecutable ok = true, want false for sidecar peer binary")
	}
}
