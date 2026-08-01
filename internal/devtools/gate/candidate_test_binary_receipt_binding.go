package gate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
)

// CandidateTestBinaryReceiptBindingDigest canonicalizes persisted candidate
// binary identities without importing the remote coordinator package.
func CandidateTestBinaryReceiptBindingDigest(builds []CandidateTestBinaryBuildRecord, candidateTree string) (string, error) {
	if !validCalibrationOID(candidateTree) {
		return "", fmt.Errorf("candidate tree is invalid")
	}
	canonical := make([]candidateTestBinaryReceiptBinding, len(builds))
	for index, build := range builds {
		if err := validateCandidateTestBinaryBuildRecord(build); err != nil || build.CandidateTree != candidateTree {
			return "", fmt.Errorf("candidate test binary binding %d is invalid", index)
		}
		baseline := slices.Clone(build.GOCacheBaselineHitRecords)
		slices.SortFunc(baseline, func(a, b CandidateTestBinaryCacheGenerationRecord) int {
			if a.Generation < b.Generation {
				return -1
			}
			if a.Generation > b.Generation {
				return 1
			}
			return 0
		})
		canonical[index] = candidateTestBinaryReceiptBinding{build.CandidateTree, build.Package, build.Mode, build.Platform, build.GoToolchain, build.CGOEnabled, build.ToolchainSHA256, slices.Clone(build.BuildFlags), build.CompileClosureSHA256, build.ManifestSHA256, build.ArtifactSHA256, build.BinarySize, build.GoListWallMS, build.BuildWallMS, build.CompileActionMS, build.LinkActionMS, build.CompileCriticalWallMS, build.GOCachePrivateHits, build.GOCachePrivateRootIdentity, baseline, build.GOCacheMisses, build.GOCachePuts}
	}
	slices.SortFunc(canonical, func(a, b candidateTestBinaryReceiptBinding) int {
		if a.Package != b.Package {
			if a.Package < b.Package {
				return -1
			}
			return 1
		}
		if a.Mode < b.Mode {
			return -1
		}
		if a.Mode > b.Mode {
			return 1
		}
		return 0
	})
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

type candidateTestBinaryReceiptBinding struct {
	CandidateTree              string                                     `json:"candidate_tree"`
	Package                    string                                     `json:"package"`
	Mode                       string                                     `json:"mode"`
	Platform                   string                                     `json:"platform"`
	GoToolchain                string                                     `json:"go_toolchain"`
	CGOEnabled                 bool                                       `json:"cgo_enabled"`
	ToolchainSHA256            string                                     `json:"toolchain_sha256"`
	BuildFlags                 []string                                   `json:"build_flags"`
	CompileClosureSHA256       string                                     `json:"compile_closure_sha256"`
	ManifestSHA256             string                                     `json:"manifest_sha256"`
	BinarySHA256               string                                     `json:"binary_sha256"`
	BinarySize                 int64                                      `json:"binary_size"`
	GoListWallMS               uint64                                     `json:"go_list_wall_ms"`
	BuildWallMS                uint64                                     `json:"build_wall_ms"`
	CompileActionMS            uint64                                     `json:"compile_action_ms"`
	LinkActionMS               uint64                                     `json:"link_action_ms"`
	CompileCriticalWallMS      uint64                                     `json:"compile_critical_wall_ms"`
	GOCachePrivateHits         uint64                                     `json:"gocache_private_hits"`
	GOCachePrivateRootIdentity string                                     `json:"gocache_private_root_identity"`
	GOCacheBaselineHitRecords  []CandidateTestBinaryCacheGenerationRecord `json:"gocache_baseline_hit_records"`
	GOCacheMisses              uint64                                     `json:"gocache_misses"`
	GOCachePuts                uint64                                     `json:"gocache_puts"`
}
