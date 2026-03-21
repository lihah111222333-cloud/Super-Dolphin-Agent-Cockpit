package wails

import "testing"

func TestMatchesScopedTargetWindowsIgnoresCase(t *testing.T) {
	root := "/repo"
	candidate := "/repo/Src/File.JS"
	target := "src/file.js"
	base := "file.js"

	if !matchesScopedTargetForOS("windows", root, candidate, target, base) {
		t.Fatal("matchesScopedTargetForOS() = false, want true on windows")
	}
	if matchesScopedTargetForOS("linux", root, candidate, target, base) {
		t.Fatal("matchesScopedTargetForOS() = true, want false on case-sensitive platforms")
	}
}
