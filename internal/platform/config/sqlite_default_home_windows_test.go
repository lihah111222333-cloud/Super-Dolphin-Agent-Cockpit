//go:build windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsNewDefaultsSQLitePathUnderUserStateHomeInDev(t *testing.T) {
	assertDefaultSQLitePathInDev(t, func(*Config) string {
		userHome, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("resolve user home: %v", err)
		}
		return filepath.Join(userHome, ".super-dolphin", "state", "super-dolphin.db")
	})
}

func TestWindowsValidateSQLiteParentFailsFastWithoutFallback(t *testing.T) {
	assertValidateSQLiteParentFailsFast(t, "DACL grants write access to broad or non-owner principal")
}
