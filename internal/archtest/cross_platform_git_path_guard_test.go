package archtest

import (
	"bytes"
	"os/exec"
	"path"
	"strings"
	"testing"
	"unicode"
)

// TestTrackedPathsArePortableToWindows 锁定 exact tree 可在 Windows 节点物化。
func TestTrackedPathsArePortableToWindows(t *testing.T) {
	root := findRepoRoot(t)
	command := exec.Command("git", "-C", root, "ls-files", "-z")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list tracked paths: %v", err)
	}
	for _, raw := range bytes.Split(output, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		tracked := string(raw)
		for _, component := range strings.Split(tracked, "/") {
			if invalidWindowsPathComponent(component) {
				t.Errorf("tracked path %q contains Windows-incompatible component %q", tracked, component)
			}
		}
	}
}

// invalidWindowsPathComponent 判断路径分量能否在 Windows exact tree 中物化。
func invalidWindowsPathComponent(component string) bool {
	return invalidWindowsPathSyntax(component) || reservedWindowsPathName(component)
}

// invalidWindowsPathSyntax 判断路径分量是否违反 Windows 文件系统语法。
func invalidWindowsPathSyntax(component string) bool {
	if component == "" {
		return true
	}
	if component == "." || component == ".." {
		return true
	}
	if strings.TrimRight(component, " .") != component {
		return true
	}
	if strings.ContainsAny(component, `<>:"\|?*`) || strings.IndexFunc(component, unicode.IsControl) >= 0 {
		return true
	}
	return false
}

// reservedWindowsPathName 判断路径分量是否命中 Windows 保留设备名。
func reservedWindowsPathName(component string) bool {
	base := strings.ToUpper(strings.TrimSuffix(component, path.Ext(component)))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) != 4 {
		return false
	}
	prefix := base[:3]
	if prefix != "COM" && prefix != "LPT" {
		return false
	}
	return base[3] >= '1' && base[3] <= '9'
}
