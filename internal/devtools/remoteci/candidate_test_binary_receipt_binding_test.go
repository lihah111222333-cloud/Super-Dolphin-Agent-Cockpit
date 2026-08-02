package remoteci

import (
	"strings"
	"testing"
)

func TestCandidateTestBinaryReceiptBindingDigestIsCanonicalAndBindsBinary(t *testing.T) {
	manifest := validCandidateTestBinaryArtifactManifest()
	ref := CandidateTestBinaryArtifactRef{CandidateTree: manifest.CandidateTree, Package: manifest.Package, Mode: manifest.Mode, Platform: manifest.Platform, GoToolchain: manifest.GoToolchain, CGOEnabled: manifest.CGOEnabled, ToolchainSHA256: manifest.ToolchainSHA256, BuildFlags: manifest.BuildFlags, CompileClosureSHA256: manifest.CompileClosureSHA256, ManifestSHA256: strings.Repeat("b", 64), BinarySHA256: strings.TrimPrefix(manifest.BinarySHA256, "sha256:"), BinarySize: manifest.BinarySize}
	build := CandidateTestBinaryBuilderBuild{Artifact: ref, Metrics: CandidateTestBinaryBuildMetrics{GOCachePrivateRootIdentity: "sha256:" + strings.Repeat("d", 64)}}
	first, err := CandidateTestBinaryReceiptBindingDigest([]CandidateTestBinaryBuilderBuild{build}, manifest.CandidateTree)
	if err != nil {
		t.Fatal(err)
	}
	build.Artifact.BinarySHA256 = strings.Repeat("c", 64)
	second, err := CandidateTestBinaryReceiptBindingDigest([]CandidateTestBinaryBuilderBuild{build}, manifest.CandidateTree)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("candidate test binary receipt binding accepted a substituted binary")
	}
	build.Artifact.BinarySHA256 = ref.BinarySHA256
	build.Metrics.BuildWallMS = 1
	metricsDigest, err := CandidateTestBinaryReceiptBindingDigest([]CandidateTestBinaryBuilderBuild{build}, manifest.CandidateTree)
	if err != nil {
		t.Fatal(err)
	}
	if first == metricsDigest {
		t.Fatal("candidate test binary receipt binding accepted substituted build metrics")
	}
	empty, err := CandidateTestBinaryReceiptBindingDigest(nil, manifest.CandidateTree)
	if err != nil || !remoteDigestPattern.MatchString(empty) || empty == "" {
		t.Fatalf("canonical empty candidate test binary receipt binding = %q, %v", empty, err)
	}
	if emptyCandidateTestBinaryReceiptBindingDigest() != empty {
		t.Fatalf("precomputed empty binding = %q, want %q", emptyCandidateTestBinaryReceiptBindingDigest(), empty)
	}
}
