//go:build windows

package processobserve_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"golang.org/x/sys/windows"
)

func assertUnsafeRootRejected(t *testing.T, root string) {
	t.Helper()
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create unsafe Windows durable root: %v", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatalf("build unsafe Windows durable root DACL: %v", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("read unsafe Windows durable root DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		root,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatalf("set unsafe Windows durable root DACL: %v", err)
	}
	if _, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true}); err == nil {
		t.Fatal("OpenDurableStore() accepted Windows root granting Everyone access")
	}
	if err := os.Remove(root); err != nil {
		t.Fatalf("remove unsafe Windows durable root: %v", err)
	}
}

func canonicalTempRoot(t *testing.T) string {
	t.Helper()
	base, err := lspplatform.CanonicalDirectoryPath(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temporary root: %v", err)
	}
	return filepath.Join(base, "durable")
}
