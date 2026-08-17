//go:build !windows

package config

import (
	"path/filepath"
	"testing"
)

func TestNonWindowsNewDefaultsSQLitePathUnderProjectHomeInDev(t *testing.T) {
	assertDefaultSQLitePathInDev(t, func(cfg *Config) string {
		return filepath.Join(cfg.ProjectRoot, ".super-dolphin", "super-dolphin.db")
	})
}

func TestNonWindowsValidateSQLiteParentFailsFastWithoutFallback(t *testing.T) {
	assertValidateSQLiteParentFailsFast(t, "group/world-writable")
}
