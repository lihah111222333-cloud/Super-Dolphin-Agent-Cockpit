//go:build windows

package config

import (
	"path/filepath"
	"testing"
)

// TestWindowsCurrentPackagedSQLiteHomeSelection 锁定 Windows 构建使用 APPDATA 策略。
func TestWindowsCurrentPackagedSQLiteHomeSelection(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	got, err := resolveCurrentPackagedSQLiteHome()
	if err != nil {
		t.Fatalf("resolve Windows packaged SQLite home: %v", err)
	}
	want := filepath.Join(appData, "Super Dolphin")
	if got != want {
		t.Fatalf("Windows packaged SQLite home=%q, want %q", got, want)
	}
}
