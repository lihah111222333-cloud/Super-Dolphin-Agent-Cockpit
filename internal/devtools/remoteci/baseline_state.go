package remoteci

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const BaselineStateSchemaVersion = cicontract.BaselineStateSchemaVersion

// BaselineState is the accepted remote CI identity. ECI ImageCache is the sole
// executable cache authority; OCIProjectCache only describes verified content
// inside the immutable image and cannot select a runtime cache on its own.
type BaselineState struct {
	SchemaVersion          uint32                   `json:"schema_version"`
	Generation             uint64                   `json:"generation"`
	ExecutionProvider      string                   `json:"execution_provider"`
	RegionID               string                   `json:"region_id"`
	MainCommit             string                   `json:"main_commit"`
	MainTree               string                   `json:"main_tree"`
	Platform               string                   `json:"platform"`
	PolicyDigest           string                   `json:"policy_digest"`
	ToolchainDigest        string                   `json:"toolchain_digest"`
	RuntimeImage           string                   `json:"runtime_image"`
	ImageCacheID           string                   `json:"image_cache_id"`
	ImageCacheSnapshotID   string                   `json:"image_cache_snapshot_id"`
	ImageCacheReady        bool                     `json:"image_cache_ready"`
	ImageDigest            string                   `json:"image_digest"`
	OCIProjectCache        *BaselineOCIProjectCache `json:"oci_project_cache"`
	GateBinarySHA256       string                   `json:"gate_binary_sha256"`
	RuntimeSeedSHA256      string                   `json:"runtime_seed_manifest_sha256"`
	BaselineManifestDigest string                   `json:"baseline_manifest_digest"`
	CreatedAt              time.Time                `json:"created_at"`
	AcceptedAt             time.Time                `json:"accepted_at"`
	RenewedAt              time.Time                `json:"renewed_at"`
}

// baselineIdentity contains all inputs whose change requires a new baseline generation.
type baselineIdentity struct{ MainCommit, MainTree, Platform, PolicyDigest, ToolchainDigest, RuntimeImage string }

// UnmarshalJSON 复用 cicontract 的 canonical owner 严格解码 accepted state。
func (state *BaselineState) UnmarshalJSON(data []byte) error {
	canonical, err := cicontract.DecodeAcceptedBaselineState(data)
	if err != nil {
		return err
	}
	type stateWire BaselineState
	var wire stateWire
	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		return fmt.Errorf("marshal canonical remote baseline state: %w", err)
	}
	if err := json.Unmarshal(canonicalJSON, &wire); err != nil {
		return err
	}
	*state = BaselineState(wire)
	return nil
}

// Validate rejects incomplete or non-OCI baseline state.
func (state BaselineState) Validate() error {
	return state.acceptedBaselineState().Validate()
}

func (state BaselineState) identity() baselineIdentity {
	return baselineIdentity{state.MainCommit, state.MainTree, state.Platform, state.PolicyDigest, state.ToolchainDigest, state.RuntimeImage}
}

func (state BaselineState) acceptedBaselineState() cicontract.AcceptedBaselineState {
	return cicontract.AcceptedBaselineState{
		SchemaVersion: state.SchemaVersion, Generation: state.Generation, ExecutionProvider: state.ExecutionProvider, RegionID: state.RegionID,
		MainCommit: state.MainCommit, MainTree: state.MainTree, Platform: state.Platform, PolicyDigest: state.PolicyDigest,
		ToolchainDigest: state.ToolchainDigest, RuntimeImage: state.RuntimeImage, ImageCacheID: state.ImageCacheID,
		ImageCacheSnapshotID: state.ImageCacheSnapshotID, ImageCacheReady: state.ImageCacheReady, ImageDigest: state.ImageDigest,
		OCIProjectCache: state.OCIProjectCache, GateBinarySHA256: state.GateBinarySHA256, RuntimeSeedSHA256: state.RuntimeSeedSHA256,
		BaselineManifestDigest: state.BaselineManifestDigest, CreatedAt: state.CreatedAt, AcceptedAt: state.AcceptedAt, RenewedAt: state.RenewedAt,
	}
}
