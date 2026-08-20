package heteroci_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

type manifestFileExternalCase struct {
	name     string
	path     string
	contents []byte
	wantOp   heteroci.ManifestFileOperation
	wantIs   error
	secrets  []string
}

func TestLoadManifestFileExternalErrorOperations(t *testing.T) {
	for _, tc := range externalManifestFileCases(t) {
		t.Run(tc.name, func(t *testing.T) { runExternalManifestFileCase(t, tc) })
	}
}

func externalManifestFileCases(t *testing.T) []manifestFileExternalCase {
	t.Helper()
	const (
		pathSecret     = "manifest-path-secret-7b4d"
		payloadSecret  = "manifest-payload-secret-2e91"
		endpointSecret = "https://endpoint-secret.invalid/probe?token=endpoint-token-secret"
	)
	probeSecret := "sha256:" + strings.Repeat("b", 64)
	valid := externalManifest()
	duplicateEndpoint := externalManifest()
	duplicateEndpoint.Nodes = append(duplicateEndpoint.Nodes, valid.Nodes[0])
	duplicateEndpoint.Nodes[1].NodeID = "node-b"
	duplicateEndpoint.Nodes[1].ProbeIdentity = "sha256:" + strings.Repeat("c", 64)
	duplicateEndpoint.Nodes[0].Endpoint = endpointSecret
	duplicateEndpoint.Nodes[1].Endpoint = endpointSecret
	duplicateProbe := externalManifest()
	duplicateProbe.Nodes = append(duplicateProbe.Nodes, valid.Nodes[0])
	duplicateProbe.Nodes[1].NodeID = "node-b"
	duplicateProbe.Nodes[1].Endpoint = "https://node-b.invalid/probe"
	duplicateProbe.Nodes[0].ProbeIdentity = probeSecret
	duplicateProbe.Nodes[1].ProbeIdentity = probeSecret

	unknownField := map[string]json.RawMessage{}
	validJSON, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("json.Marshal(valid manifest): %v", err)
	}
	if err := json.Unmarshal(validJSON, &unknownField); err != nil {
		t.Fatalf("json.Unmarshal(valid manifest): %v", err)
	}
	unknownField["payload_secret"] = json.RawMessage(`"` + payloadSecret + `"`)
	unknownJSON, err := json.Marshal(unknownField)
	if err != nil {
		t.Fatalf("json.Marshal(unknown manifest): %v", err)
	}

	return []manifestFileExternalCase{
		{
			name:    "path",
			path:    pathSecret + "/../manifest.json",
			wantOp:  heteroci.ManifestFileOperationPath,
			secrets: []string{pathSecret},
		},
		{
			name:    "open missing",
			path:    filepath.Join(t.TempDir(), pathSecret+".json"),
			wantOp:  heteroci.ManifestFileOperationOpen,
			wantIs:  os.ErrNotExist,
			secrets: []string{pathSecret},
		},
		{
			name:    "stat non-regular",
			path:    filepath.Join(t.TempDir(), pathSecret+"-directory"),
			wantOp:  heteroci.ManifestFileOperationStat,
			secrets: []string{pathSecret},
		},
		{
			name:     "decode payload",
			path:     filepath.Join(t.TempDir(), "manifest.json"),
			contents: unknownJSON,
			wantOp:   heteroci.ManifestFileOperationDecode,
			secrets:  []string{payloadSecret},
		},
		{
			name:     "decode endpoint",
			path:     filepath.Join(t.TempDir(), "manifest-endpoint.json"),
			contents: mustExternalManifestJSON(t, duplicateEndpoint),
			wantOp:   heteroci.ManifestFileOperationDecode,
			secrets:  []string{endpointSecret, "endpoint-token-secret"},
		},
		{
			name:     "decode probe",
			path:     filepath.Join(t.TempDir(), "manifest-probe.json"),
			contents: mustExternalManifestJSON(t, duplicateProbe),
			wantOp:   heteroci.ManifestFileOperationDecode,
			secrets:  []string{probeSecret},
		},
	}
}

func runExternalManifestFileCase(t *testing.T, tc manifestFileExternalCase) {
	t.Helper()
	prepareExternalManifestFileCase(t, tc)
	got, err := heteroci.LoadManifestFile(tc.path)
	assertExternalManifestFileCase(t, tc, got, err)
}

func prepareExternalManifestFileCase(t *testing.T, tc manifestFileExternalCase) {
	t.Helper()
	if tc.name == "stat non-regular" {
		if err := os.Mkdir(tc.path, 0o700); err != nil {
			t.Fatalf("os.Mkdir(%q): %v", tc.path, err)
		}
		return
	}
	if tc.contents == nil {
		return
	}
	if err := os.WriteFile(tc.path, tc.contents, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q): %v", tc.path, err)
	}
}

func assertExternalManifestFileCase(t *testing.T, tc manifestFileExternalCase, got heteroci.Manifest, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("LoadManifestFile(%q) unexpectedly succeeded", tc.path)
	}
	var fileErr *heteroci.ManifestFileError
	if !errors.As(err, &fileErr) {
		t.Fatalf("LoadManifestFile(%q) error type = %T, want *heteroci.ManifestFileError", tc.path, err)
	}
	if fileErr.Operation() != tc.wantOp {
		t.Fatalf("LoadManifestFile(%q) operation = %q, want %q", tc.path, fileErr.Operation(), tc.wantOp)
	}
	if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
		t.Fatalf("LoadManifestFile(%q) error = %v, want errors.Is(%v)", tc.path, err, tc.wantIs)
	}
	if !reflect.DeepEqual(got, heteroci.Manifest{}) {
		t.Fatalf("LoadManifestFile(%q) returned %#v, want zero Manifest", tc.path, got)
	}
	for _, secret := range append(tc.secrets, "manifest-path-secret-7b4d") {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("LoadManifestFile(%q) leaked %q in error", tc.path, secret)
		}
	}
}

func externalManifest() heteroci.Manifest {
	return heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Nodes: []heteroci.NodeConfig{{
			NodeID:        "node-a",
			Endpoint:      "https://node-a.invalid/probe",
			ProbeIdentity: "sha256:" + strings.Repeat("a", 64),
			Platform:      heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{{
				Runtime:     heteroci.RuntimeUbuntuContainer,
				Image:       "registry.example/ubuntu",
				ImageDigest: "sha256:" + strings.Repeat("d", 64),
			}},
		}},
	}
}

func mustExternalManifestJSON(t *testing.T, manifest heteroci.Manifest) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal(manifest): %v", err)
	}
	return raw
}
