package codexapp

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func codexWheelForTest(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{
		"codex_cli_bin/bin/" + codexExecutableFileName(): fakeCodexBodyForTest(t),
		"codex_cli_bin/codex-path/rg":                    []byte("#!/bin/sh\n"),
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func codexWheelWithLargeEntryForTest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex.whl")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("codex_cli_bin/bin/" + codexExecutableFileName())
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte("#!/bin/sh\n0123456789abcdef\n")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write wheel: %v", err)
	}
	return path
}

func codexTarGzForTest(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	body := fakeCodexBodyForTest(t)
	if err := tw.WriteHeader(&tar.Header{
		Name: "codex-package/bin/" + codexExecutableFileName(),
		Mode: 0o755,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func fakeCodexBodyForTest(t *testing.T) []byte {
	t.Helper()
	if runtime.GOOS != "windows" {
		return []byte("#!/bin/sh\n")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	t.Setenv(codexFakeHelperEnv, "1")
	t.Setenv(codexFakeSupportsAppServerEnv, "1")
	return body
}

func codexReleaseAssetNameForTest(t *testing.T) string {
	t.Helper()
	platform, err := codexWheelReleasePlatform()
	if err != nil {
		t.Skip(err)
	}
	return "openai_codex_cli_bin-0.1.0-py3-none-" + platform + ".whl"
}

func codexTarGzReleaseAssetNameForTest(t *testing.T) string {
	t.Helper()
	target, err := codexRustReleaseTarget()
	if err != nil {
		t.Skip(err)
	}
	return "codex-package-" + target + ".tar.gz"
}
