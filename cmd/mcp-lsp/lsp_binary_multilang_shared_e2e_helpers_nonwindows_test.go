//go:build e2e && !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFakeMultilangDiagnosticsLangservers(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range fakeMultilangDiagnosticsLangserverNames {
		script := "#!/bin/sh\n" +
			fakeMultilangDiagnosticsEnv + "=1 " + fakeMultilangServerEnv + "=" + shellQuote(name) +
			" exec " + shellQuote(os.Args[0]) +
			" -test.run=TestFakeMultilangDiagnosticsLangserverHelper -- \"$@\"\n"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	return dir
}

// startFakeMultilangDiagnosticsClientForTest 在非 Windows 上保持 PATH fake
// server 装配；Windows 的 package trust 只在对应平台 helper 中处理。
func startFakeMultilangDiagnosticsClientForTest(t *testing.T, ctx context.Context, binary, root, fakeServersBinDir string, extraEnv []string, _ string) *mcpLSPBinaryClient {
	t.Helper()
	return startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, extraEnv)
}
