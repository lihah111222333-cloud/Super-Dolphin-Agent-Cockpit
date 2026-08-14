//go:build windows

package processobserve_test

import (
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
)

func TestDurableStoreWindowsContractIsAvailable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "durable")
	store, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true})
	if err != nil {
		t.Fatalf("OpenDurableStore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
