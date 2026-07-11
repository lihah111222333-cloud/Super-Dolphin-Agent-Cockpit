package contract

import (
	"path/filepath"
	"strings"
	"testing"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// TestValidateManifestBinaryRejectsManagedNameWithUnsafeCommand 确认受管 server 名称不能伪装成任意 stdio 进程。
func TestValidateManifestBinaryRejectsManagedNameWithUnsafeCommand(t *testing.T) {
	t.Parallel()

	err := DefaultRuntimeMCPPolicy().ValidateManifestBinary(providerdto.MCPBinary{
		Name:    string(providerdto.FamilyLSP),
		Command: []string{filepath.Join(t.TempDir(), "shell")},
	})
	if err == nil {
		t.Fatal("ValidateManifestBinary() error = nil, want managed stdio command rejection")
	}
	if !strings.Contains(err.Error(), "managed stdio command") {
		t.Fatalf("ValidateManifestBinary() error = %v, want managed stdio command context", err)
	}
}

// TestValidateManifestBinaryAllowsManagedSidecarCommand 锁住受管 peer 允许绝对路径 sidecar basename 的行为。
func TestValidateManifestBinaryAllowsManagedSidecarCommand(t *testing.T) {
	t.Parallel()

	err := DefaultRuntimeMCPPolicy().ValidateManifestBinary(providerdto.MCPBinary{
		Name:    string(providerdto.FamilyOrch),
		Command: []string{filepath.Join(t.TempDir(), "mcp-orch")},
	})
	if err != nil {
		t.Fatalf("ValidateManifestBinary() error = %v", err)
	}
}
