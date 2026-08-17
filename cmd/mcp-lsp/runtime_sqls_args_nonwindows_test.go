//go:build !windows

package main

import (
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// TestRuntimeServerArgsKeepsSqruffLSPArgumentOutsideWindows 锁定非 Windows
// 现有 sqruff adapter 契约，Windows 的 product-owned SQLS 特例不能泄漏到其他平台。
func TestRuntimeServerArgsKeepsSqruffLSPArgumentOutsideWindows(t *testing.T) {
	command := multilsp.ServerCommand{Executable: "sqruff", Args: []string{"lsp"}}
	args, err := runtimeServerArgsPlatform(command, "sqruff", nil)
	if err != nil {
		t.Fatalf("runtimeServerArgsPlatform(sqruff) error = %v", err)
	}
	if want := []string{"lsp"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("runtimeServerArgsPlatform(sqruff) = %#v, want %#v", args, want)
	}
}
