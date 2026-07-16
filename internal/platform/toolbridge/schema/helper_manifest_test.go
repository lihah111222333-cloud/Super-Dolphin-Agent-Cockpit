package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeHelperPackageFixture(t *testing.T, dir, content string, identity HelperIdentity) (string, string) {
	t.Helper()
	helper := filepath.Join(dir, HelperFileName(identity.GOOS))
	image := []byte(content)
	if identity.GOOS == "windows" {
		image = append([]byte{'M', 'Z'}, image...)
	}
	if err := os.WriteFile(helper, image, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := helper + HelperManifestSuffix
	if err := WriteHelperManifest(helper, manifest, identity); err != nil {
		t.Fatal(err)
	}
	return helper, manifest
}

func TestHelperPackageRejectsMissingMixedAndTamperedArtifacts(t *testing.T) {
	identity := HelperIdentity{AppCommit: "commit-a", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{"missing manifest", func(t *testing.T, _, manifest string) { t.Helper(); _ = os.Remove(manifest) }},
		{"symlink manifest", func(t *testing.T, _, manifest string) {
			t.Helper()
			realManifest := manifest + ".real"
			if err := os.Rename(manifest, realManifest); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(realManifest, manifest); err != nil {
				t.Fatal(err)
			}
		}},
		{"tampered helper", func(t *testing.T, helper, _ string) { t.Helper(); _ = os.WriteFile(helper, []byte("tampered"), 0o700) }},
		{"mixed identity", func(t *testing.T, helper, manifest string) {
			t.Helper()
			other := identity
			other.AppCommit = "commit-b"
			if err := WriteHelperManifest(helper, manifest, other); err != nil {
				t.Fatal(err)
			}
		}},
		{"protocol mismatch", func(t *testing.T, _, manifest string) {
			mutateHelperManifestField(t, manifest, "protocol", "wrong")
		}},
		{"Go version mismatch", func(t *testing.T, _, manifest string) { mutateHelperManifestField(t, manifest, "go_version", "go0.0") }},
		{"GOOS mismatch", func(t *testing.T, _, manifest string) { mutateHelperManifestField(t, manifest, "goos", "wrong") }},
		{"GOARCH mismatch", func(t *testing.T, _, manifest string) { mutateHelperManifestField(t, manifest, "goarch", "wrong") }},
		{"executable policy mismatch", func(t *testing.T, _, manifest string) {
			mutateHelperManifestField(t, manifest, "executable_policy", "wrong")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			helper, manifest := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", identity)
			test.mutate(t, helper, manifest)
			if err := VerifyHelperPackage(helper, manifest, identity); err == nil {
				t.Fatal("VerifyHelperPackage() error = nil")
			}
		})
	}
}

func mutateHelperManifestField(t *testing.T, manifest, field string, value any) {
	t.Helper()
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document[field] = value
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHelperPackageRejectsSymlinkAndNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable policy test")
	}
	identity := HelperIdentity{AppCommit: "commit-a", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	dir := t.TempDir()
	helper, manifest := writeHelperPackageFixture(t, dir, "#!/bin/sh\nexit 0\n", identity)
	if err := os.Chmod(helper, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyHelperPackage(helper, manifest, identity); err == nil || !strings.Contains(err.Error(), "execute") {
		t.Fatalf("non-executable verification error = %v", err)
	}
	if err := os.Chmod(helper, 0o700); err != nil {
		t.Fatal(err)
	}
	realHelper := filepath.Join(dir, "real-helper")
	if err := os.Rename(helper, realHelper); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHelper, helper); err != nil {
		t.Fatal(err)
	}
	if err := VerifyHelperPackage(helper, manifest, identity); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink verification error = %v", err)
	}
}

func TestWindowsProcessGuardSuspendsAssignsThenResumes(t *testing.T) {
	source, err := os.ReadFile("process_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	suspend := strings.Index(text, "windows.CREATE_SUSPENDED")
	assign := strings.Index(text, "windows.AssignProcessToJobObject")
	resume := strings.Index(text, "NtResumeProcess")
	if suspend < 0 || assign < 0 || resume < 0 || assign >= resume {
		t.Fatalf("Windows process guard order missing: suspend=%d assign=%d resume=%d", suspend, assign, resume)
	}
	closeOnFailure := strings.Index(text[resume:], "windows.CloseHandle(handle)")
	if closeOnFailure < 0 {
		t.Fatalf("Windows process guard order missing: suspend=%d assign=%d resume=%d close=%d", suspend, assign, resume, closeOnFailure)
	}
}
