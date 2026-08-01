package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	BaselineManifestSchemaVersion            uint32 = 10
	BaselineManifestMinimumCompatibleVersion uint32 = 6
	legacyBaselineManifestSchemaVersion             = BaselineManifestMinimumCompatibleVersion
	layeredBaselineManifestSchemaVersion     uint32 = 7
	anchorDeltaBaselineManifestSchemaVersion uint32 = 8
	BaselineStorageModeAnchor                       = "anchor"
	BaselineStorageModeDelta                        = "delta"
	BaselineLayerKindAnchor                         = "anchor"
	BaselineLayerKindDelta                          = "delta"
)

// BaselineLayer binds one deterministic archive to a cache generation.
type BaselineLayer struct {
	Generation                    uint64 `json:"generation,omitempty"`
	Kind                          string `json:"kind,omitempty"`
	Name                          string `json:"name"`
	Archive                       string `json:"archive"`
	SHA256                        string `json:"sha256"`
	Size                          int64  `json:"size"`
	BaseCommit                    string `json:"base_commit,omitempty"`
	BaseTree                      string `json:"base_tree,omitempty"`
	TargetCommit                  string `json:"target_commit,omitempty"`
	TargetTree                    string `json:"target_tree,omitempty"`
	BaseRuntimeDependencyDigest   string `json:"base_runtime_dependency_digest,omitempty"`
	TargetRuntimeDependencyDigest string `json:"target_runtime_dependency_digest,omitempty"`
}

// BaselineManifest is emitted by the remote seed after immutable artifacts exist.
type BaselineManifest struct {
	SchemaVersion             uint32          `json:"schema_version"`
	Generation                uint64          `json:"generation"`
	MainCommit                string          `json:"main_commit"`
	MainTree                  string          `json:"main_tree"`
	Platform                  string          `json:"platform"`
	PolicyDigest              string          `json:"policy_digest"`
	ToolchainDigest           string          `json:"toolchain_digest"`
	RuntimeImage              string          `json:"runtime_image"`
	GateSourceSHA256          string          `json:"gate_source_sha256,omitempty"`
	GateBinarySHA256          string          `json:"gate_binary_sha256"`
	GateBinarySize            int64           `json:"gate_binary_size"`
	RuntimeSeedManifestSHA256 string          `json:"runtime_seed_manifest_sha256"`
	RuntimeDependencyDigest   string          `json:"runtime_dependency_digest,omitempty"`
	CABundleSHA256            string          `json:"ca_bundle_sha256"`
	CABundleSize              int64           `json:"ca_bundle_size"`
	StorageMode               string          `json:"storage_mode,omitempty"`
	Layers                    []BaselineLayer `json:"layers,omitempty"`
	ArchiveSHA256             string          `json:"archive_sha256,omitempty"`
	ArchiveSize               int64           `json:"archive_size,omitempty"`
}

