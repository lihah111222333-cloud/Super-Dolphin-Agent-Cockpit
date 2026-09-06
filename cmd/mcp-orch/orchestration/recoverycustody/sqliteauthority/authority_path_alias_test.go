package sqliteauthority

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAuthorityPathIntermediateSymlinkUsesCanonicalTarget 验证校验与后续
// Bootstrap/OpenAuthority 始终操作同一物理路径。
func TestAuthorityPathIntermediateSymlinkUsesCanonicalTarget(t *testing.T) {
	config := authorityTestConfig(t, "intermediate-alias")
	originalPath := config.Path
	canonicalPath, err := physicalPath(originalPath, true)
	if err != nil {
		t.Fatalf("resolve canonical authority path: %v", err)
	}
	alias := filepath.Join(filepath.Dir(filepath.Dir(originalPath)), "authority-alias")
	if err := os.Symlink(filepath.Dir(originalPath), alias); err != nil {
		t.Fatalf("create authority directory alias: %v", err)
	}
	config.Path = filepath.Join(alias, filepath.Base(originalPath))

	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() through intermediate alias: %v", err)
	}
	if _, err := os.Stat(canonicalPath); err != nil {
		t.Fatalf("canonical authority path was not bootstrapped: %v", err)
	}

	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() through intermediate alias: %v", err)
	}
	t.Cleanup(func() { _ = authority.Close() })
	if authority.config.Path != canonicalPath {
		t.Fatalf("authority config path = %q, want canonical %q", authority.config.Path, canonicalPath)
	}

	aliasInfo, err := os.Stat(config.Path)
	if err != nil {
		t.Fatalf("stat authority through alias: %v", err)
	}
	canonicalInfo, err := os.Stat(canonicalPath)
	if err != nil {
		t.Fatalf("stat canonical authority: %v", err)
	}
	if !os.SameFile(aliasInfo, canonicalInfo) {
		t.Fatal("authority alias and canonical path refer to different files")
	}
}
