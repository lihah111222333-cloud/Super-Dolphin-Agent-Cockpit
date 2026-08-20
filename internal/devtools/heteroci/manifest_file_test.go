package heteroci

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadManifestFileCanonicalAbsoluteRegularFile(t *testing.T) {
	want := testManifest(1)
	path := filepath.Join(t.TempDir(), "manifest.json")
	writeManifestFileForLoad(t, path, mustMarshalManifest(t, want))

	canonical, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", path, err)
	}
	if canonical != path || !filepath.IsAbs(canonical) || filepath.Clean(canonical) != canonical {
		t.Fatalf("test manifest path is not canonical absolute: %q", canonical)
	}

	got, err := LoadManifestFile(canonical)
	if err != nil {
		t.Fatalf("LoadManifestFile(%q): %v", canonical, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadManifestFile(%q) = %#v, want %#v", canonical, got, want)
	}
}

func TestLoadManifestFileRejectsNonCanonicalPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manifest.json")
	writeManifestFileForLoad(t, path, mustMarshalManifest(t, testManifest(1)))
	nonClean := filepath.Dir(path) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(path)

	cases := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "whitespace", path: " \t"},
		{name: "control", path: filepath.Join(root, "manifest\n.json")},
		{name: "relative", path: filepath.Join("relative", "manifest.json")},
		{name: "non-clean", path: nonClean},
		{name: "directory", path: root},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadManifestFile(tc.path); err == nil {
				t.Fatalf("LoadManifestFile(%q) unexpectedly succeeded", tc.path)
			}
		})
	}
}

func TestLoadManifestFileRejectsStrictContent(t *testing.T) {
	valid := mustMarshalManifest(t, testManifest(1))
	duplicate := append([]byte(`{"authority":"LOCAL_NON_AUTHORITATIVE",`), valid[1:]...)
	cases := []struct {
		name string
		raw  []byte
	}{
		{name: "unknown field", raw: addRootField(t, valid, "unknown_field", json.RawMessage(`true`))},
		{name: "null document", raw: []byte("null")},
		{name: "duplicate field", raw: duplicate},
		{name: "trailing value", raw: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "oversize", raw: bytes.Repeat([]byte("x"), maxManifestBytes+1)},
		{name: "old schema", raw: addRootField(t, removeRootField(t, valid, "schema_version"), "schema_version", json.RawMessage(`"heteroci-manifest/v0"`))},
		{name: "authority drift", raw: addRootField(t, removeRootField(t, valid, "authority"), "authority", json.RawMessage(`"REMOTE_AUTHORITATIVE"`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			writeManifestFileForLoad(t, path, tc.raw)
			if _, err := LoadManifestFile(path); err == nil {
				t.Fatalf("LoadManifestFile(%q) unexpectedly accepted %s", path, tc.name)
			}
		})
	}
}

func TestLoadManifestFileDoesNotUseEnvironmentFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	writeManifestFileForLoad(t, path, mustMarshalManifest(t, testManifest(1)))
	for _, name := range []string{
		"HETEROCI_MANIFEST_PATH",
		"SUPER_DOLPHIN_HETEROCI_MANIFEST_PATH",
		"SUPER_DOLPHIN_MANIFEST_PATH",
	} {
		t.Setenv(name, path)
	}
	if _, err := LoadManifestFile(""); err == nil {
		t.Fatal("LoadManifestFile(\"\") unexpectedly used an environment/default path")
	}
}

