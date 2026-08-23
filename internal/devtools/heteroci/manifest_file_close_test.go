package heteroci

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type manifestFileCloseCase struct {
	name          string
	operationErr  error
	closeErr      error
	want          Manifest
	wantErr       bool
	wantOperation ManifestFileOperation
	wantIs        []error
}

func TestFinishManifestFileLoad(t *testing.T) {
	manifest, secrets := validManifestFileCloseFixture(t)
	closeSentinel := errors.New(strings.Join(secrets, " | "))
	operationSentinel := errors.New("manifest operation sentinel")
	operationErr := safeManifestFileOperationError(ManifestFileOperationDecode, operationSentinel)
	cases := []manifestFileCloseCase{
		{
			name:          "close failure zeros successful operation",
			closeErr:      closeSentinel,
			want:          Manifest{},
			wantErr:       true,
			wantOperation: ManifestFileOperationClose,
			wantIs:        []error{closeSentinel},
		},
		{
			name:         "operation and close failures are both retained",
			operationErr: operationErr,
			closeErr:     closeSentinel,
			want:         Manifest{},
			wantErr:      true,
			wantIs:       []error{operationSentinel, closeSentinel},
		},
		{
			name: "success returns original manifest",
			want: manifest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			closeCalls := 0
			got, err := finishManifestFileLoad(manifest, tc.operationErr, func() error {
				closeCalls++
				return tc.closeErr
			})
			assertManifestFileCloseResult(t, got, tc.want, closeCalls)
			if tc.wantErr {
				assertManifestFileCloseError(t, err, tc.wantOperation, tc.wantIs)
				assertManifestFileCloseRedaction(t, err, secrets)
			} else if err != nil {
				t.Fatalf("finishManifestFileLoad() error = %v, want nil", err)
			}
		})
	}
}

func validManifestFileCloseFixture(t *testing.T) (Manifest, []string) {
	t.Helper()
	secrets := []string{
		"/manifest-path-secret/inventory.json",
		"manifest-payload-secret",
		"manifest-endpoint-secret.invalid",
		"sha256:" + strings.Repeat("b", 64),
	}
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Authority:     AuthorityLocalNonAuthoritative,
		Nodes: []NodeConfig{
			{NodeID: "node-close-test", Endpoint: "https://" + secrets[2] + "/probe", ProbeIdentity: secrets[3], Platform: PlatformLinuxAMD64, Capabilities: []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "registry.invalid/" + secrets[1], ImageDigest: "sha256:" + strings.Repeat("d", 64)}}},
			{NodeID: "node-close-test-2", Endpoint: "https://node-close-test-2.invalid/probe", ProbeIdentity: "sha256:" + strings.Repeat("e", 64), Platform: PlatformLinuxAMD64, Capabilities: []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("f", 64)}}},
			{NodeID: "node-close-test-3", Endpoint: "https://node-close-test-3.invalid/probe", ProbeIdentity: "sha256:" + strings.Repeat("1", 64), Platform: PlatformLinuxAMD64, Capabilities: []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("3", 64)}}},
			{NodeID: "node-close-test-4", Endpoint: "https://node-close-test-4.invalid/probe", ProbeIdentity: "sha256:" + strings.Repeat("2", 64), Platform: PlatformLinuxAMD64, Capabilities: []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("4", 64)}}},
		},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("fixture manifest is invalid: %v", err)
	}
	return manifest, secrets
}

func assertManifestFileCloseResult(t *testing.T, got, want Manifest, closeCalls int) {
	t.Helper()
	if closeCalls != 1 {
		t.Fatalf("finishManifestFileLoad() close calls = %d, want 1", closeCalls)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("finishManifestFileLoad() manifest = %#v, want %#v", got, want)
	}
}

func assertManifestFileCloseError(
	t *testing.T,
	err error,
	wantOperation ManifestFileOperation,
	wantIs []error,
) {
	t.Helper()
	if err == nil {
		t.Fatal("finishManifestFileLoad() error = nil, want failure")
	}
	if wantOperation != "" {
		var fileErr *ManifestFileError
		if !errors.As(err, &fileErr) {
			t.Fatalf("finishManifestFileLoad() error type = %T, want *ManifestFileError", err)
		}
		if fileErr.Operation() != wantOperation {
			t.Fatalf("finishManifestFileLoad() operation = %q, want %q", fileErr.Operation(), wantOperation)
		}
	}
	for _, target := range wantIs {
		if !errors.Is(err, target) {
			t.Fatalf("finishManifestFileLoad() error = %v, want errors.Is(%v)", err, target)
		}
	}
}

func assertManifestFileCloseRedaction(t *testing.T, err error, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("finishManifestFileLoad() error leaked %q: %v", secret, err)
		}
	}
}
