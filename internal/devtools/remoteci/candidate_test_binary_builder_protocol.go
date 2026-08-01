package remoteci

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// CandidateTestBinaryBuilderRequestSchemaVersion is the only accepted remote builder request format.
const CandidateTestBinaryBuilderRequestSchemaVersion uint32 = 1

// CandidateTestBinaryBuilderResultSchemaVersion is the only accepted remote builder result format.
const CandidateTestBinaryBuilderResultSchemaVersion uint32 = 1

// CandidateTestBinaryBuildTarget is one deduplicated exact normal Go-test target.
type CandidateTestBinaryBuildTarget struct {
	Package    string `json:"package"`
	Mode       string `json:"mode"`
	CGOEnabled bool   `json:"cgo_enabled"`
}

// CandidateTestBinaryBuilderRequest binds a Linux builder to the same immutable source, baseline and candidate CLI as its test shards.
type CandidateTestBinaryBuilderRequest struct {
	SchemaVersion    uint32                           `json:"schema_version"`
	JobID            string                           `json:"job_id"`
	CandidateTree    string                           `json:"candidate_tree"`
	BaselineManifest string                           `json:"runner_manifest_digest"`
	AnchorGeneration uint64                           `json:"anchor_generation"`
	AnchorManifest   string                           `json:"anchor_manifest_digest"`
	AnchorCommit     string                           `json:"anchor_commit"`
	AnchorTree       string                           `json:"anchor_tree"`
	BaselineDeltas   []BaselineDeltaLayer             `json:"baseline_deltas,omitempty"`
	RunnerBaseCommit string                           `json:"runner_base_commit"`
	RunnerBaseTree   string                           `json:"runner_base_tree"`
	PatchFormat      string                           `json:"patch_format"`
	PatchKey         string                           `json:"patch_key"`
	PatchSHA256      string                           `json:"patch_sha256"`
	PatchSize        int64                            `json:"patch_size"`
	ManifestKey      string                           `json:"manifest_key"`
	ManifestSHA256   string                           `json:"manifest_sha256"`
	CandidateCLI     CandidateCLIArtifactRef          `json:"candidate_cli_artifact"`
	CGOEnabled       bool                             `json:"cgo_enabled"`
	Targets          []CandidateTestBinaryBuildTarget `json:"targets"`
	OutputPrefix     string                           `json:"output_prefix"`
}

// CandidateTestBinaryBuildMetrics is builder-observed per-package compilation evidence.
type CandidateTestBinaryBuildMetrics struct {
	// GoListWallMS and BuildWallMS are command wall time. Action values are
	// action-graph sums and must never be interpreted as wall time.
	GoListWallMS                    uint64                                  `json:"go_list_wall_ms"`
	BuildWallMS                     uint64                                  `json:"build_wall_ms"`
	CompileActionMS                 uint64                                  `json:"compile_action_ms"`
	LinkActionMS                    uint64                                  `json:"link_action_ms"`
	CompileCriticalWallMS           uint64                                  `json:"compile_critical_wall_ms"`
	GOCachePrivateHits              uint64                                  `json:"gocache_private_hits"`
	GOCacheBaselineHitsByGeneration []CandidateTestBinaryCacheGenerationHit `json:"gocache_baseline_hits_by_generation"`
	GOCacheMisses                   uint64                                  `json:"gocache_misses"`
	GOCachePuts                     uint64                                  `json:"gocache_puts"`
	GOCachePrivateRootIdentity      string                                  `json:"gocache_private_root_identity"`
}

// CandidateTestBinaryCacheGenerationHit is one immutable baseline cache generation observation.
type CandidateTestBinaryCacheGenerationHit struct {
	Generation           uint64 `json:"generation"`
	Hits                 uint64 `json:"hits"`
	AnchorGeneration     uint64 `json:"anchor_generation"`
	AnchorManifestDigest string `json:"anchor_manifest_digest"`
	ManifestDigest       string `json:"manifest_digest"`
	CacheRootIdentity    string `json:"cache_root_identity"`
}

// CandidateTestBinaryBuilderBuild records one artifact reference and its builder metrics.
type CandidateTestBinaryBuilderBuild struct {
	Artifact CandidateTestBinaryArtifactRef  `json:"artifact"`
	Metrics  CandidateTestBinaryBuildMetrics `json:"metrics"`
}

