package remoteci

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const BaselineStateSchemaVersion uint32 = 11

// BaselineState is the accepted remote CI identity. ECI ImageCache is the sole
// executable cache authority; OCIProjectCache only describes verified content
// inside the immutable image and cannot select a runtime cache on its own.
type BaselineState struct {
	SchemaVersion                uint32                   `json:"schema_version"`
	Generation                   uint64                   `json:"generation"`
	MainCommit                   string                   `json:"main_commit"`
	MainTree                     string                   `json:"main_tree"`
	Platform                     string                   `json:"platform"`
	PolicyDigest                 string                   `json:"policy_digest"`
	ToolchainDigest              string                   `json:"toolchain_digest"`
	RuntimeImage                 string                   `json:"runtime_image"`
	ImageCacheID                 string                   `json:"image_cache_id"`
	ImageCacheSnapshotID         string                   `json:"image_cache_snapshot_id"`
	ImageCacheReady              bool                     `json:"image_cache_ready"`
	ImageDigest                  string                   `json:"image_digest"`
	OCIProjectCache              *BaselineOCIProjectCache `json:"oci_project_cache"`
	GateBinarySHA256             string                   `json:"gate_binary_sha256"`
	RuntimeSeedSHA256            string                   `json:"runtime_seed_manifest_sha256"`
	BaselineManifestDigest       string                   `json:"baseline_manifest_digest"`
	SourceSnapshotManifestDigest string                   `json:"source_snapshot_manifest_digest"`
	SourceSnapshotImagePath      string                   `json:"source_snapshot_image_path"`
	SourceSnapshotClosureDigest  string                   `json:"source_snapshot_closure_digest"`
	CreatedAt                    time.Time                `json:"created_at"`
	AcceptedAt                   time.Time                `json:"accepted_at"`
	RenewedAt                    time.Time                `json:"renewed_at"`
}

// BaselineIdentity contains all inputs whose change requires a new baseline generation.
type BaselineIdentity struct{ MainCommit, MainTree, Platform, PolicyDigest, ToolchainDigest, RuntimeImage string }

// UnmarshalJSON strictly decodes only the OCI-only state schema. Older state
// must never be silently reinterpreted as a current cache authority.
func (state *BaselineState) UnmarshalJSON(data []byte) error {
	var header struct {
		SchemaVersion uint32 `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("decode remote baseline state header: %w", err)
	}
	if header.SchemaVersion != BaselineStateSchemaVersion {
		return fmt.Errorf("remote baseline state schema %d is not accepted schema %d", header.SchemaVersion, BaselineStateSchemaVersion)
	}
	type stateWire BaselineState
	var wire stateWire
	if err := decodeSingleJSON(data, &wire); err != nil {
		return err
	}
	*state = BaselineState(wire)
	return nil
}

func decodeSingleJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("remote baseline state contains multiple JSON values")
		}
		return err
	}
	return nil
}

// Validate rejects incomplete or non-OCI baseline state.
func (state BaselineState) Validate() error {
	if state.SchemaVersion != BaselineStateSchemaVersion || state.Generation == 0 {
		return errors.New("remote baseline state schema or generation is invalid")
	}
	if err := validateBaselineIdentity(state.identity()); err != nil {
		return err
	}
	if err := state.validateImageCacheAuthority(); err != nil {
		return err
	}
	if state.OCIProjectCache == nil {
		return errors.New("remote baseline OCI project cache description is required")
	}
	if err := state.OCIProjectCache.ValidateForBaseline(state.MainTree, state.ToolchainDigest, state.Platform, state.RuntimeImage); err != nil {
		return err
	}
	if !remoteDigestPattern.MatchString(state.BaselineManifestDigest) || !remoteDigestPattern.MatchString(state.GateBinarySHA256) || !remoteDigestPattern.MatchString(state.RuntimeSeedSHA256) ||
		!remoteDigestPattern.MatchString(state.SourceSnapshotManifestDigest) || !remoteDigestPattern.MatchString(state.SourceSnapshotClosureDigest) ||
		state.SourceSnapshotImagePath != cicontract.SourceSnapshotManifestPath {
		return errors.New("remote baseline digest is invalid")
	}
	if !validBaselineTimes(state.CreatedAt, state.AcceptedAt, state.RenewedAt) {
		return errors.New("remote baseline timestamps are invalid")
	}
	return nil
}

func (state BaselineState) identity() BaselineIdentity {
	return BaselineIdentity{state.MainCommit, state.MainTree, state.Platform, state.PolicyDigest, state.ToolchainDigest, state.RuntimeImage}
}

// Matches checks the immutable identity of a valid OCI-only state.
func (state BaselineState) Matches(identity BaselineIdentity) bool {
	return state.Validate() == nil && validateBaselineIdentity(identity) == nil && state.identity() == identity
}

// validateImageCacheAuthority rejects a state whose ECI image cache cannot be
// selected deterministically by a consumer. Ready is persisted only after the
// ECI control-plane result is verified against the immutable runtime image.
func (state BaselineState) validateImageCacheAuthority() error {
	if !validImageCacheIdentifier(state.ImageCacheID) || !validImageCacheIdentifier(state.ImageCacheSnapshotID) || !state.ImageCacheReady {
		return errors.New("remote baseline ECI image cache authority is invalid")
	}
	if state.ImageDigest != remoteRuntimeImageDigest(state.RuntimeImage) {
		return errors.New("remote baseline ECI image cache digest does not match runtime image")
	}
	return nil
}

func validImageCacheIdentifier(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n\t /\\?#")
}

func remoteRuntimeImageDigest(value string) string {
	_, digest, ok := strings.Cut(value, "@")
	if !ok {
		return ""
	}
	return digest
}

func validBaselineTimes(createdAt, acceptedAt, renewedAt time.Time) bool {
	return !createdAt.IsZero() && !acceptedAt.IsZero() && !renewedAt.IsZero() &&
		createdAt.Location() == time.UTC && acceptedAt.Location() == time.UTC && renewedAt.Location() == time.UTC &&
		!acceptedAt.Before(createdAt) && !renewedAt.Before(acceptedAt)
}

func validateBaselineIdentity(identity BaselineIdentity) error {
	if !baselineOIDPattern.MatchString(identity.MainCommit) || !baselineOIDPattern.MatchString(identity.MainTree) {
		return errors.New("remote baseline Git identity is invalid")
	}
	if err := cicontract.ValidateTargetPlatform(identity.Platform); err != nil {
		return err
	}
	for name, value := range map[string]string{"policy": identity.PolicyDigest, "toolchain": identity.ToolchainDigest} {
		if !remoteDigestPattern.MatchString(value) {
			return fmt.Errorf("remote baseline %s digest is invalid", name)
		}
	}
	if !validRemoteImageReference(identity.RuntimeImage) {
		return errors.New("remote baseline runtime image must use an immutable digest")
	}
	return nil
}

func validRemoteImageReference(value string) bool {
	repository, digest, ok := strings.Cut(value, "@")
	if !ok || strings.Contains(digest, "@") || !remoteDigestPattern.MatchString(digest) || repository == "" || repository != strings.ToLower(repository) || strings.ContainsAny(repository, " \t\r\n\\?#") || strings.Contains(repository, "://") {
		return false
	}
	if cicontract.ValidateNonACRRegistryHost(repository) != nil {
		return false
	}
	last := repository
	if slash := strings.LastIndexByte(repository, '/'); slash >= 0 {
		last = repository[slash+1:]
	}
	return last != "" && !strings.Contains(last, ":")
}

var baselineOIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
