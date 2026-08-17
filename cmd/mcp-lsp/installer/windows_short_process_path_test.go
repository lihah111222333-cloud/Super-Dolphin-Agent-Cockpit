//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

func TestWindowsShortProcessPathPreservesDeepFileIdentity(t *testing.T) {
	deepDir := t.TempDir()
	for range 8 {
		deepDir = filepath.Join(deepDir, "windows-node-runtime-long-component")
	}
	if err := os.MkdirAll(deepDir, 0o700); err != nil {
		t.Fatalf("create deep Windows process fixture: %v", err)
	}
	canonical := filepath.Join(deepDir, "npm.cmd")
	if err := os.WriteFile(canonical, []byte("@exit /b 0\r\n"), 0o600); err != nil {
		t.Fatalf("write deep Windows process fixture: %v", err)
	}
	if len(canonical) < 260 {
		t.Fatalf("deep Windows process fixture length=%d, want at least 260", len(canonical))
	}
	shortPath, err := windowsShortProcessPath(canonical)
	if err != nil {
		t.Fatalf("windowsShortProcessPath(): %v", err)
	}
	if !filepath.IsAbs(shortPath) || !strings.EqualFold(filepath.Ext(shortPath), ".cmd") {
		t.Fatalf("short Windows process path=%q, want absolute .cmd path", shortPath)
	}
	if len(shortPath) >= len(canonical) {
		t.Fatalf("short Windows process path length=%d, want below canonical length=%d", len(shortPath), len(canonical))
	}
	canonicalInfo, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	shortInfo, err := os.Stat(shortPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(canonicalInfo, shortInfo) {
		t.Fatal("short Windows process path changed file identity")
	}
}

// TestWindowsShortProcessPathUsesUTF16CodeUnitsForMAXPATH 验证 Windows 长度门槛按
// UTF-16 code unit 计算，而不是按 Go 字符串的 UTF-8 字节数误判非 ASCII 路径。
func TestWindowsShortProcessPathUsesUTF16CodeUnitsForMAXPATH(t *testing.T) {
	path := `C:\LSP-架构-🙂`
	units, err := windows.UTF16FromString(path)
	if err != nil {
		t.Fatalf("UTF16FromString(%q): %v", path, err)
	}
	if got, want := len(units)-1, utf8.RuneCountInString(path)+1; got != want {
		t.Fatalf("UTF-16 code-unit length=%d, want %d (emoji must consume two units)", got, want)
	}
	if len(path) == len(units)-1 {
		t.Fatal("Unicode fixture did not distinguish UTF-8 byte length from UTF-16 code-unit length")
	}
}

// TestWindowsShortProcessPathWithinRootRejectsParentJunction 验证 8.3 转换不会
// 穿过可信根内的 junction，即使最终文件真实存在也必须 fail-fast。
func TestWindowsShortProcessPathWithinRootRejectsParentJunction(t *testing.T) {
	root := t.TempDir()
	externalRoot := t.TempDir()
	target := filepath.Join(externalRoot, "server.exe")
	if err := os.WriteFile(target, []byte("MZ"), 0o600); err != nil {
		t.Fatalf("write external process fixture: %v", err)
	}
	junction := filepath.Join(root, "runtime-junction")
	createWindowsTestJunction(t, junction, externalRoot)
	_, err := WindowsShortProcessPathWithinRoot(root, filepath.Join(junction, "server.exe"))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "reparse") {
		t.Fatalf("WindowsShortProcessPathWithinRoot() error = %v, want reparse rejection", err)
	}
}
