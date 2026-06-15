package shared

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRelativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trim and clean", input: "  nested/../file.txt  ", want: "file.txt"},
		{name: "empty becomes dot", input: "   ", want: "."},
		{name: "current directory stays dot", input: "./", want: "."},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeRelativePath(tc.input); got != tc.want {
				t.Fatalf("NormalizeRelativePath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestContainsPath(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "tmp", "root")
	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{name: "same root", target: root, want: true},
		{name: "nested path", target: filepath.Join(root, "dir", "file.txt"), want: true},
		{name: "sibling prefix does not match", target: root + "-other", want: false},
		{name: "outside path", target: filepath.Join(string(filepath.Separator), "tmp", "other"), want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ContainsPath(root, tc.target); got != tc.want {
				t.Fatalf("ContainsPath(%q, %q) = %v, want %v", root, tc.target, got, tc.want)
			}
		})
	}
}

func TestAppManagedDataRootsRejectsArbitraryConfiguredHomeSubtree(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	setTestUserHome(t, fakeHome)
	t.Setenv(SuperDolphinHomeEnv, filepath.Join(fakeHome, "Documents", "not-app-data"))

	_, err := AppManagedDataRoots()
	if err == nil {
		t.Fatal("AppManagedDataRoots returned nil error, want arbitrary configured home rejected")
	}
	if !strings.Contains(err.Error(), "not an allowed app-managed data root") {
		t.Fatalf("AppManagedDataRoots error = %q, want allowed-root rejection", err.Error())
	}
}

func TestAppManagedDataRootsAllowsApplicationSupportHome(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	setTestUserHome(t, fakeHome)
	t.Setenv(SuperDolphinHomeEnv, appHome)

	roots, err := AppManagedDataRoots()
	if err != nil {
		t.Fatalf("AppManagedDataRoots returned error: %v", err)
	}
	assertContainsRoot(t, roots, appHome)
}

func TestAppManagedDataRootsIncludesExplicitMultiAgentLogRoot(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	setTestUserHome(t, fakeHome)
	t.Setenv(SuperDolphinHomeEnv, "")

	roots, err := AppManagedDataRoots()
	if err != nil {
		t.Fatalf("AppManagedDataRoots returned error: %v", err)
	}
	assertContainsRoot(t, roots, filepath.Join(fakeHome, ".multi-agent", "log"))
}

func setTestUserHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func assertContainsRoot(t *testing.T, roots []string, want string) {
	t.Helper()
	cleanWant, err := cleanAppManagedDataRoot(want, filepath.Dir(want))
	if err != nil {
		t.Fatalf("normalize wanted root: %v", err)
	}
	for _, root := range roots {
		if root == cleanWant {
			return
		}
	}
	t.Fatalf("roots %#v do not contain %q", roots, cleanWant)
}
