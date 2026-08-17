//go:build windows

package multilsp

import (
	"os/exec"
	"testing"
)

func TestLSPStartupDiagnosticFieldsRedactCommandAndEnvironment(t *testing.T) {
	cmd := exec.Command("clangd.exe", "--compile-commands-dir", `C:\private\workspace`)
	cmd.Env = []string{"SECRET_ENV=do-not-log", `PATH=C:\private\bin`}
	fields := lspStartupDiagnosticFields(transportOptions{Dir: `C:\private\workspace`}, cmd)
	got := make(map[string]any, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if ok {
			got[key] = fields[i+1]
		}
	}
	if got["startup_argument_count"] != 3 {
		t.Fatalf("argument count = %#v, want 3", got["startup_argument_count"])
	}
	if got["startup_env_count"] != 2 {
		t.Fatalf("environment count = %#v, want 2", got["startup_env_count"])
	}
	if got["startup_path_entry_count"] != 1 || got["startup_path_entry_basenames"] != "bin" {
		t.Fatalf("PATH diagnostics = count=%#v basenames=%#v, want one basename bin", got["startup_path_entry_count"], got["startup_path_entry_basenames"])
	}
	if got["startup_path_entry_hashes"] == "" || got["startup_path_utf16_bytes"] == 0 {
		t.Fatalf("PATH diagnostics omitted hash/size: %#v", got)
	}
	for _, secret := range []string{"SECRET_ENV=do-not-log", `C:\private\workspace`, `C:\private\bin`} {
		for _, value := range fields {
			if value == secret {
				t.Fatalf("diagnostic fields leaked %q: %#v", secret, fields)
			}
		}
	}
}
