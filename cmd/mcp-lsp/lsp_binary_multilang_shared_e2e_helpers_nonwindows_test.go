//go:build e2e && !windows

package main

import (
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
