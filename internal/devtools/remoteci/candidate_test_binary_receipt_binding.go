package remoteci

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
)

// canonicalEmptyCandidateTestBinaryReceiptBindingDigest is SHA-256 of the
// canonical JSON empty array. It cannot fail and is not an omitted field.
const canonicalEmptyCandidateTestBinaryReceiptBindingDigest = "sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"

// CandidateTestBinaryReceiptBindingDigest returns the canonical binding of every
// candidate test binary and its observed build/cache metrics. An empty list is
// deliberate: it encodes as the canonical empty binding, never as omission.
func CandidateTestBinaryReceiptBindingDigest(builds []CandidateTestBinaryBuilderBuild, candidateTree string) (string, error) {
	if !remoteOIDPattern.MatchString(candidateTree) {
		return "", fmt.Errorf("candidate test binary receipt binding candidate tree is invalid")
	}
	canonical := make([]candidateTestBinaryReceiptBinding, len(builds))
	seen := make(map[string]struct{}, len(builds))
	for index, build := range builds {
		ref := build.Artifact
		if ref.CandidateTree != candidateTree || !validGoTestBinaryBuild(ref.Package, ref.Mode, ref.Platform, ref.GoToolchain, ref.CGOEnabled, ref.BuildFlags) ||
			!remoteDigestPattern.MatchString(ref.ToolchainSHA256) || !remoteDigestPattern.MatchString(ref.CompileClosureSHA256) ||
			!validObjectDigest(ref.ManifestSHA256) || !validObjectDigest(ref.BinarySHA256) ||
			ref.BinarySize <= 0 || ref.BinarySize > 512<<20 || validateCandidateTestBinaryBuildMetrics(build.Metrics) != nil {
			return "", fmt.Errorf("candidate test binary receipt binding[%d] is invalid", index)
		}
		identity := ref.Package + "\x00" + ref.Mode
		if _, duplicate := seen[identity]; duplicate {
			return "", fmt.Errorf("candidate test binary receipt binding has duplicate %q", identity)
		}
		seen[identity] = struct{}{}
		baseline := slices.Clone(build.Metrics.GOCacheBaselineHitsByGeneration)
		slices.SortFunc(baseline, func(left, right CandidateTestBinaryCacheGenerationHit) int {
			return compareUint64(left.Generation, right.Generation)
		})
		canonical[index] = candidateTestBinaryReceiptBinding{CandidateTree: ref.CandidateTree, Package: ref.Package, Mode: ref.Mode, Platform: ref.Platform, GoToolchain: ref.GoToolchain, CGOEnabled: ref.CGOEnabled, ToolchainSHA256: ref.ToolchainSHA256, BuildFlags: slices.Clone(ref.BuildFlags), CompileClosureSHA256: ref.CompileClosureSHA256, ManifestSHA256: ref.ManifestSHA256, BinarySHA256: "sha256:" + ref.BinarySHA256, BinarySize: ref.BinarySize, GoListWallMS: build.Metrics.GoListWallMS, BuildWallMS: build.Metrics.BuildWallMS, CompileActionMS: build.Metrics.CompileActionMS, LinkActionMS: build.Metrics.LinkActionMS, CompileCriticalWallMS: build.Metrics.CompileCriticalWallMS, GOCachePrivateHits: build.Metrics.GOCachePrivateHits, GOCachePrivateRootIdentity: build.Metrics.GOCachePrivateRootIdentity, GOCacheBaselineHitRecords: baseline, GOCacheMisses: build.Metrics.GOCacheMisses, GOCachePuts: build.Metrics.GOCachePuts}
	}
	slices.SortFunc(canonical, func(left, right candidateTestBinaryReceiptBinding) int {
		if left.Package != right.Package {
			return compare(left.Package, right.Package)
		}
		return compare(left.Mode, right.Mode)
	})
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal candidate test binary receipt binding: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

func emptyCandidateTestBinaryReceiptBindingDigest() string {
	return canonicalEmptyCandidateTestBinaryReceiptBindingDigest
}

// CandidateTestBinaryReceiptBindingDigestFromBuilds recomputes the run binding
// from coordinator-validated builder observations.
func CandidateTestBinaryReceiptBindingDigestFromBuilds(builds []CandidateTestBinaryBuilderBuild, candidateTree string) (string, error) {
	return CandidateTestBinaryReceiptBindingDigest(builds, candidateTree)
}

type candidateTestBinaryReceiptBinding struct {
	CandidateTree              string                                  `json:"candidate_tree"`
	Package                    string                                  `json:"package"`
	Mode                       string                                  `json:"mode"`
	Platform                   string                                  `json:"platform"`
	GoToolchain                string                                  `json:"go_toolchain"`
	CGOEnabled                 bool                                    `json:"cgo_enabled"`
	ToolchainSHA256            string                                  `json:"toolchain_sha256"`
	BuildFlags                 []string                                `json:"build_flags"`
	CompileClosureSHA256       string                                  `json:"compile_closure_sha256"`
	ManifestSHA256             string                                  `json:"manifest_sha256"`
	BinarySHA256               string                                  `json:"binary_sha256"`
	BinarySize                 int64                                   `json:"binary_size"`
	GoListWallMS               uint64                                  `json:"go_list_wall_ms"`
	BuildWallMS                uint64                                  `json:"build_wall_ms"`
	CompileActionMS            uint64                                  `json:"compile_action_ms"`
	LinkActionMS               uint64                                  `json:"link_action_ms"`
	CompileCriticalWallMS      uint64                                  `json:"compile_critical_wall_ms"`
	GOCachePrivateHits         uint64                                  `json:"gocache_private_hits"`
	GOCachePrivateRootIdentity string                                  `json:"gocache_private_root_identity"`
	GOCacheBaselineHitRecords  []CandidateTestBinaryCacheGenerationHit `json:"gocache_baseline_hit_records"`
	GOCacheMisses              uint64                                  `json:"gocache_misses"`
	GOCachePuts                uint64                                  `json:"gocache_puts"`
}

func compareUint64(left, right uint64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compare(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