// DecodeBaselineManifest 严格解析 manifest 并拒绝尾随 JSON 数据。
func DecodeBaselineManifest(data []byte) (BaselineManifest, error) {
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

// Validate 拒绝不完整或格式错误的 manifest 产物。
func (manifest BaselineManifest) Validate() error {
	if manifest.Generation == 0 {
		return errors.New("remote baseline manifest schema or generation is invalid")
	}
	if err := validateBaselineIdentity(BaselineIdentity{MainCommit: manifest.MainCommit, MainTree: manifest.MainTree, Platform: manifest.Platform, PolicyDigest: manifest.PolicyDigest, ToolchainDigest: manifest.ToolchainDigest, RuntimeImage: manifest.RuntimeImage}); err != nil {
		return err
	}
	for name, digest := range map[string]string{"gate binary": manifest.GateBinarySHA256, "runtime seed manifest": manifest.RuntimeSeedManifestSHA256, "CA bundle": manifest.CABundleSHA256} {
		if !remoteDigestPattern.MatchString(digest) {
			return fmt.Errorf("remote baseline manifest %s digest is invalid", name)
		}
	}
	if manifest.GateBinarySize <= 0 || manifest.CABundleSize <= 0 {
		return errors.New("remote baseline manifest artifact size is invalid")
	}
	return validateBaselineManifestLayers(manifest)
}

// validateBaselineManifestLayers 按 schema 版本校验层存储契约。
func validateBaselineManifestLayers(manifest BaselineManifest) error {
	switch manifest.SchemaVersion {
	case legacyBaselineManifestSchemaVersion:
		return validateLegacyBaselineManifest(manifest)
	case layeredBaselineManifestSchemaVersion:
		return validateV7BaselineManifest(manifest)
	case anchorDeltaBaselineManifestSchemaVersion:
		if manifest.GateSourceSHA256 != "" {
			return errors.New("remote v8 baseline contains a v9 gate source digest")
		}
		return validateAnchorDeltaBaselineManifest(manifest, "v8")
	case 9:
		if !remoteDigestPattern.MatchString(manifest.GateSourceSHA256) {
			return errors.New("remote v9 baseline gate source digest is invalid")
		}
		return validateAnchorDeltaBaselineManifest(manifest, "v9")
	case BaselineManifestSchemaVersion:
		if !remoteDigestPattern.MatchString(manifest.GateSourceSHA256) || !remoteDigestPattern.MatchString(manifest.RuntimeDependencyDigest) {
			return errors.New("remote v10 baseline runtime dependency or gate source digest is invalid")
		}
		return validateAnchorDeltaBaselineManifest(manifest, "v10")
	default:
		return errors.New("remote baseline manifest schema or generation is invalid")
	}
}

// validateLegacyBaselineManifest 校验 v6 单归档 manifest。
func validateLegacyBaselineManifest(manifest BaselineManifest) error {
	if manifest.GateSourceSHA256 != "" || manifest.StorageMode != "" || len(manifest.Layers) != 0 || !remoteDigestPattern.MatchString(manifest.ArchiveSHA256) || manifest.ArchiveSize <= 0 {
		return errors.New("remote legacy baseline archive is invalid")
	}
	return nil
}

func validateV7BaselineManifest(manifest BaselineManifest) error {
	if manifest.GateSourceSHA256 != "" || manifest.StorageMode != "" || manifest.ArchiveSHA256 != "" || manifest.ArchiveSize != 0 {
		return errors.New("remote v7 baseline contains incompatible fields")
	}
	return validateLayerSet(manifest.Layers, []layerContract{{"runtime-deps", "runtime-deps.tar.gz", ""}, {"source", "source.tar.gz", ""}, {"go-build-cache", "go-build-cache.tar.gz", ""}}, 0, "")
}

// validateAnchorDeltaBaselineManifest 校验 anchor 或 delta 层的 manifest。
func validateAnchorDeltaBaselineManifest(manifest BaselineManifest, version string) error {
	if manifest.ArchiveSHA256 != "" || manifest.ArchiveSize != 0 {
		return fmt.Errorf("remote %s baseline contains legacy archive fields", version)
	}
	switch manifest.StorageMode {
	case BaselineStorageModeAnchor:
		return validateLayerSet(manifest.Layers, []layerContract{{"runtime-deps", "runtime-deps.tar.gz", BaselineLayerKindAnchor}, {"source", "source.tar.gz", BaselineLayerKindAnchor}, {"go-build-cache", "go-build-cache.tar.gz", BaselineLayerKindAnchor}}, manifest.Generation, BaselineStorageModeAnchor)
	case BaselineStorageModeDelta:
		expected := []layerContract{{"source", "source.delta.bundle", BaselineLayerKindDelta}, {"go-build-cache", "go-build-cache.delta.tar.gz", BaselineLayerKindDelta}}
		if len(manifest.Layers) == 3 {
			if manifest.Layers[1].Name == "runtime-go" {
				expected = []layerContract{{"source", "source.delta.bundle", BaselineLayerKindDelta}, {"runtime-go", "runtime-go.delta.tar.gz", BaselineLayerKindDelta}, {"go-build-cache", "go-build-cache.delta.tar.gz", BaselineLayerKindDelta}}
			} else {
				expected = []layerContract{{"source", "source.delta.bundle", BaselineLayerKindDelta}, {"runtime-deps", "runtime-deps.delta.tar.gz", BaselineLayerKindDelta}, {"go-build-cache", "go-build-cache.delta.tar.gz", BaselineLayerKindDelta}}
			}
		}
		if err := validateLayerSet(manifest.Layers, expected, manifest.Generation, BaselineStorageModeDelta); err != nil {
			return err
		}
		if err := validateSourceDelta(manifest, manifest.Layers[0]); err != nil {
			return err
		}
		for _, layer := range manifest.Layers {
			if layer.Name == "runtime-deps" && (!remoteDigestPattern.MatchString(layer.BaseRuntimeDependencyDigest) || !remoteDigestPattern.MatchString(layer.TargetRuntimeDependencyDigest) || layer.BaseRuntimeDependencyDigest == layer.TargetRuntimeDependencyDigest || layer.TargetRuntimeDependencyDigest != manifest.RuntimeDependencyDigest) {
				return errors.New("remote runtime dependency delta digest transition is invalid")
			}
		}
		return nil
	default:
		return fmt.Errorf("remote %s baseline storage mode is invalid", version)
	}
}

type layerContract struct{ name, archive, kind string }

// validateLayerSet 按顺序校验层集合及其存储绑定。
func validateLayerSet(layers []BaselineLayer, expected []layerContract, generation uint64, mode string) error {
	if len(layers) != len(expected) {
		return errors.New("remote baseline layer set is incomplete")
	}
	for index, contract := range expected {
		layer := layers[index]
		if !matchesLayerContract(layer, contract) {
			return fmt.Errorf("remote baseline layer %d is invalid", index)
		}
		if !matchesLayerStorage(layer, contract, generation, mode) {
			return fmt.Errorf("remote baseline layer %d generation or kind is invalid", index)
		}
		if contract.name != "source" && hasLayerSourceChain(layer) {
			return fmt.Errorf("remote baseline layer %d has source chain fields", index)
		}
		if mode == BaselineStorageModeAnchor && hasLayerSourceChain(layer) {
			return fmt.Errorf("remote anchor layer %d has delta source chain fields", index)
		}
	}
	return nil
}

// matchesLayerContract 校验层的固定名称、归档名和摘要容量。
func matchesLayerContract(layer BaselineLayer, contract layerContract) bool {
	return layer.Name == contract.name && layer.Archive == contract.archive &&
		remoteDigestPattern.MatchString(layer.SHA256) && layer.Size > 0
}

// matchesLayerStorage 校验指定存储模式下的 generation 与层种类。
func matchesLayerStorage(layer BaselineLayer, contract layerContract, generation uint64, mode string) bool {
	return mode == "" || (layer.Generation == generation && layer.Kind == contract.kind)
}

// hasLayerSourceChain 报告层是否携带 source delta 专用链字段。
func hasLayerSourceChain(layer BaselineLayer) bool {
	return layer.BaseCommit != "" || layer.BaseTree != "" || layer.TargetCommit != "" || layer.TargetTree != ""
}

// validateSourceDelta 校验 source delta 的 Git 连续性。
func validateSourceDelta(manifest BaselineManifest, layer BaselineLayer) error {
	if !baselineOIDPattern.MatchString(layer.BaseCommit) || !baselineOIDPattern.MatchString(layer.BaseTree) || !baselineOIDPattern.MatchString(layer.TargetCommit) || !baselineOIDPattern.MatchString(layer.TargetTree) || layer.BaseCommit == layer.TargetCommit || layer.TargetCommit != manifest.MainCommit || layer.TargetTree != manifest.MainTree {
		return errors.New("remote source delta commit chain is incomplete")
	}
	return nil
}

// Matches 校验 seed 结果是否匹配精确的刷新输入。
func (manifest BaselineManifest) Matches(generation uint64, identity BaselineIdentity) bool {
	if manifest.Validate() != nil || validateBaselineIdentity(identity) != nil {
		return false
	}
	return manifest.Generation == generation && manifest.MainCommit == identity.MainCommit && manifest.MainTree == identity.MainTree && manifest.Platform == identity.Platform && manifest.PolicyDigest == identity.PolicyDigest && manifest.ToolchainDigest == identity.ToolchainDigest && manifest.RuntimeImage == identity.RuntimeImage
}

// BaselineManifestDigest 返回已持久化 manifest 对象的精确摘要。
func BaselineManifestDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}
