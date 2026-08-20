package remotecicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestArtifactBuilderObservedIdentityMeasuresInjectedExecutableAndCompiler(t *testing.T) {
	t.Parallel()

	executable := []byte("opened executable bytes\x00")
	closureDigest := "sha256:" + strings.Repeat("b", 64)
	identity, err := measureArtifactBuilderObservedIdentity(context.Background(), artifactBuilderObservedIdentityDependencies{
		OpenExecutable: func() (io.ReadSeeker, error) { return bytes.NewReader(executable), nil },
		CompilerPath:   func() (string, error) { return "/locked/go", nil },
		CompilerClosureDigest: func(_ context.Context, compilerPath string) (string, error) {
			if compilerPath != "/locked/go" {
				t.Fatalf("compiler path = %q", compilerPath)
			}
			return closureDigest, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(executable)
	if identity.BuilderSHA256 != "sha256:"+hex.EncodeToString(wantSum[:]) {
		t.Fatalf("builder SHA = %q", identity.BuilderSHA256)
	}
	if identity.ToolchainDigest != closureDigest {
		t.Fatalf("toolchain digest = %q, want %q", identity.ToolchainDigest, closureDigest)
	}
}

func TestArtifactBuilderObservedIdentityRejectsIncompleteDependencies(t *testing.T) {
	t.Parallel()

	if _, err := measureArtifactBuilderObservedIdentity(context.Background(), artifactBuilderObservedIdentityDependencies{}); err == nil {
		t.Fatal("measurement accepted incomplete dependencies")
	}
	var nilContext context.Context
	if _, err := measureArtifactBuilderObservedIdentity(nilContext, artifactBuilderObservedIdentityDependencies{}); err == nil {
		t.Fatal("measurement accepted nil context")
	}
}

func TestArtifactBuilderObservedIdentityCanonicalJSONAndDynamicFieldGuard(t *testing.T) {
	t.Parallel()

	identity := ArtifactBuilderObservedIdentity{
		SchemaVersion:   ArtifactBuilderObservedIdentitySchema,
		BuilderSHA256:   "sha256:" + strings.Repeat("a", 64),
		ToolchainDigest: "sha256:" + strings.Repeat("b", 64),
	}
	encoded, err := identity.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > artifactBuilderObservedIdentityMaxBytes {
		t.Fatalf("encoded identity size = %d", len(encoded))
	}
	decoded, err := DecodeArtifactBuilderObservedIdentity(encoded)
	if err != nil || decoded != identity {
		t.Fatalf("decoded identity = %#v, err = %v", decoded, err)
	}
	assertArtifactBuilderObservedIdentityJSONFields(t, encoded)
}

func assertArtifactBuilderObservedIdentityJSONFields(t *testing.T, encoded []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	typeOfIdentity := reflect.TypeFor[ArtifactBuilderObservedIdentity]()
	if typeOfIdentity.NumField() != len(fields) {
		t.Fatalf("production fields = %d, JSON fields = %d", typeOfIdentity.NumField(), len(fields))
	}
	for index := 0; index < typeOfIdentity.NumField(); index++ {
		field := typeOfIdentity.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("field %q has no JSON contract", field.Name)
		}
		if _, ok := fields[name]; !ok {
			t.Fatalf("JSON omitted production field %q", name)
		}
	}
}

func TestArtifactBuilderObservedIdentityRejectsUnknownArgsAndJSON(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := executeCLI([]string{"_artifact-identity", "--unknown"})
	if code == 0 || stdout != "" || stderr == "" {
		t.Fatalf("unknown artifact identity args = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	input := []byte(`{"schema_version":"artifact-builder-observed-identity/v1","builder_sha256":"sha256:` + strings.Repeat("a", 64) + `","toolchain_digest":"sha256:` + strings.Repeat("b", 64) + `","unknown":"field"}`)
	if _, err := DecodeArtifactBuilderObservedIdentity(input); err == nil {
		t.Fatal("DecodeArtifactBuilderObservedIdentity accepted an unknown field")
	}
}

func TestArtifactBuilderObservedIdentityCLIUsesLinuxAuthorityOnly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("current executable authority is Linux /proc/self/exe only")
	}
	code, stdout, stderr := executeCLI([]string{"_artifact-identity"})
	if code != 0 || stderr != "" {
		t.Fatalf("artifact identity CLI = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	identity, err := DecodeArtifactBuilderObservedIdentity([]byte(strings.TrimSuffix(stdout, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if identity.SchemaVersion != ArtifactBuilderObservedIdentitySchema || !isDigest(identity.BuilderSHA256) || !isDigest(identity.ToolchainDigest) {
		t.Fatalf("CLI identity = %#v", identity)
	}
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("CLI wrote more than one JSON line: %q", stdout)
	}
}