func TestLoadManifestFileMissingErrorDoesNotLeakSecret(t *testing.T) {
	secret := "manifest-file-secret-7f3f"
	path := filepath.Join(t.TempDir(), secret+".json")
	_, err := LoadManifestFile(path)
	if err == nil {
		t.Fatalf("LoadManifestFile(%q) unexpectedly succeeded", path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadManifestFile(%q) error = %v, want os.ErrNotExist", path, err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("LoadManifestFile(%q) leaked manifest secret in error: %v", path, err)
	}
}

func TestLoadManifestFileMalformedErrorDoesNotLeakSecret(t *testing.T) {
	secret := "manifest-content-secret-9e2a"
	path := filepath.Join(t.TempDir(), "manifest.json")
	raw := []byte(`{"schema_version":"` + ManifestSchemaVersion + `","unknown":"` + secret + `"}`)
	writeManifestFileForLoad(t, path, raw)
	got, err := LoadManifestFile(path)
	if err == nil {
		t.Fatalf("LoadManifestFile(%q) unexpectedly accepted malformed content", path)
	}
	if !reflect.DeepEqual(got, Manifest{}) {
		t.Fatalf("LoadManifestFile(%q) returned %#v on malformed content, want zero Manifest", path, got)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("LoadManifestFile(%q) leaked manifest secret in error: %v", path, err)
	}
}

func TestLoadManifestFileSemanticDecodeErrorsAreRedactedAndReturnZeroManifest(t *testing.T) {
	const (
		schemaSecret    = "manifest-schema-secret-4d7b"
		authoritySecret = "manifest-authority-secret-8a1c"
		endpointURL     = "https://manifest-endpoint-secret.invalid/runner?token=manifest-token-secret"
		tokenSecret     = "manifest-token-secret"
		probeSecret     = "manifest-probe-identity-secret-6f2e"
	)
	probeDigest := "sha256:" + strings.Repeat("a", 64)

	invalidSchema := testManifest(1)
	invalidSchema.SchemaVersion = schemaSecret
	invalidSchema.Nodes[0].Endpoint = endpointURL

	invalidAuthority := testManifest(1)
	invalidAuthority.Authority = authoritySecret
	invalidAuthority.Nodes[0].Endpoint = endpointURL

	invalidEndpoint := testManifest(1)
	invalidEndpoint.Nodes[0].Endpoint = " " + endpointURL

	invalidProbeIdentity := testManifest(1)
	invalidProbeIdentity.Nodes[0].ProbeIdentity = probeSecret
	invalidProbeIdentity.Nodes[0].Endpoint = endpointURL

	duplicateEndpoint := testManifest(2)
	duplicateEndpoint.Nodes[0].Endpoint = endpointURL
	duplicateEndpoint.Nodes[1].Endpoint = endpointURL

	duplicateProbeIdentity := testManifest(2)
	duplicateProbeIdentity.Nodes[0].ProbeIdentity = probeDigest
	duplicateProbeIdentity.Nodes[1].ProbeIdentity = probeDigest
	duplicateProbeIdentity.Nodes[0].Endpoint = endpointURL

	cases := []struct {
		name     string
		manifest Manifest
		secrets  []string
	}{
		{name: "invalid schema_version", manifest: invalidSchema, secrets: []string{schemaSecret, endpointURL, tokenSecret}},
		{name: "invalid authority", manifest: invalidAuthority, secrets: []string{authoritySecret, endpointURL, tokenSecret}},
		{name: "invalid endpoint", manifest: invalidEndpoint, secrets: []string{endpointURL, tokenSecret}},
		{name: "invalid probe_identity", manifest: invalidProbeIdentity, secrets: []string{probeSecret, endpointURL, tokenSecret}},
		{name: "duplicate endpoint", manifest: duplicateEndpoint, secrets: []string{endpointURL, tokenSecret}},
		{name: "duplicate probe_identity", manifest: duplicateProbeIdentity, secrets: []string{probeDigest, endpointURL, tokenSecret}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			writeManifestFileForLoad(t, path, mustMarshalManifest(t, tc.manifest))

			got, err := LoadManifestFile(path)
			if err == nil {
				t.Fatalf("LoadManifestFile(%q) unexpectedly accepted semantic-invalid manifest", path)
			}
			if !reflect.DeepEqual(got, Manifest{}) {
				t.Fatalf("LoadManifestFile(%q) returned %#v on decode error, want zero Manifest", path, got)
			}
			for _, secret := range tc.secrets {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("LoadManifestFile(%q) leaked %q in error: %v", path, secret, err)
				}
			}
		})
	}
}

func TestLoadManifestFilePermissionErrorDoesNotLeakSecret(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission-denied file access is not stable on this platform/user")
	}
	secret := "manifest-permission-secret-c31d"
	path := filepath.Join(t.TempDir(), secret+".json")
	writeManifestFileForLoad(t, path, mustMarshalManifest(t, testManifest(1)))
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod(%q, 0): %v", path, err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Errorf("restore manifest mode: %v", err)
		}
	})
	_, err := LoadManifestFile(path)
	if err == nil {
		t.Fatalf("LoadManifestFile(%q) unexpectedly succeeded", path)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("LoadManifestFile(%q) error = %v, want os.ErrPermission", path, err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("LoadManifestFile(%q) leaked manifest secret in error: %v", path, err)
	}
}

func writeManifestFileForLoad(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write manifest file %q: %v", path, err)
	}
}
