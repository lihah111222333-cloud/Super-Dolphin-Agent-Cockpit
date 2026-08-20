package remotecicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
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

func TestArtifactBuilderObservedIdentityFailureStageDoesNotLeakCause(t *testing.T) {
	_, err := measureArtifactBuilderObservedIdentity(context.Background(), artifactBuilderObservedIdentityDependencies{
		OpenExecutable: func() (io.ReadSeeker, error) { return bytes.NewReader([]byte("executable")), nil },
		CompilerPath:   func() (string, error) { return "/locked/go", nil },
		CompilerClosureDigest: func(context.Context, string) (string, error) {
			return "", errors.New("/secret/goroot/private")
		},
	})
	if err == nil || observedIdentityFailureStage(err) != "toolchain_unknown" {
		t.Fatalf("failure stage = %q, error=%v", observedIdentityFailureStage(err), err)
	}
	if strings.Contains(err.Error(), "/secret/") {
		t.Fatalf("failure leaked raw cause: %v", err)
	}
}

func TestExplicitCompilerPathValidationRejectsImplicitOrUnsafePaths(t *testing.T) {
	for _, value := range []string{"", "go", "/usr/local/go/../go/bin/go", "/usr/local/go/bin/go\n"} {
		if err := validateCompilerPath(value); err == nil {
			t.Fatalf("validateCompilerPath(%q) accepted unsafe path", value)
		}
	}
}

func TestArtifactBuilderObservedIdentityCanonicalJSONAndDynamicFieldGuard(t *testing.T) {
	t.Parallel()

	identity := ArtifactBuilderObservedIdentity{
		SchemaVersion:             ArtifactBuilderObservedIdentitySchema,
		BuilderSHA256:             "sha256:" + strings.Repeat("a", 64),
		ToolchainDigest:           "sha256:" + strings.Repeat("b", 64),
		ProtocolABIDigest:         "sha256:" + strings.Repeat("c", 64),
		WorkloadManifestABIDigest: "sha256:" + strings.Repeat("d", 64),
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
	for field := range typeOfIdentity.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
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

	for _, args := range [][]string{{"_artifact-identity", "--unknown"}, {"_artifact-identity"}, {"_artifact-identity", "--compiler-path"}, {"_artifact-identity", "--compiler-path", "/usr/local/go/bin/go", "--compiler-path", "/usr/local/go/bin/go"}} {
		code, stdout, stderr := executeCLI(args)
		if code == 0 || stdout != "" || stderr == "" {
			t.Fatalf("invalid artifact identity args %#v = code %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	input := []byte(`{"schema_version":"artifact-builder-observed-identity/v1","builder_sha256":"sha256:` + strings.Repeat("a", 64) + `","toolchain_digest":"sha256:` + strings.Repeat("b", 64) + `","protocol_abi_digest":"sha256:` + strings.Repeat("c", 64) + `","workload_manifest_abi_digest":"sha256:` + strings.Repeat("d", 64) + `","unknown":"field"}`)
	if _, err := DecodeArtifactBuilderObservedIdentity(input); err == nil {
		t.Fatal("DecodeArtifactBuilderObservedIdentity accepted an unknown field")
	}
}

func TestArtifactBuilderObservedIdentityCLIUsesLinuxAuthorityOnly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("current executable authority is Linux /proc/self/exe only")
	}
	code, stdout, stderr := executeCLI([]string{"_artifact-identity", "--compiler-path", compilerPathForTest(t)})
	if code != 0 || stderr != "" {
		t.Fatalf("artifact identity CLI = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	assertLinuxArtifactBuilderIdentityOutput(t, stdout)
}

func assertLinuxArtifactBuilderIdentityOutput(t *testing.T, stdout string) {
	t.Helper()
	identity, err := DecodeArtifactBuilderObservedIdentity([]byte(strings.TrimSuffix(stdout, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if identity.SchemaVersion != ArtifactBuilderObservedIdentitySchema {
		t.Fatalf("CLI identity = %#v", identity)
	}
	for _, digest := range []string{identity.BuilderSHA256, identity.ToolchainDigest, identity.ProtocolABIDigest, identity.WorkloadManifestABIDigest} {
		if !isDigest(digest) {
			t.Fatalf("CLI identity digest = %q", digest)
		}
	}
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("CLI wrote more than one JSON line: %q", stdout)
	}
}

func TestArtifactBuilderObservedIdentityProductionReadUsesCommittedABIs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("current executable authority is Linux /proc/self/exe only")
	}
	identity, err := ReadArtifactBuilderObservedIdentity(context.Background(), compilerPathForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := remoteci.BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	workload, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	if identity.ProtocolABIDigest != protocol || identity.WorkloadManifestABIDigest != workload {
		t.Fatalf("production ABI identity = %#v, want protocol=%q workload=%q", identity, protocol, workload)
	}
}

func compilerPathForTest(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("compiler path is not executable regular file: %q (%v)", path, err)
	}
	return path
}
