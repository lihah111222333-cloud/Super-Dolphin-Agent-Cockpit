package gateclosure

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractArchiveAcceptsRegularFile(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	content := []byte("regular file\n")
	if err := writer.WriteHeader(&tar.Header{
		Name: "nested/input.txt", Mode: 0o640, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	destination := t.TempDir()
	if err := extractArchive(tar.NewReader(bytes.NewReader(archive.Bytes())), destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "nested", "input.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("extracted content = %q, want %q", got, content)
	}
}
