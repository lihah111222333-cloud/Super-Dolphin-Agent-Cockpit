//go:build windows

package processobserve_test

import (
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
)

func TestDurableStoreWindowsContractIsExplicitlyNotVerified(t *testing.T) {
	_, err := processobserve.OpenDurableStore(`C:\\private\\mcp-lsp-observations`, processobserve.DurableOptions{})
	if !errors.Is(err, processobserve.ErrDurablePlatformNotVerified) {
		t.Fatalf("OpenDurableStore() error = %v, want explicit Windows N/V", err)
	}
}
