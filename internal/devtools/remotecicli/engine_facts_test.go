package remotecicli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashStableEngineBinaryBindsRegularHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.bin")
	if err := os.WriteFile(path, []byte("portable-engine"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := hashStableEngineBinary(path)
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("hashStableEngineBinary = %q, err=%v", digest, err)
	}
}

func TestHashStableEngineBinaryRejectsSymlinkAndOversize(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.bin")
	link := filepath.Join(root, "engine.bin")
	if err := os.WriteFile(target, []byte("engine"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, err := hashStableEngineBinary(link); err == nil {
		t.Fatal("hashStableEngineBinary accepted a symlink")
	}
	if _, _, err := openEngineBinary(target, 1); err == nil {
		t.Fatal("openEngineBinary accepted an oversized handle")
	}
}

func TestValidateEngineBinaryReadRejectsMutationSignals(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.bin")
	secondPath := filepath.Join(root, "second.bin")
	if err := os.WriteFile(firstPath, []byte("first"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second"), 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                       string
		count                      int64
		size                       int64
		copyErr, statErr, closeErr error
		opened, final              os.FileInfo
	}{
		{name: "copy", count: 5, size: 5, copyErr: errors.New("copy"), opened: first, final: first},
		{name: "stat", count: 5, size: 5, statErr: errors.New("stat"), opened: first, final: first},
		{name: "close", count: 5, size: 5, closeErr: errors.New("close"), opened: first, final: first},
		{name: "count", count: 4, size: 5, opened: first, final: first},
		{name: "identity", count: 5, size: 5, opened: first, final: second},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateEngineBinaryRead(testCase.count, testCase.size, testCase.copyErr, testCase.statErr, testCase.closeErr, testCase.opened, testCase.final); err == nil {
				t.Fatal("validateEngineBinaryRead accepted mutation signal")
			}
		})
	}
}
