package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackRemoteOCIContextRejectsTraversalAndLinks(t *testing.T) {
	for _, item := range []struct {
		name string
		head *tar.Header
	}{
		{"traversal", &tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Size: 1}},
		{"symlink", &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := tar.NewWriter(&archive)
			if err := writer.WriteHeader(item.head); err != nil {
				t.Fatal(err)
			}
			if item.head.Size != 0 {
				_, _ = writer.Write([]byte("x"))
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := unpackRemoteOCIContext(t.TempDir()+"/context", archive.Bytes()); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestUnpackRemoteOCIContextWritesOnlyRegularFiles(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "build/gate/Dockerfile", Typeflag: tar.TypeReg, Mode: 0600, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("FROM")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "context")
	if err := unpackRemoteOCIContext(root, archive.Bytes()); err != nil {
		t.Fatalf("unpack safe archive: %v", err)
	}
	info, err := os.Lstat(filepath.Join(root, "build/gate/Dockerfile"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("unpacked file = %#v, %v", info, err)
	}
}
