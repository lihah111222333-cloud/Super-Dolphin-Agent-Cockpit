//go:build e2e && windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFakeMultilangDiagnosticsLangservers(t *testing.T) string {
	t.Helper()
	return writeFakeMultilangDiagnosticsLangserversWithBuilder(t, func(t *testing.T, target, _ string) {
		writeFakeWindowsMultilangGoplsExecutable(t, target, "")
	})
}

func writeFakeMultilangDiagnosticsLangserversWithBuilder(t *testing.T, build func(*testing.T, string, string)) string {
	t.Helper()
	dir := t.TempDir()
	genericPath := filepath.Join(dir, "generic-fake-multilang.exe")
	build(t, genericPath, "")
	payload, err := os.ReadFile(genericPath)
	if err != nil {
		t.Fatalf("read generic fake Windows multilang executable: %v", err)
	}
	for _, name := range fakeMultilangDiagnosticsLangserverNames {
		for _, executablePath := range []string{
			filepath.Join(dir, name+".exe"),
			filepath.Join(dir, name),
		} {
			if err := os.WriteFile(executablePath, payload, 0o700); err != nil {
				t.Fatalf("copy fake %s Windows executable: %v", name, err)
			}
		}
	}
	return dir
}

func TestWriteFakeMultilangDiagnosticsLangserversBuildsOnce(t *testing.T) {
	var buildCount int
	dir := writeFakeMultilangDiagnosticsLangserversWithBuilder(t, func(t *testing.T, target, _ string) {
		buildCount++
		if err := os.WriteFile(target, []byte("fake executable"), 0o700); err != nil {
			t.Fatalf("write fake executable: %v", err)
		}
	})
	if buildCount != 1 {
		t.Fatalf("fake Windows multilang executable build count = %d, want 1", buildCount)
	}
	for _, name := range fakeMultilangDiagnosticsLangserverNames {
		for _, path := range []string{filepath.Join(dir, name+".exe"), filepath.Join(dir, name)} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("stat fake %s Windows executable %s: %v", name, path, err)
			}
		}
	}
}
