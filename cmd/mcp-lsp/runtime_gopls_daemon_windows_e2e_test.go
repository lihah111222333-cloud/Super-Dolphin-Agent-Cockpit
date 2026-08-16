//go:build windows && e2e

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRuntimeWindowsGoplsDaemonRecordRejectsOwnerPIDEqualDaemonPID_E2E 锁定 broker owner 与 daemon 必须是不同进程。
func TestRuntimeWindowsGoplsDaemonRecordRejectsOwnerPIDEqualDaemonPID_E2E(t *testing.T) {
	record := runtimeServerWindowsGoplsDaemonRecord{
		SchemaVersion:         runtimeServerWindowsGoplsDaemonSchema,
		ConfigDigest:          "config-digest",
		Endpoint:              "tcp;127.0.0.1:1",
		OwnerPID:              4242,
		OwnerStartIdentity:    "owner-start",
		OwnerExecutablePath:   filepath.Join(t.TempDir(), "mcp-lsp.exe"),
		OwnerSHA256:           strings.Repeat("a", 64),
		DaemonPID:             4243,
		DaemonStartIdentity:   "daemon-start",
		GoplsExecutablePath:   filepath.Join(t.TempDir(), "gopls.exe"),
		GoplsSHA256:           strings.Repeat("b", 64),
		IdleTimeoutNanos:      1,
		ObservationEndpoint:   "tcp;127.0.0.1:2",
		ObservationCapability: strings.Repeat("c", 64),
		ReclaimCapability:     strings.Repeat("d", 64),
	}
	if err := runtimeServerValidateWindowsGoplsDaemonRecord(record); err != nil {
		t.Fatalf("minimum valid daemon record: %v", err)
	}

	record.DaemonPID = record.OwnerPID
	if err := runtimeServerValidateWindowsGoplsDaemonRecord(record); err == nil {
		t.Fatal("daemon record accepted owner_pid equal to daemon_pid")
	}
}
