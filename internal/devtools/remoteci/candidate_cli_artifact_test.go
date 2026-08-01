package remoteci

import (
	"strings"
	"testing"
)

func TestCandidateCLIArtifactManifestStrictRoundTrip(t *testing.T) {
	manifest := CandidateCLIArtifactManifest{
		SchemaVersion: CandidateCLIArtifactSchemaVersion, CandidateTree: strings.Repeat("a", 40),
		SourceSHA256: "sha256:" + strings.Repeat("b", 64), ToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		Platform: "linux/amd64", BinaryKey: "candidate-artifacts/job-012/candidate-linux-amd64.candidate-cli",
		BinarySHA256: "sha256:" + strings.Repeat("d", 64), BinarySize: 123,
	}
	manifest.CLIIdentity = CandidateCLIIdentity(manifest.SourceSHA256, manifest.ToolchainSHA256)
	data, digest, err := EncodeCandidateCLIArtifactManifest(manifest)
	if err != nil {
		t.Fatalf("EncodeCandidateCLIArtifactManifest() error = %v", err)
	}
	if !remoteDigestPattern.MatchString(digest) {
		t.Fatalf("manifest digest = %q", digest)
	}
	decoded, err := DecodeCandidateCLIArtifactManifest(data)
	if err != nil || decoded != manifest {
		t.Fatalf("DecodeCandidateCLIArtifactManifest() = %#v, %v", decoded, err)
	}
}

func TestCandidateCLIArtifactManifestRejectsIdentityDrift(t *testing.T) {
	manifest := CandidateCLIArtifactManifest{SchemaVersion: CandidateCLIArtifactSchemaVersion, CandidateTree: strings.Repeat("a", 40), SourceSHA256: "sha256:" + strings.Repeat("b", 64), ToolchainSHA256: "sha256:" + strings.Repeat("c", 64), Platform: "linux/amd64", BinaryKey: "candidate-artifacts/job-012/candidate-linux-amd64.candidate-cli", BinarySHA256: "sha256:" + strings.Repeat("d", 64), BinarySize: 1, CLIIdentity: "wrong"}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted CLI identity drift")
	}
}
