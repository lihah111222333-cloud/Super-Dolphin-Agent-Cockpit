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

// TestValidateRuntimeStdioCommandRejectsRemovedPostgresCommand prevents the retired built-in database peer from returning.
func TestValidateRuntimeStdioCommandRejectsRemovedPostgresCommand(t *testing.T) {
	t.Parallel()

	err := DefaultRuntimeMCPPolicy().ValidateRuntimeStdioCommand(
		"mcp-server-postgres",
		[]string{"postgresql://localhost/removed"},
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported stdio command") {
		t.Fatalf("ValidateRuntimeStdioCommand() error = %v, want removed postgres command rejection", err)
	}
}

func TestValidateRuntimeStdioCommandAllowsPinnedSQLiteDefault(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "super-dolphin.db")
	err := DefaultRuntimeMCPPolicy().ValidateRuntimeStdioCommand("npx", []string{"-y", "@bytebase/dbhub@0.23.0", "--dsn=" + runtimeSQLiteDBHubDSN(dbPath)}, dbPath)
	if err != nil {
		t.Fatalf("ValidateRuntimeStdioCommand() error = %v", err)
	}
}

func TestValidateRuntimeStdioCommandRejectsUnpinnedOrDifferentSQLitePackages(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "super-dolphin.db")
	for _, pkg := range []string{"@bytebase/dbhub", "@bytebase/dbhub@latest", "@bytebase/dbhub@0.22.0"} {
		t.Run(pkg, func(t *testing.T) {
			err := DefaultRuntimeMCPPolicy().ValidateRuntimeStdioCommand("npx", []string{"-y", pkg, "--dsn=" + runtimeSQLiteDBHubDSN(dbPath)}, dbPath)
			if err == nil {
				t.Fatalf("ValidateRuntimeStdioCommand() error = nil, want %q rejection", pkg)
			}
		})
	}
}