// CandidateTestBinaryBuilderResult is uploaded by the builder only after every artifact is content-addressed.
type CandidateTestBinaryBuilderResult struct {
	SchemaVersion   uint32                            `json:"schema_version"`
	JobID           string                            `json:"job_id"`
	CandidateTree   string                            `json:"candidate_tree"`
	Platform        string                            `json:"platform"`
	GoToolchain     string                            `json:"go_toolchain"`
	CGOEnabled      bool                              `json:"cgo_enabled"`
	ToolchainSHA256 string                            `json:"toolchain_sha256"`
	Builds          []CandidateTestBinaryBuilderBuild `json:"builds"`
}

func (request CandidateTestBinaryBuilderRequest) Validate() error {
	return request.validate(path.Dir(request.PatchKey) + "/")
}

func (request CandidateTestBinaryBuilderRequest) validate(objectPrefix string) error {
	if request.SchemaVersion != CandidateTestBinaryBuilderRequestSchemaVersion || !remoteIDPattern.MatchString(request.JobID) || !remoteOIDPattern.MatchString(request.CandidateTree) || !validObjectPrefix(request.OutputPrefix) || path.Base(strings.TrimSuffix(request.OutputPrefix, "/")) != "test-binaries" {
		return errors.New("candidate test binary builder request identity is invalid")
	}
	shard := ShardRequest{SchemaVersion: ShardRequestSchemaVersion, JobID: request.JobID, ShardIdentity: "sha256:" + strings.Repeat("0", sha256.Size*2), Profile: "local-fast", PlanDigest: "sha256:" + strings.Repeat("0", sha256.Size*2), BaselineManifest: request.BaselineManifest, AnchorGeneration: request.AnchorGeneration, AnchorManifest: request.AnchorManifest, AnchorCommit: request.AnchorCommit, AnchorTree: request.AnchorTree, BaselineDeltas: slices.Clone(request.BaselineDeltas), RunnerBaseCommit: request.RunnerBaseCommit, RunnerBaseTree: request.RunnerBaseTree, SourceTreeSHA: request.CandidateTree, PatchFormat: request.PatchFormat, PatchKey: request.PatchKey, PatchSHA256: request.PatchSHA256, PatchSize: request.PatchSize, ManifestKey: request.ManifestKey, ManifestSHA256: request.ManifestSHA256, CandidateCLI: request.CandidateCLI, GateIDs: []gate.GateID{"builder"}}
	if err := shard.validateBaselineChain(); err != nil || shard.validateSource() != nil || request.CandidateCLI.Validate(objectPrefix, request.CandidateTree) != nil {
		return errors.New("candidate test binary builder request source binding is invalid")
	}
	if len(request.Targets) == 0 || len(request.Targets) > 64 {
		return errors.New("candidate test binary builder request target count is invalid")
	}
	seen := make(map[string]struct{}, len(request.Targets))
	for _, target := range request.Targets {
		if !request.CGOEnabled || !validGoTestBinaryBuild(target.Package, target.Mode, "linux/amd64", "go1.26.5", target.CGOEnabled, []string{"-mod=readonly", "-buildvcs=false", "-trimpath"}) {
			return errors.New("candidate test binary builder request target is invalid")
		}
		identity := target.Package + "\x00" + target.Mode
		if _, duplicate := seen[identity]; duplicate {
			return errors.New("candidate test binary builder request target is duplicated")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func (result CandidateTestBinaryBuilderResult) Validate() error {
	if result.SchemaVersion != CandidateTestBinaryBuilderResultSchemaVersion || !remoteIDPattern.MatchString(result.JobID) || !remoteOIDPattern.MatchString(result.CandidateTree) || result.Platform != "linux/amd64" || result.GoToolchain != "go1.26.5" || !result.CGOEnabled || !remoteDigestPattern.MatchString(result.ToolchainSHA256) || len(result.Builds) == 0 || len(result.Builds) > 64 {
		return errors.New("candidate test binary builder result identity is invalid")
	}
	return nil
}

// ValidateAgainst rejects builder output that is not bound to the submitted request.
func (result CandidateTestBinaryBuilderResult) ValidateAgainst(request CandidateTestBinaryBuilderRequest) error {
	if err := result.Validate(); err != nil {
		return err
	}
	if result.SchemaVersion != CandidateTestBinaryBuilderResultSchemaVersion || result.JobID != request.JobID || result.CandidateTree != request.CandidateTree || result.Platform != "linux/amd64" || result.GoToolchain != "go1.26.5" || result.CGOEnabled != request.CGOEnabled || result.ToolchainSHA256 != request.CandidateCLI.ToolchainSHA256 || len(result.Builds) != len(request.Targets) {
		return errors.New("candidate test binary builder result identity is invalid")
	}
	seen := make(map[string]struct{}, len(result.Builds))
	for _, build := range result.Builds {
		ref := build.Artifact
		if ref.CandidateTree != request.CandidateTree || ref.Platform != result.Platform || ref.GoToolchain != result.GoToolchain || ref.CGOEnabled != result.CGOEnabled || ref.ToolchainSHA256 != result.ToolchainSHA256 || !slices.Equal(ref.BuildFlags, []string{"-mod=readonly", "-buildvcs=false", "-trimpath"}) {
			return errors.New("candidate test binary builder result artifact identity is invalid")
		}
		if err := ref.Validate(request.OutputPrefix, request.CandidateTree); err != nil {
			return err
		}
		identity := ref.Package + "\x00" + ref.Mode
		if _, duplicate := seen[identity]; duplicate {
			return errors.New("candidate test binary builder result artifact is duplicated")
		}
		if err := validateCandidateTestBinaryBuildMetrics(build.Metrics); err != nil {
			return err
		}
		for _, hit := range build.Metrics.GOCacheBaselineHitsByGeneration {
			if hit.AnchorGeneration != request.AnchorGeneration || hit.AnchorManifestDigest != request.AnchorManifest {
				return errors.New("candidate test binary builder cache anchor identity is invalid")
			}
			if hit.Generation == request.AnchorGeneration {
				if hit.ManifestDigest != request.BaselineManifest {
					return errors.New("candidate test binary builder anchor cache identity is invalid")
				}
				continue
			}
			matched := false
			for _, delta := range request.BaselineDeltas {
				matched = delta.Generation == hit.Generation && delta.ManifestDigest == hit.ManifestDigest
				if matched {
					break
				}
			}
			if !matched {
				return errors.New("candidate test binary builder delta cache identity is invalid")
			}
		}
		seen[identity] = struct{}{}
	}
	for _, target := range request.Targets {
		if _, ok := seen[target.Package+"\x00"+target.Mode]; !ok {
			return errors.New("candidate test binary builder result target is missing")
		}
	}
	return nil
}

func validateCandidateTestBinaryBuildMetrics(metrics CandidateTestBinaryBuildMetrics) error {
	var previous uint64
	if !remoteDigestPattern.MatchString(metrics.GOCachePrivateRootIdentity) {
		return errors.New("candidate test binary builder private cache identity is invalid")
	}
	for index, baseline := range metrics.GOCacheBaselineHitsByGeneration {
		if baseline.Generation == 0 || baseline.Hits == 0 || !remoteDigestPattern.MatchString(baseline.AnchorManifestDigest) || !remoteDigestPattern.MatchString(baseline.ManifestDigest) || !remoteDigestPattern.MatchString(baseline.CacheRootIdentity) || (index != 0 && baseline.Generation <= previous) {
			return errors.New("candidate test binary builder baseline cache metrics are invalid")
		}
		previous = baseline.Generation
	}
	return nil
}

func EncodeCandidateTestBinaryBuilderRequest(request CandidateTestBinaryBuilderRequest) ([]byte, string, error) {
	if err := request.Validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, "", fmt.Errorf("encode candidate test binary builder request: %w", err)
	}
	return data, fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func DecodeCandidateTestBinaryBuilderRequest(data []byte) (CandidateTestBinaryBuilderRequest, error) {
	var request CandidateTestBinaryBuilderRequest
	if err := gate.DecodeStrictJSON(data, &request); err != nil {
		return request, fmt.Errorf("decode candidate test binary builder request: %w", err)
	}
	if err := request.Validate(); err != nil {
		return request, err
	}
	return request, nil
}

func EncodeCandidateTestBinaryBuilderResult(result CandidateTestBinaryBuilderResult) ([]byte, string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, "", fmt.Errorf("encode candidate test binary builder result: %w", err)
	}
	return data, fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func DecodeCandidateTestBinaryBuilderResult(data []byte, request CandidateTestBinaryBuilderRequest) (CandidateTestBinaryBuilderResult, error) {
	var result CandidateTestBinaryBuilderResult
	if err := gate.DecodeStrictJSON(data, &result); err != nil {
		return result, fmt.Errorf("decode candidate test binary builder result: %w", err)
	}
	if err := result.ValidateAgainst(request); err != nil {
		return result, err
	}
	return result, nil
}
