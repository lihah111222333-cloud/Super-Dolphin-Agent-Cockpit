package main

import (
	"archive/tar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeDependencyReplayExtractorRelocatesContainedLinks(t *testing.T) {
	entries := runtimeDependencyArchiveBaseEntries()
	entries = append(entries,
		remoteArchiveDirectory("runtime/rootfs/usr"),
		remoteArchiveDirectory("runtime/rootfs/usr/share"),
		remoteArchiveDirectory("runtime/rootfs/usr/share/ca-certificates"),
		remoteArchiveDirectory("runtime/rootfs/usr/share/ca-certificates/mozilla"),
		remoteArchiveFile("runtime/rootfs/usr/share/ca-certificates/mozilla/ACCVRAIZ1.crt", "cert"),
		remoteArchiveDirectory("runtime/rootfs/etc"),
		remoteArchiveDirectory("runtime/rootfs/etc/ssl"),
		remoteArchiveDirectory("runtime/rootfs/etc/ssl/certs"),
		remoteArchiveSymlink("runtime/rootfs/etc/ssl/certs/ACCVRAIZ1.pem", "/usr/share/ca-certificates/mozilla/ACCVRAIZ1.crt"),
		remoteArchiveDirectory("runtime/lsp"),
		remoteArchiveDirectory("runtime/lsp/node_modules"),
		remoteArchiveDirectory("runtime/lsp/node_modules/.bin"),
		remoteArchiveFile("runtime/lsp/node_modules/.bin/gopls", "tool"),
		remoteArchiveSymlink("runtime/bin/gopls", "../lsp/node_modules/.bin/gopls"),
		remoteArchiveFixtureEntry{header: tar.Header{Name: "runtime/bin/cert-copy", Typeflag: tar.TypeLink, Linkname: "runtime/rootfs/usr/share/ca-certificates/mozilla/ACCVRAIZ1.crt"}},
	)
	stage := runRuntimeDependencyExtractor(t, entries, true)
	absoluteLink := filepath.Join(stage, "runtime/rootfs/etc/ssl/certs/ACCVRAIZ1.pem")
	target, err := os.Readlink(absoluteLink)
	if err != nil || filepath.IsAbs(target) {
		t.Fatalf("relocated rootfs link = %q, %v", target, err)
	}
	for _, name := range []string{"runtime/rootfs/etc/ssl/certs/ACCVRAIZ1.pem", "runtime/bin/gopls", "runtime/bin/cert-copy"} {
		data, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(name)))
		if err != nil || (string(data) != "cert" && string(data) != "tool") {
			t.Fatalf("read extracted %q = %q, %v", name, data, err)
		}
	}
}

func TestRuntimeDependencyReplayExtractorRejectsUnsafeArchives(t *testing.T) {
	tests := []struct {
		name    string
		entries []remoteArchiveFixtureEntry
		want    string
	}{
		{name: "relative escape", entries: []remoteArchiveFixtureEntry{remoteArchiveSymlink("runtime/bin/escape", "../../outside")}, want: "escaping symbolic link"},
		{name: "absolute outside rootfs", entries: []remoteArchiveFixtureEntry{remoteArchiveSymlink("runtime/bin/escape", "/usr/bin/tool")}, want: "escaping symbolic link"},
		{name: "hard link before target", entries: []remoteArchiveFixtureEntry{{header: tar.Header{Name: "runtime/bin/link", Typeflag: tar.TypeLink, Linkname: "runtime/bin/target"}}}, want: "invalid hard link"},
		{name: "entry below symlink", entries: []remoteArchiveFixtureEntry{remoteArchiveSymlink("runtime/frontend/link", "node_modules"), remoteArchiveFile("runtime/frontend/link/child", "bad")}, want: "entries below a symbolic link"},
		{name: "special type", entries: []remoteArchiveFixtureEntry{{header: tar.Header{Name: "runtime/bin/fifo", Typeflag: tar.TypeFifo}}}, want: "unsupported entry type"},
		{name: "duplicate", entries: []remoteArchiveFixtureEntry{remoteArchiveFile("runtime/bin/tool", "one"), remoteArchiveFile("runtime/bin/tool", "two")}, want: "duplicate path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries := append(runtimeDependencyArchiveBaseEntries(), test.entries...)
			output := runRuntimeDependencyExtractorFailure(t, entries)
			if !strings.Contains(output, test.want) {
				t.Fatalf("extractor error = %q, want %q", output, test.want)
			}
		})
	}
}

func runtimeDependencyArchiveBaseEntries() []remoteArchiveFixtureEntry {
	entries := []remoteArchiveFixtureEntry{remoteArchiveDirectory("runtime")}
	for _, name := range []string{
		"runtime/bin", "runtime/go", "runtime/python", "runtime/node", "runtime/rootfs",
		"runtime/go-mod-cache", "runtime/go-proxy", "runtime/frontend",
		"runtime/frontend/node_modules", "runtime/frontend/npm-cache",
	} {
		entries = append(entries, remoteArchiveDirectory(name))
	}
	manifest := `{"schema_version":11,"go_sum_sha256":"x","module_proxy_lock_sha256":"x","module_proxy_tree_sha256":"x","go_mod_cache_tree_sha256":"x","package_lock_sha256":"x","node_modules_tree_sha256":"x","npm_cache_tree_sha256":"x","ripgrep_sha256":"x","sqruff_sha256":"x"}`
	return append(entries, remoteArchiveFile("runtime/manifest.json", manifest))
}

func remoteArchiveDirectory(name string) remoteArchiveFixtureEntry {
	return remoteArchiveFixtureEntry{header: tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}}
}

func remoteArchiveFile(name string, data string) remoteArchiveFixtureEntry {
	return remoteArchiveFixtureEntry{header: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(data))}, data: data}
}

func remoteArchiveSymlink(name string, target string) remoteArchiveFixtureEntry {
	return remoteArchiveFixtureEntry{header: tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target}}
}

func runRuntimeDependencyExtractor(t *testing.T, entries []remoteArchiveFixtureEntry, wantSuccess bool) string {
	t.Helper()
	root := t.TempDir()
	archive := filepath.Join(root, "runtime-deps.tar.gz")
	stage := filepath.Join(root, "stage")
	writeRemoteArchiveFixture(t, archive, entries)
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "-c", runtimeDependencyReplayExtractor(t), archive, stage)
	output, err := command.CombinedOutput()
	if wantSuccess && err != nil {
		t.Fatalf("runtime dependency extractor: %v: %s", err, output)
	}
	if !wantSuccess && err == nil {
		t.Fatal("runtime dependency extractor accepted an unsafe archive")
	}
	if !wantSuccess {
		return string(output)
	}
	return stage
}

func runRuntimeDependencyExtractorFailure(t *testing.T, entries []remoteArchiveFixtureEntry) string {
	t.Helper()
	return runRuntimeDependencyExtractor(t, entries, false)
}

func runtimeDependencyReplayExtractor(t *testing.T) string {
	t.Helper()
	startMarker := `<<'PY'` + "\n"
	start := strings.Index(remoteBaselineSeedScriptRuntimeDepsReplay, startMarker)
	if start < 0 {
		t.Fatal("runtime dependency extractor start marker is missing")
	}
	start += len(startMarker)
	end := strings.Index(remoteBaselineSeedScriptRuntimeDepsReplay[start:], "\nPY\n")
	if end < 0 {
		t.Fatal("runtime dependency extractor end marker is missing")
	}
	return remoteBaselineSeedScriptRuntimeDepsReplay[start : start+end]
}
