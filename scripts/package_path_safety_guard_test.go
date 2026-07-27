package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLinuxAndWindowsPackageIdentityRejectUnsafePathComponents(t *testing.T) {
	root := scriptsRepoRoot(t)
	linux := readPackageSafetyScript(t, filepath.Join(root, "scripts", "package_linux.sh"))
	windows := readPackageSafetyScript(t, filepath.Join(root, "scripts", "package_windows.ps1"))

	namePattern := extractPackageSafetyPattern(t, linux, `name_pattern='([^']+)'`)
	versionPattern := extractPackageSafetyPattern(t, linux, `version_pattern='([^']+)'`)
	assertPackageSafetyPatternParity(t, windows, namePattern, versionPattern)
	nameRE := regexp.MustCompile(namePattern)
	versionRE := regexp.MustCompile(versionPattern)

	assertPackagePatternMatches(t, nameRE, "APP_NAME", true, []string{"Super Dolphin", "super-dolphin_2", "A.b"})
	assertPackagePatternMatches(t, nameRE, "APP_NAME", false, []string{"../escape", "/absolute", `C:\escape`, ".", "..", "bad\nname", "bad/name"})
	assertPackagePatternMatches(t, versionRE, "VERSION", true, []string{"0.1.0", "v1.2.3", "1.2.3-rc.1+build.7"})
	assertPackagePatternMatches(t, versionRE, "VERSION", false, []string{"../1.2.3", "/1.2.3", `C:\1.2.3`, "1.2", "1.2.3\nescape"})
}

func TestPackageDeletionTargetsRequireCanonicalDistContainment(t *testing.T) {
	root := scriptsRepoRoot(t)
	linux := readPackageSafetyScript(t, filepath.Join(root, "scripts", "package_linux.sh"))
	windows := readPackageSafetyScript(t, filepath.Join(root, "scripts", "package_windows.ps1"))

	assertPackageScriptContainsAll(t, "Linux package script", linux, []string{
		`require_linux_package_path_within "$dist" "$stage"`,
		`require_linux_package_path_within "$dist" "$stage.tar.gz"`,
		`canonical_candidate="$(realpath -m -- "$candidate")"`,
	})
	windowsContainmentAssignments := make([]string, 0, 4)
	for _, variable := range []string{"$stage", "$zipPath", "$setupPath", "$issPath"} {
		windowsContainmentAssignments = append(windowsContainmentAssignments,
			variable+" = Resolve-ContainedWindowsPackagePath -DistRoot $dist -Candidate "+variable)
	}
	assertPackageScriptContainsAll(t, "Windows package script", windows, windowsContainmentAssignments)
	assertPackageValidationPrecedesDeletion(t, "Linux", linux,
		"\nvalidate_linux_package_identity\n", `rm -rf "$stage"`)
	assertPackageValidationPrecedesDeletion(t, "Windows", windows,
		"\nAssert-SafeWindowsPackageIdentity -Name $AppName -ReleaseVersion $Version\n",
		"Remove-Item -LiteralPath $stage -Recurse -Force")
	assertPackageScriptContainsAll(t, "Windows package identity guard", windows, []string{
		"Windows path components must not end with a space or dot",
		"^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$",
	})
}

func assertPackageSafetyPatternParity(t *testing.T, windows, namePattern, versionPattern string) {
	t.Helper()
	if !strings.Contains(windows, "$namePattern = '"+namePattern+"'") ||
		!strings.Contains(windows, "$versionPattern = '"+versionPattern+"'") {
		t.Fatal("Linux and Windows package identity patterns drifted")
	}
}

func assertPackagePatternMatches(t *testing.T, pattern *regexp.Regexp, label string, want bool, values []string) {
	t.Helper()
	for _, value := range values {
		if pattern.MatchString(value) != want {
			t.Errorf("%s safety match for %q = %t, want %t", label, value, !want, want)
		}
	}
}

func assertPackageScriptContainsAll(t *testing.T, label, script string, tokens []string) {
	t.Helper()
	for _, token := range tokens {
		if !strings.Contains(script, token) {
			t.Errorf("%s missing %q", label, token)
		}
	}
}

func assertPackageValidationPrecedesDeletion(t *testing.T, platform, script, validation, deletion string) {
	t.Helper()
	validationIndex := strings.Index(script, validation)
	deletionIndex := strings.Index(script, deletion)
	if validationIndex < 0 || deletionIndex < 0 {
		t.Fatalf("%s package script must invoke identity validation and retain the guarded stage deletion", platform)
	}
	if validationIndex > deletionIndex {
		t.Fatalf("%s package identity validation must run before recursive stage deletion", platform)
	}
}

func extractPackageSafetyPattern(t *testing.T, script, expression string) string {
	t.Helper()
	match := regexp.MustCompile(expression).FindStringSubmatch(script)
	if len(match) != 2 {
		t.Fatalf("package script is missing pattern %q", expression)
	}
	return match[1]
}

func readPackageSafetyScript(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func scriptsRepoRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, ".."))
}
