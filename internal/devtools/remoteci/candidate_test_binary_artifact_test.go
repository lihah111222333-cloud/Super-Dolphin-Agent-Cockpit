package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCandidateTestBinaryArtifactManifestStrictRoundTrip(t *testing.T) {
	manifest := validCandidateTestBinaryArtifactManifest()
	data, digest, err := EncodeCandidateTestBinaryArtifactManifest(manifest)
	if err != nil {
		t.Fatalf("EncodeCandidateTestBinaryArtifactManifest() error = %v", err)
	}
	if !remoteDigestPattern.MatchString(digest) {
		t.Fatalf("manifest digest = %q", digest)
	}
	decoded, err := DecodeCandidateTestBinaryArtifactManifest(data)
	if err != nil || !equalCandidateTestBinaryManifest(decoded, manifest) {
		t.Fatalf("DecodeCandidateTestBinaryArtifactManifest() = %#v, %v", decoded, err)
	}
}

func TestCandidateTestBinaryArtifactManifestRejectsRequiredBindingDrift(t *testing.T) {
	for name, mutate := range map[string]func(*CandidateTestBinaryArtifactManifest){
		"candidate tree":   func(manifest *CandidateTestBinaryArtifactManifest) { manifest.CandidateTree = "" },
		"package":          func(manifest *CandidateTestBinaryArtifactManifest) { manifest.Package = "" },
		"mode":             func(manifest *CandidateTestBinaryArtifactManifest) { manifest.Mode = "benchmark" },
		"platform":         func(manifest *CandidateTestBinaryArtifactManifest) { manifest.Platform = "darwin/arm64" },
		"toolchain":        func(manifest *CandidateTestBinaryArtifactManifest) { manifest.GoToolchain = "" },
		"toolchain digest": func(manifest *CandidateTestBinaryArtifactManifest) { manifest.ToolchainSHA256 = "" },
		"compile closure":  func(manifest *CandidateTestBinaryArtifactManifest) { manifest.CompileClosureSHA256 = "" },
		"binary sha":       func(manifest *CandidateTestBinaryArtifactManifest) { manifest.BinarySHA256 = "" },
		"binary size":      func(manifest *CandidateTestBinaryArtifactManifest) { manifest.BinarySize = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			manifest := validCandidateTestBinaryArtifactManifest()
			mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatalf("Validate() accepted %s drift", name)
			}
		})
	}
}

func TestCandidateTestBinaryArtifactManifestRejectsNonBaselineToolchains(t *testing.T) {
	for _, toolchain := range []string{"go1.25.7", "go1.26.4", "go1.26.6"} {
		t.Run(toolchain, func(t *testing.T) {
			manifest := validCandidateTestBinaryArtifactManifest()
			manifest.GoToolchain = toolchain
			if err := manifest.Validate(); err == nil {
				t.Fatalf("Validate() accepted non-baseline toolchain %q", toolchain)
			}
		})
	}
}

func validCandidateTestBinaryArtifactManifest() CandidateTestBinaryArtifactManifest {
	digest := "sha256:" + strings.Repeat("a", 64)
	return CandidateTestBinaryArtifactManifest{SchemaVersion: CandidateTestBinaryArtifactSchemaVersion, CandidateTree: strings.Repeat("b", 40), Package: "example.invalid/project/pkg", Mode: "test", Platform: "linux/amd64", GoToolchain: gate.RequiredGoToolchain, CGOEnabled: true, ToolchainSHA256: digest, BuildFlags: []string{"-trimpath", "-tags=integration"}, CompileClosureSHA256: digest, BinaryKey: "candidate-artifacts/job-012/package.test-bin", BinarySHA256: digest, BinarySize: 42}
}

func equalCandidateTestBinaryManifest(left, right CandidateTestBinaryArtifactManifest) bool {
	return left.SchemaVersion == right.SchemaVersion && left.CandidateTree == right.CandidateTree && left.Package == right.Package && left.Mode == right.Mode && left.Platform == right.Platform && left.GoToolchain == right.GoToolchain && left.ToolchainSHA256 == right.ToolchainSHA256 && strings.Join(left.BuildFlags, "\x00") == strings.Join(right.BuildFlags, "\x00") && left.CompileClosureSHA256 == right.CompileClosureSHA256 && left.BinaryKey == right.BinaryKey && left.BinarySHA256 == right.BinarySHA256 && left.BinarySize == right.BinarySize
}
