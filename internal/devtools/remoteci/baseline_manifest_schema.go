package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const BaselineManifestSchemaVersion uint32 = 11

// BaselineManifest is the OCI-only seed output. Archive/layer storage is a
// retired protocol and is deliberately not part of the wire type.
type BaselineManifest struct {
	SchemaVersion             uint32                   `json:"schema_version"`
	Generation                uint64                   `json:"generation"`
	MainCommit                string                   `json:"main_commit"`
	MainTree                  string                   `json:"main_tree"`
	Platform                  string                   `json:"platform"`
	PolicyDigest              string                   `json:"policy_digest"`
	ToolchainDigest           string                   `json:"toolchain_digest"`
	RuntimeImage              string                   `json:"runtime_image"`
	OCIProjectCache           *BaselineOCIProjectCache `json:"oci_project_cache"`
	GateSourceSHA256          string                   `json:"gate_source_sha256"`
	GateBinarySHA256          string                   `json:"gate_binary_sha256"`
	GateBinarySize            int64                    `json:"gate_binary_size"`
	RuntimeSeedManifestSHA256 string                   `json:"runtime_seed_manifest_sha256"`
	RuntimeDependencyDigest   string                   `json:"runtime_dependency_digest"`
	CABundleSHA256            string                   `json:"ca_bundle_sha256"`
	CABundleSize              int64                    `json:"ca_bundle_size"`
}

// DecodeBaselineManifest strictly parses only the OCI-only manifest schema.
func DecodeBaselineManifest(data []byte) (BaselineManifest, error) {
	var header struct {
		SchemaVersion uint32 `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return BaselineManifest{}, fmt.Errorf("decode remote baseline manifest header: %w", err)
	}
	if header.SchemaVersion != BaselineManifestSchemaVersion {
		return BaselineManifest{}, fmt.Errorf("%w: remote baseline manifest schema %d is not OCI-only schema %d", ErrRemoteBaselineMigrationRequired, header.SchemaVersion, BaselineManifestSchemaVersion)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest BaselineManifest
	if err := decoder.Decode(&manifest); err != nil {
		return BaselineManifest{}, fmt.Errorf("decode remote baseline manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return BaselineManifest{}, errors.New("remote baseline manifest contains multiple JSON values")
		}
		return BaselineManifest{}, fmt.Errorf("decode remote baseline manifest trailer: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return BaselineManifest{}, err
	}
	return manifest, nil
}

func (manifest BaselineManifest) Validate() error {
	if manifest.SchemaVersion != BaselineManifestSchemaVersion || manifest.Generation == 0 {
		return errors.New("remote baseline manifest schema or generation is invalid")
	}
	if err := validateBaselineIdentity(BaselineIdentity{MainCommit: manifest.MainCommit, MainTree: manifest.MainTree, Platform: manifest.Platform, PolicyDigest: manifest.PolicyDigest, ToolchainDigest: manifest.ToolchainDigest, RuntimeImage: manifest.RuntimeImage}); err != nil {
		return err
	}
	if manifest.OCIProjectCache == nil {
		return errors.New("remote baseline manifest OCI project cache is required")
	}
	if err := manifest.OCIProjectCache.ValidateForBaseline(manifest.MainTree, manifest.ToolchainDigest, manifest.Platform, manifest.RuntimeImage); err != nil {
		return err
	}
	for name, digest := range map[string]string{"gate source": manifest.GateSourceSHA256, "gate binary": manifest.GateBinarySHA256, "runtime seed manifest": manifest.RuntimeSeedManifestSHA256, "runtime dependency": manifest.RuntimeDependencyDigest, "CA bundle": manifest.CABundleSHA256} {
		if !remoteDigestPattern.MatchString(digest) {
			return fmt.Errorf("remote baseline manifest %s digest is invalid", name)
		}
	}
	if manifest.GateBinarySize <= 0 || manifest.CABundleSize <= 0 {
		return errors.New("remote baseline manifest artifact size is invalid")
	}
	return nil
}

func (manifest BaselineManifest) Matches(generation uint64, identity BaselineIdentity) bool {
	return manifest.Validate() == nil && validateBaselineIdentity(identity) == nil && manifest.Generation == generation && manifest.MainCommit == identity.MainCommit && manifest.MainTree == identity.MainTree && manifest.Platform == identity.Platform && manifest.PolicyDigest == identity.PolicyDigest && manifest.ToolchainDigest == identity.ToolchainDigest && manifest.RuntimeImage == identity.RuntimeImage
}

func BaselineManifestDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}
