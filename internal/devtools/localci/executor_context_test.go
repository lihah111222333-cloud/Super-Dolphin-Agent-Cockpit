package localci

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestCanonicalContextIsStableAndInputSensitive(t *testing.T) {
	entries := []sourceexport.TreeEntry{
		contextEntry("build/gate/toolchain.lock", "100644", "go=1\n"),
		contextEntry("build/gate/Dockerfile", "100644", "FROM scratch\n"),
	}
	first, err := buildCanonicalContext(entries)
	if err != nil {
		t.Fatalf("buildCanonicalContext() error = %v", err)
	}
	second, err := buildCanonicalContext([]sourceexport.TreeEntry{entries[1], entries[0]})
	if err != nil {
		t.Fatalf("buildCanonicalContext(reordered) error = %v", err)
	}
	if !bytes.Equal(first.Tar, second.Tar) || first.ContextDigest != second.ContextDigest || first.InputDigest != second.InputDigest {
		t.Fatal("canonical context changed when input order changed")
	}

	entries[0].Data = []byte("go=2\n")
	entries[0].Hash, _ = gitBlobHash(entries[0].Hash, entries[0].Data)
	changed, err := buildCanonicalContext(entries)
	if err != nil {
		t.Fatalf("buildCanonicalContext(changed) error = %v", err)
	}
	if changed.ContextDigest == first.ContextDigest || changed.InputDigest == first.InputDigest {
		t.Fatal("input content change did not change both digests")
	}
}

func TestCanonicalContextNormalizesTarMetadata(t *testing.T) {
	context, err := buildCanonicalContext([]sourceexport.TreeEntry{contextEntry("run.sh", "100755", "#!/bin/sh\n")})
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(context.Tar))
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "run.sh" || header.Mode != 0o755 || !header.ModTime.Equal(time.Unix(0, 0)) || header.Uid != 0 || header.Gid != 0 {
		t.Fatalf("non-canonical tar header = %#v", header)
	}
	if data, err := io.ReadAll(reader); err != nil || string(data) != "#!/bin/sh\n" {
		t.Fatalf("tar content = %q, err = %v", data, err)
	}
}

func TestCanonicalContextRejectsDuplicateAndUnverifiedEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []sourceexport.TreeEntry
	}{
		{name: "duplicate", entries: []sourceexport.TreeEntry{contextEntry("a", "100644", "1"), contextEntry("a", "100644", "2")}},
		{name: "case-fold collision", entries: []sourceexport.TreeEntry{contextEntry("A", "100644", "1"), contextEntry("a", "100644", "2")}},
		{name: "missing object hash", entries: []sourceexport.TreeEntry{{Path: "a", Mode: "100644", Data: []byte("1")}}},
		{name: "unsupported mode", entries: []sourceexport.TreeEntry{contextEntry("a", "120000", "target")}},
		{name: "absolute path", entries: []sourceexport.TreeEntry{contextEntry("/a", "100644", "1")}},
		{name: "parent traversal", entries: []sourceexport.TreeEntry{contextEntry("a/../b", "100644", "1")}},
		{name: "nul path", entries: []sourceexport.TreeEntry{contextEntry("a\x00b", "100644", "1")}},
		{name: "tampered blob", entries: []sourceexport.TreeEntry{{Path: "a", Mode: "100644", Hash: contextEntry("a", "100644", "original").Hash, Data: []byte("tampered")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := buildCanonicalContext(test.entries); err == nil {
				t.Fatal("buildCanonicalContext() accepted invalid input")
			}
		})
	}
}

func contextEntry(name string, mode string, content string) sourceexport.TreeEntry {
	entry := sourceexport.TreeEntry{Path: name, Mode: mode, Data: []byte(content), Hash: "0000000000000000000000000000000000000000"}
	entry.Hash, _ = gitBlobHash(entry.Hash, entry.Data)
	return entry
}
