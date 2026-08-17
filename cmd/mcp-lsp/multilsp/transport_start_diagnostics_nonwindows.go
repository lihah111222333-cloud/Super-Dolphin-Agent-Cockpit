//go:build !windows

package multilsp

import "os/exec"

// lspStartupDiagnosticFields 非 Windows 不改变既有 transport 日志行为。
// Windows 专用启动统计隔离在带 build tag 的文件中，避免污染其它平台。
func lspStartupDiagnosticFields(_ transportOptions, _ *exec.Cmd) []any { return nil }

func lspTransportWriteFailureFields(_ *transport, _ string, _ error) []any { return nil }
