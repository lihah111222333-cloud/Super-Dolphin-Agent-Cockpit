package main

import (
	"os"
	"path/filepath"
	"testing"
)

const recoveryTestGeneration = "00112233445566778899aabbccddeeff"

func realUpdaterTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
