//go:build windows

package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// TestRuntimeServerArgsDropsProductSQLSLSPArgumentOnWindows 锁定 Windows
// product-owned sqls.exe 的真实启动形状：v0.2.48 根 action 直接进入 LSP，
// 不存在正式 lsp 子命令；这里只移除 sqruff adapter 遗留的 lsp 参数。
func TestRuntimeServerArgsDropsProductSQLSLSPArgumentOnWindows(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	command := multilsp.ServerCommand{Executable: "sqruff", Args: []string{"lsp"}}
	binary := filepath.Join(productRoot, "cache", "lsp-assets", "runtime-dependencies", "go-sqls", "arm64", "bin", "sqls.exe")
	args, err := runtimeServerArgsPlatform(command, binary, nil)
	if err != nil {
		t.Fatalf("runtimeServerArgsPlatform(product SQLS) error = %v", err)
	}
	if want := []string{}; !reflect.DeepEqual(args, want) {
		t.Fatalf("runtimeServerArgsPlatform(product SQLS) = %#v, want %#v", args, want)
	}
}

// TestRuntimeServerArgsRejectsNonCanonicalProductSQLSArgsOnWindows 防止静默删除
// 部分参数掩盖 adapter 漂移；product-owned SQLS 只接受精确的 ["lsp"]。
func TestRuntimeServerArgsRejectsNonCanonicalProductSQLSArgsOnWindows(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	binary := filepath.Join(productRoot, "cache", "lsp-assets", "runtime-dependencies", "go-sqls", "arm64", "bin", "sqls.exe")
	for _, args := range [][]string{{}, {"lsp", "--trace"}, {"lsp", "lsp"}, {"LSP"}} {
		command := multilsp.ServerCommand{Executable: "sqruff", Args: args}
		if _, err := runtimeServerArgsPlatform(command, binary, nil); err == nil {
			t.Errorf("runtimeServerArgsPlatform(product SQLS args=%#v) error = nil, want fail-fast", args)
		}
	}
}

// TestRuntimeServerArgsLeavesForeignSQLSArgumentOnWindows 防止同名外部 sqls.exe
// 借助 marker 碰撞触发 product 参数改写；只有当前产品根归属才允许移除 lsp。
func TestRuntimeServerArgsLeavesForeignSQLSArgumentOnWindows(t *testing.T) {
	productRoot := t.TempDir()
	foreignRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	command := multilsp.ServerCommand{Executable: "sqruff", Args: []string{"lsp"}}
	binary := filepath.Join(foreignRoot, "cache", "lsp-assets", "runtime-dependencies", "go-sqls", "arm64", "bin", "sqls.exe")
	args, err := runtimeServerArgsPlatform(command, binary, nil)
	if err != nil {
		t.Fatalf("runtimeServerArgsPlatform(foreign SQLS) error = %v", err)
	}
	if want := []string{"lsp"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("runtimeServerArgsPlatform(foreign SQLS) = %#v, want %#v", args, want)
	}
}
