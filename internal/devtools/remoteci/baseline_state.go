package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	BaselineStateSchemaVersion         uint32 = 8
	BaselineSourceHistorySchemaVersion uint32 = 1
	BaselineStatePreviousSchemaVersion uint32 = 7
	baselineStateV6SchemaVersion       uint32 = 6
	baselineStateLegacySchemaVersion   uint32 = 5
)

const BaselineCacheKindAnchor = "anchor"

// BaselineCacheRef identifies the one stable DataCache anchor mounted by workers.
type BaselineCacheRef struct {
	Generation         uint64    `json:"generation"`
	Kind               string    `json:"kind"`
	ManifestDigest     string    `json:"manifest_digest"`
	MainCommit         string    `json:"main_commit"`
	MainTree           string    `json:"main_tree"`
	DataCacheID        string    `json:"data_cache_id"`
	DataCacheBucket    string    `json:"data_cache_bucket"`
	DataCachePath      string    `json:"data_cache_path"`
	SizeGiB            int       `json:"size_gib"`
	SourceObjectPrefix string    `json:"source_object_prefix"`
	AcceptedAt         time.Time `json:"accepted_at"`
}

// BaselineDeltaRef identifies one digest-verified OSS delta applied after the anchor.
type BaselineDeltaRef struct {
	Generation         uint64    `json:"generation"`
	SourceObjectPrefix string    `json:"source_object_prefix"`
	ManifestDigest     string    `json:"manifest_digest"`
	BaseCommit         string    `json:"base_commit"`
	BaseTree           string    `json:"base_tree"`
	MainCommit         string    `json:"main_commit"`
	MainTree           string    `json:"main_tree"`
	AcceptedAt         time.Time `json:"accepted_at"`
}

// DirectCacheLayerRef 绑定一个不可变 sidecar 直读缓存层的 DataCache、父链和运行时种子输入。
// Layers 始终按 newest-first 排序，worker 只能按这一顺序查询缓存。
type DirectCacheLayerRef struct {
	DataCacheID        string `json:"data_cache_id"`
	DataCacheBucket    string `json:"data_cache_bucket"`
	DataCachePath      string `json:"data_cache_path"`
	SizeGiB            int    `json:"size_gib"`
	Generation         uint64 `json:"generation"`
	SourceObjectPrefix string `json:"source_object_prefix"`
	ManifestDigest     string `json:"manifest_digest"`
	TreeSHA256         string `json:"tree_sha256"`
	ParentChainSHA256  string `json:"parent_chain_sha256"`
	RuntimeGoSHA256    string `json:"runtime_go_sha256"`
	RuntimeDepsSHA256  string `json:"runtime_deps_sha256"`
}

// DirectCacheRef 是有界、有序的直读缓存层集合。旧的单层字段仅用于 v7 解码，禁止新状态写入。
type DirectCacheRef struct {
	Layers []DirectCacheLayerRef `json:"layers"`

	DataCacheID        string `json:"-"`
	DataCacheBucket    string `json:"-"`
	DataCachePath      string `json:"-"`
	SizeGiB            int    `json:"-"`
	Generation         uint64 `json:"-"`
	SourceObjectPrefix string `json:"-"`
	ManifestDigest     string `json:"-"`
	TreeSHA256         string `json:"-"`
	ParentChainSHA256  string `json:"-"`
	RuntimeGoSHA256    string `json:"-"`
	RuntimeDepsSHA256  string `json:"-"`
}

// BaselineState is the accepted remote CI identity with one Anchor and bounded OSS deltas.
type BaselineState struct {
	SchemaVersion          uint32             `json:"schema_version"`
	Generation             uint64             `json:"generation"`
	MainCommit             string             `json:"main_commit"`
	MainTree               string             `json:"main_tree"`
	Platform               string             `json:"platform"`
	PolicyDigest           string             `json:"policy_digest"`
	ToolchainDigest        string             `json:"toolchain_digest"`
	RuntimeImage           string             `json:"runtime_image"`
	GateBinarySHA256       string             `json:"gate_binary_sha256"`
	RuntimeSeedSHA256      string             `json:"runtime_seed_manifest_sha256"`
	BaselineManifestDigest string             `json:"baseline_manifest_digest"`
	SourceHistoryVersion   uint32             `json:"source_history_version"`
	DataCacheID            string             `json:"data_cache_id"`
	DataCacheBucket        string             `json:"data_cache_bucket"`
	DataCachePath          string             `json:"data_cache_path"`
	DataCacheSizeGiB       int                `json:"data_cache_size_gib"`
	SourceObjectPrefix     string             `json:"source_object_prefix"`
	CreatedAt              time.Time          `json:"created_at"`
	AcceptedAt             time.Time          `json:"accepted_at"`
	Anchor                 BaselineCacheRef   `json:"anchor"`
	Deltas                 []BaselineDeltaRef `json:"deltas,omitempty"`
	DirectCacheRef         *DirectCacheRef    `json:"direct_cache_ref,omitempty"`
	RetiredDirectCacheRef  *DirectCacheRef    `json:"retired_direct_cache_ref,omitempty"`
	PreviousAnchor         *BaselineCacheRef  `json:"previous_anchor,omitempty"`
	RetiredAnchor          *BaselineCacheRef  `json:"retired_anchor,omitempty"`
	PreviousDeltas         []BaselineDeltaRef `json:"previous_deltas,omitempty"`
	RetiredDeltas          []BaselineDeltaRef `json:"retired_deltas,omitempty"`
}

type baselineStateV4Wire struct {
	SchemaVersion          uint32         `json:"schema_version"`
	Generation             uint64         `json:"generation"`
	MainCommit             string         `json:"main_commit"`
	MainTree               string         `json:"main_tree"`
	Platform               string         `json:"platform"`
	PolicyDigest           string         `json:"policy_digest"`
	ToolchainDigest        string         `json:"toolchain_digest"`
	RuntimeImage           string         `json:"runtime_image"`
	GateBinarySHA256       string         `json:"gate_binary_sha256"`
	RuntimeSeedSHA256      string         `json:"runtime_seed_manifest_sha256"`
	BaselineManifestDigest string         `json:"baseline_manifest_digest"`
	DataCacheID            string         `json:"data_cache_id"`
	DataCacheBucket        string         `json:"data_cache_bucket"`
	DataCachePath          string         `json:"data_cache_path"`
	DataCacheSizeGiB       int            `json:"data_cache_size_gib"`
	SourceObjectPrefix     string         `json:"source_object_prefix"`
	CreatedAt              time.Time      `json:"created_at"`
	AcceptedAt             time.Time      `json:"accepted_at"`
	Previous               *baselineV4Ref `json:"previous,omitempty"`
	Retired                *baselineV4Ref `json:"retired,omitempty"`
}

type baselineV4Ref struct {
	Generation         uint64    `json:"generation"`
	DataCacheID        string    `json:"data_cache_id"`
	DataCacheBucket    string    `json:"data_cache_bucket"`
	DataCachePath      string    `json:"data_cache_path"`
	SourceObjectPrefix string    `json:"source_object_prefix"`
	AcceptedAt         time.Time `json:"accepted_at"`
}

type legacyDirectCacheRefWire DirectCacheLayerRef

func (reference legacyDirectCacheRefWire) layer() DirectCacheLayerRef {
	return DirectCacheLayerRef(reference)
}

// UnmarshalJSON 严格解码 v8 状态并在内存中迁移 v4-v7 状态。
func (state *BaselineState) UnmarshalJSON(data []byte) error {
	var header struct {
		SchemaVersion uint32 `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return err
	}
	switch header.SchemaVersion {
	case BaselineStateSchemaVersion:
		return state.decodeCurrentBaselineState(data)
	case BaselineStatePreviousSchemaVersion:
		return state.decodePreviousBaselineState(data)
	case baselineStateV6SchemaVersion:
		return state.decodeV6BaselineState(data)
	case baselineStateLegacySchemaVersion:
		return state.decodeLegacyBaselineState(data)
	case 4:
		return state.decodeV4BaselineState(data)
	default:
		return errors.New("remote baseline state schema is invalid")
	}
}

// decodeCurrentBaselineState 解码当前状态并拒绝来源历史协议漂移。
func (state *BaselineState) decodeCurrentBaselineState(data []byte) error {
	type stateWire BaselineState
	var wire stateWire
	if err := decodeSingleJSON(data, &wire); err != nil {
		return err
	}
	if wire.SourceHistoryVersion > BaselineSourceHistorySchemaVersion {
		return errors.New("remote baseline source history version is invalid")
	}
	*state = BaselineState(wire)
	return nil
}

// decodePreviousBaselineState 将 v7 单层直读缓存迁移为一层有序集合。
func (state *BaselineState) decodePreviousBaselineState(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	directData := fields["direct_cache_ref"]
	retiredData := fields["retired_direct_cache_ref"]
	delete(fields, "direct_cache_ref")
	delete(fields, "retired_direct_cache_ref")
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	type stateWire BaselineState
	var wire stateWire
	if err := decodeSingleJSON(data, &wire); err != nil {
		return err
	}
	*state = BaselineState(wire)
	state.SchemaVersion = BaselineStateSchemaVersion
	if directData != nil {
		var reference legacyDirectCacheRefWire
		if err := decodeSingleJSON(directData, &reference); err != nil {
			return err
		}
		state.DirectCacheRef = &DirectCacheRef{Layers: []DirectCacheLayerRef{reference.layer()}}
	}
	if retiredData != nil {
		var reference legacyDirectCacheRefWire
		if err := decodeSingleJSON(retiredData, &reference); err != nil {
			return err
		}
		state.RetiredDirectCacheRef = &DirectCacheRef{Layers: []DirectCacheLayerRef{reference.layer()}}
	}
	return nil
}

// decodeV6BaselineState 迁移 v6 状态，保留需要重建完整历史的哨兵值。
func (state *BaselineState) decodeV6BaselineState(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["direct_cache_ref"]; ok {
		return errors.New("remote v6 baseline state contains a v7 direct cache reference")
	}
	if _, ok := fields["retired_direct_cache_ref"]; ok {
		return errors.New("remote v6 baseline state contains a v7 retired direct cache reference")
	}
	type stateWire BaselineState
	var wire stateWire
	if err := decodeSingleJSON(data, &wire); err != nil {
		return err
	}
	*state = BaselineState(wire)
	state.SchemaVersion = BaselineStateSchemaVersion
	return nil
}

// decodeLegacyBaselineState 迁移 v5 状态并保留需要重建完整历史的哨兵值。
func (state *BaselineState) decodeLegacyBaselineState(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["source_history_version"]; ok {
		return errors.New("remote v5 baseline state contains a v6 source history version")
	}
	if _, ok := fields["direct_cache_ref"]; ok {
		return errors.New("remote v5 baseline state contains a v7 direct cache reference")
	}
	if _, ok := fields["retired_direct_cache_ref"]; ok {
		return errors.New("remote v5 baseline state contains a v7 retired direct cache reference")
	}
	type stateWire BaselineState
	var wire stateWire
	if err := decodeSingleJSON(data, &wire); err != nil {
		return err
	}
	*state = BaselineState(wire)
	state.SchemaVersion = BaselineStateSchemaVersion
	state.SourceHistoryVersion = 0
	return nil
}

// decodeV4BaselineState 将 v4 线性状态迁移到当前链式状态。
func (state *BaselineState) decodeV4BaselineState(data []byte) error {
	var wire baselineStateV4Wire
	if err := decodeSingleJSON(data, &wire); err != nil {
		return err
	}
	*state = migrateBaselineStateV4(wire)
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

func migrateBaselineStateV4(wire baselineStateV4Wire) BaselineState {
	state := BaselineState{SchemaVersion: BaselineStateSchemaVersion, Generation: wire.Generation, MainCommit: wire.MainCommit, MainTree: wire.MainTree, Platform: wire.Platform, PolicyDigest: wire.PolicyDigest, ToolchainDigest: wire.ToolchainDigest, RuntimeImage: wire.RuntimeImage, GateBinarySHA256: wire.GateBinarySHA256, RuntimeSeedSHA256: wire.RuntimeSeedSHA256, BaselineManifestDigest: wire.BaselineManifestDigest, DataCacheID: wire.DataCacheID, DataCacheBucket: wire.DataCacheBucket, DataCachePath: wire.DataCachePath, DataCacheSizeGiB: wire.DataCacheSizeGiB, SourceObjectPrefix: wire.SourceObjectPrefix, CreatedAt: wire.CreatedAt, AcceptedAt: wire.AcceptedAt}
	state.Anchor = state.topAnchor()
	if wire.Previous != nil {
		previous := migrateBaselineV4Ref(*wire.Previous, wire.DataCacheSizeGiB)
		state.PreviousAnchor = &previous
	}
	if wire.Retired != nil {
		retired := migrateBaselineV4Ref(*wire.Retired, wire.DataCacheSizeGiB)
		state.RetiredAnchor = &retired
	}
	return state
}

func migrateBaselineV4Ref(reference baselineV4Ref, sizeGiB int) BaselineCacheRef {
	return BaselineCacheRef{Generation: reference.Generation, Kind: BaselineCacheKindAnchor, DataCacheID: reference.DataCacheID, DataCacheBucket: reference.DataCacheBucket, DataCachePath: reference.DataCachePath, SizeGiB: sizeGiB, SourceObjectPrefix: reference.SourceObjectPrefix, AcceptedAt: reference.AcceptedAt}
}

// BaselineIdentity contains all inputs whose change requires a new baseline generation.
type BaselineIdentity struct{ MainCommit, MainTree, Platform, PolicyDigest, ToolchainDigest, RuntimeImage string }

// Validate 拒绝不完整、可变或不连续的 anchor 与 delta 状态。
func (state BaselineState) Validate() error {
	if err := state.validateTopLevel(); err != nil {
		return err
	}
	if err := state.validateAnchors(); err != nil {
		return err
	}
	if err := state.validateChains(); err != nil {
		return err
	}
	if err := state.validateDirectCacheRef(); err != nil {
		return err
	}
	if state.RetiredDirectCacheRef != nil {
		if err := state.RetiredDirectCacheRef.validateRetired(state.DataCacheBucket); err != nil {
			return err
		}
	}
	return state.validateCurrentChain()
}

// validateTopLevel 校验状态自身的版本、身份、摘要和 DataCache 字段。
func (state BaselineState) validateTopLevel() error {
	if state.SchemaVersion != BaselineStateSchemaVersion || state.Generation == 0 {
		return errors.New("remote baseline state schema or generation is invalid")
	}
	if state.SourceHistoryVersion > BaselineSourceHistorySchemaVersion {
		return errors.New("remote baseline source history version is invalid")
	}
	if err := validateBaselineIdentity(state.identity()); err != nil {
		return err
	}
	if !validBaselineDigests(state) {
		return errors.New("remote baseline digest is invalid")
	}
	if !validCurrentCache(state) || !validBaselineTimes(state.CreatedAt, state.AcceptedAt) {
		return errors.New("remote baseline DataCache identity or timestamps are invalid")
	}
	return nil
}

// validBaselineDigests 校验状态保存的三个不可变对象摘要。
func validBaselineDigests(state BaselineState) bool {
	return remoteDigestPattern.MatchString(state.BaselineManifestDigest) && remoteDigestPattern.MatchString(state.GateBinarySHA256) && remoteDigestPattern.MatchString(state.RuntimeSeedSHA256)
}

// validateAnchors 校验当前、历史和退休 anchor 的存储身份。
func (state BaselineState) validateAnchors() error {
	if err := state.Anchor.validate(state.DataCacheBucket, false); err != nil {
		return fmt.Errorf("remote baseline anchor: %w", err)
	}
	if !matchesTopDataCache(state, state.Anchor) {
		return errors.New("remote baseline top-level DataCache does not match anchor")
	}
	if err := validateAnchorReference(state.PreviousAnchor, state.DataCacheBucket); err != nil {
		return fmt.Errorf("remote baseline previous anchor: %w", err)
	}
	if err := validateAnchorReference(state.RetiredAnchor, state.DataCacheBucket); err != nil {
		return fmt.Errorf("remote baseline retired anchor: %w", err)
	}
	if state.retiredAnchorOverlaps() {
		return errors.New("remote baseline retired anchor overlaps live anchor")
	}
	return nil
}

// retiredAnchorOverlaps 判断退休 anchor 是否与活动引用重叠。
func (state BaselineState) retiredAnchorOverlaps() bool {
	return state.RetiredAnchor != nil && (sameAnchor(*state.RetiredAnchor, state.Anchor) || (state.PreviousAnchor != nil && sameAnchor(*state.RetiredAnchor, *state.PreviousAnchor)))
}

// validateChains 校验当前、历史和退休 delta 链。
func (state BaselineState) validateChains() error {
	if err := validateDeltaChain(state.Deltas, state.Anchor.Generation, state.Anchor.MainCommit, state.Anchor.MainTree); err != nil {
		return fmt.Errorf("remote baseline deltas: %w", err)
	}
	if err := validateReferencedDeltaChain(state.PreviousAnchor, state.PreviousDeltas); err != nil {
		return fmt.Errorf("remote baseline previous deltas: %w", err)
	}
	if err := validateReferencedDeltaChain(state.RetiredAnchor, state.RetiredDeltas); err != nil {
		return fmt.Errorf("remote baseline retired deltas: %w", err)
	}
	if overlapsRetiredDeltas(state.RetiredDeltas, state.Deltas, state.PreviousDeltas) {
		return errors.New("remote baseline retired deltas overlap live deltas")
	}
	return nil
}

// validateCurrentChain 校验顶层字段仍指向当前链尖。
func (state BaselineState) validateCurrentChain() error {
	current := state.currentDeltaOrAnchor()
	if state.Generation != current.Generation || state.MainCommit != current.MainCommit || state.MainTree != current.MainTree || state.BaselineManifestDigest != current.ManifestDigest || state.SourceObjectPrefix != current.SourceObjectPrefix {
		return errors.New("remote baseline top-level identity does not match current delta or anchor")
	}
	if previous, ok := baselineChainTip(state.PreviousAnchor, state.PreviousDeltas); ok && previous.Generation >= current.Generation {
		return errors.New("remote baseline previous chain is not older than current chain")
	}
	if retired, ok := baselineChainTip(state.RetiredAnchor, state.RetiredDeltas); ok && retired.Generation >= current.Generation {
		return errors.New("remote baseline retired chain is not older than current chain")
	}
	return nil
}

// validateDirectCacheRef 校验可选直读缓存与当前父链及运行时输入完全绑定。
func (state BaselineState) validateDirectCacheRef() error {
	if state.DirectCacheRef == nil {
		return nil
	}
	return state.DirectCacheRef.validate(state)
}

func validateAnchorReference(reference *BaselineCacheRef, bucket string) error {
	if reference == nil {
		return nil
	}
	return reference.validate(bucket, true)
}

// validateReferencedDeltaChain 校验可选 anchor 所属的 delta 链。
func validateReferencedDeltaChain(anchor *BaselineCacheRef, deltas []BaselineDeltaRef) error {
	if anchor == nil {
		if len(deltas) != 0 {
			return errors.New("delta chain has no anchor")
		}
		return nil
	}
	baseCommit, baseTree := anchor.MainCommit, anchor.MainTree
	if len(deltas) > 0 && (!baselineOIDPattern.MatchString(baseCommit) || !baselineOIDPattern.MatchString(baseTree)) {
		return errors.New("delta chain anchor has no source identity")
	}
	return validateDeltaChain(deltas, anchor.Generation, baseCommit, baseTree)
}

// validateDeltaChain 校验 delta generation 与 Git 身份的连续性。
func validateDeltaChain(deltas []BaselineDeltaRef, minimumGeneration uint64, baseCommit string, baseTree string) error {
	if len(deltas) > 4 {
		return errors.New("delta chain length exceeds four")
	}
	for index, delta := range deltas {
		if !validBaselineDelta(delta, minimumGeneration) {
			return errors.New("delta reference is invalid")
		}
		if index == 0 {
			if !extendsBaselineAnchor(delta, baseCommit, baseTree) {
				return errors.New("first delta does not extend its anchor")
			}
			continue
		}
		previous := deltas[index-1]
		if previous.Generation >= delta.Generation || delta.BaseCommit != previous.MainCommit || delta.BaseTree != previous.MainTree {
			return errors.New("delta generations or source identities are not continuous")
		}
	}
	return nil
}

// extendsBaselineAnchor 校验首个 delta 是否从给定 anchor 继续。
func extendsBaselineAnchor(delta BaselineDeltaRef, commit, tree string) bool {
	return commit == "" || (delta.BaseCommit == commit && delta.BaseTree == tree)
}

// validBaselineDelta 校验 delta 自身的不可变身份和时间字段。
func validBaselineDelta(delta BaselineDeltaRef, minimumGeneration uint64) bool {
	return validBaselineDeltaObjects(delta) && validBaselineDeltaProgress(delta, minimumGeneration)
}

// validBaselineDeltaObjects 校验 delta 的 Git 对象、摘要和对象前缀。
func validBaselineDeltaObjects(delta BaselineDeltaRef) bool {
	return baselineOIDPattern.MatchString(delta.BaseCommit) && baselineOIDPattern.MatchString(delta.BaseTree) && baselineOIDPattern.MatchString(delta.MainCommit) && baselineOIDPattern.MatchString(delta.MainTree) && remoteDigestPattern.MatchString(delta.ManifestDigest) && validSourceObjectPrefix(delta.SourceObjectPrefix)
}

// validBaselineDeltaProgress 校验 delta 的 generation、源差异和 UTC 时间。
func validBaselineDeltaProgress(delta BaselineDeltaRef, minimumGeneration uint64) bool {
	return !delta.AcceptedAt.IsZero() && delta.AcceptedAt.Location() == time.UTC && delta.Generation > minimumGeneration && delta.BaseCommit != delta.MainCommit
}

func baselineChainTip(anchor *BaselineCacheRef, deltas []BaselineDeltaRef) (BaselineDeltaRef, bool) {
	if len(deltas) > 0 {
		return deltas[len(deltas)-1], true
	}
	if anchor == nil {
		return BaselineDeltaRef{}, false
	}
	return BaselineDeltaRef{
		Generation:         anchor.Generation,
		SourceObjectPrefix: anchor.SourceObjectPrefix,
		ManifestDigest:     anchor.ManifestDigest,
		MainCommit:         anchor.MainCommit,
		MainTree:           anchor.MainTree,
		AcceptedAt:         anchor.AcceptedAt,
	}, true
}

func overlapsRetiredDeltas(retired []BaselineDeltaRef, liveSets ...[]BaselineDeltaRef) bool {
	live := make(map[string]struct{})
	for _, deltas := range liveSets {
		for _, delta := range deltas {
			live[deltaRefKey(delta)] = struct{}{}
		}
	}
	for _, delta := range retired {
		if _, exists := live[deltaRefKey(delta)]; exists {
			return true
		}
	}
	return false
}

func deltaRefKey(delta BaselineDeltaRef) string {
	return fmt.Sprintf("%d\x00%s\x00%s", delta.Generation, delta.SourceObjectPrefix, delta.ManifestDigest)
}

func (state BaselineState) currentDeltaOrAnchor() BaselineDeltaRef {
	if len(state.Deltas) > 0 {
		return state.Deltas[len(state.Deltas)-1]
	}
	return BaselineDeltaRef{Generation: state.Anchor.Generation, SourceObjectPrefix: state.Anchor.SourceObjectPrefix, ManifestDigest: state.Anchor.ManifestDigest, MainCommit: state.Anchor.MainCommit, MainTree: state.Anchor.MainTree, AcceptedAt: state.Anchor.AcceptedAt}
}

// CurrentBaselineParentChainDigest 返回当前 Anchor 和有序 Delta 链的确定性父链摘要。
func CurrentBaselineParentChainDigest(state BaselineState) (string, error) {
	anchor := baselineParentChainAnchorIdentity{Generation: state.Anchor.Generation, ManifestDigest: state.Anchor.ManifestDigest, MainCommit: state.Anchor.MainCommit, MainTree: state.Anchor.MainTree}
	deltas := make([]baselineParentChainDeltaIdentity, 0, len(state.DeltaRefs()))
	for _, delta := range state.DeltaRefs() {
		deltas = append(deltas, baselineParentChainDeltaIdentity{Generation: delta.Generation, ManifestDigest: delta.ManifestDigest, BaseCommit: delta.BaseCommit, BaseTree: delta.BaseTree, MainCommit: delta.MainCommit, MainTree: delta.MainTree})
	}
	return baselineParentChainIdentityDigest(anchor, deltas)
}

type baselineParentChainAnchorIdentity struct {
	Generation     uint64 `json:"generation"`
	ManifestDigest string `json:"manifest_digest"`
	MainCommit     string `json:"main_commit"`
	MainTree       string `json:"main_tree"`
}

type baselineParentChainDeltaIdentity struct {
	Generation     uint64 `json:"generation"`
	ManifestDigest string `json:"manifest_digest"`
	BaseCommit     string `json:"base_commit"`
	BaseTree       string `json:"base_tree"`
	MainCommit     string `json:"main_commit"`
	MainTree       string `json:"main_tree"`
}

func baselineParentChainIdentityDigest(anchor baselineParentChainAnchorIdentity, deltas []baselineParentChainDeltaIdentity) (string, error) {
	chain := struct {
		Version uint32                             `json:"version"`
		Anchor  baselineParentChainAnchorIdentity  `json:"anchor"`
		Deltas  []baselineParentChainDeltaIdentity `json:"deltas"`
	}{Version: 1, Anchor: anchor, Deltas: deltas}
	encoded, err := json.Marshal(chain)
	if err != nil {
		return "", fmt.Errorf("marshal remote baseline parent chain: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// CurrentBaselineParentChainDigest 返回当前状态的确定性 Anchor-Delta 父链摘要。
func (state BaselineState) CurrentBaselineParentChainDigest() (string, error) {
	return CurrentBaselineParentChainDigest(state)
}

func (state BaselineState) topAnchor() BaselineCacheRef {
	return BaselineCacheRef{Generation: state.Generation, Kind: BaselineCacheKindAnchor, ManifestDigest: state.BaselineManifestDigest, MainCommit: state.MainCommit, MainTree: state.MainTree, DataCacheID: state.DataCacheID, DataCacheBucket: state.DataCacheBucket, DataCachePath: state.DataCachePath, SizeGiB: state.DataCacheSizeGiB, SourceObjectPrefix: state.SourceObjectPrefix, AcceptedAt: state.AcceptedAt}
}

// CurrentAnchorRef 返回当前已挂载的唯一 DataCache anchor。
func (state BaselineState) CurrentAnchorRef() BaselineCacheRef { return state.Anchor }

// DeltaRefs 返回按从旧到新排序的 OSS delta 链副本。
func (state BaselineState) DeltaRefs() []BaselineDeltaRef {
	return append([]BaselineDeltaRef(nil), state.Deltas...)
}

// HasRetiredReferences 报告刷新是否必须先完成远端清理。
func (state BaselineState) HasRetiredReferences() bool {
	return state.RetiredAnchor != nil || state.RetiredDirectCacheRef != nil || len(state.RetiredDeltas) != 0
}

func (state BaselineState) identity() BaselineIdentity {
	return BaselineIdentity{state.MainCommit, state.MainTree, state.Platform, state.PolicyDigest, state.ToolchainDigest, state.RuntimeImage}
}

// Matches 校验状态是否匹配给定的不可变身份。
func (state BaselineState) Matches(identity BaselineIdentity) bool {
	return state.Validate() == nil && validateBaselineIdentity(identity) == nil && state.identity() == identity
}

// validate 校验 anchor 引用的对象、存储和时间身份。
func (reference BaselineCacheRef) validate(bucket string, allowMissingManifestDigest bool) error {
	hasSourceIdentity := baselineOIDPattern.MatchString(reference.MainCommit) && baselineOIDPattern.MatchString(reference.MainTree)
	if !reference.validIdentity(hasSourceIdentity, allowMissingManifestDigest) || !reference.validStorage(bucket) {
		return errors.New("DataCache anchor is invalid")
	}
	return nil
}

// validate 校验直读缓存层集合的顺序、存储身份、父链和运行时种子绑定。
func (reference DirectCacheRef) validate(state BaselineState) error {
	parentChainDigest, err := CurrentBaselineParentChainDigest(state)
	if err != nil {
		return err
	}
	return reference.validateLayers(state.DataCacheBucket, state.Generation, state.RuntimeSeedSHA256, parentChainDigest)
}

func (reference DirectCacheRef) validateRetired(bucket string) error {
	return reference.validateLayers(bucket, 0, "", "")
}

func (reference DirectCacheRef) validateLayers(bucket string, tipGeneration uint64, runtimeGoDigest string, parentChainDigest string) error {
	if len(reference.Layers) == 0 || len(reference.Layers) > 3 {
		return errors.New("direct cache layer count is invalid")
	}
	seen := make(map[string]struct{}, len(reference.Layers))
	for index, layer := range reference.Layers {
		if err := layer.validateIdentity(bucket); err != nil {
			return err
		}
		if runtimeGoDigest != "" && layer.RuntimeGoSHA256 != runtimeGoDigest {
			return errors.New("direct cache layer runtime Go digest does not match current baseline")
		}
		if index == 0 {
			if tipGeneration != 0 && (layer.Generation != tipGeneration || layer.ParentChainSHA256 != parentChainDigest) {
				return errors.New("newest direct cache layer does not match current baseline chain")
			}
		} else if reference.Layers[index-1].Generation <= layer.Generation {
			return errors.New("direct cache layers are not newest-first")
		}
		identity := layer.DataCacheID + "\x00" + layer.DataCacheBucket + "\x00" + layer.DataCachePath
		if _, duplicate := seen[identity]; duplicate {
			return errors.New("direct cache layer identity is duplicated")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func (reference DirectCacheLayerRef) validateIdentity(bucket string) error {
	if !dataCacheIDPattern.MatchString(reference.DataCacheID) ||
		!dataCacheBucketPattern.MatchString(reference.DataCacheBucket) ||
		reference.DataCacheBucket == "eci-system" ||
		reference.DataCacheBucket != bucket ||
		!validBaselinePath(reference.DataCachePath) ||
		reference.SizeGiB <= 0 ||
		!validSourceObjectPrefix(reference.SourceObjectPrefix) || !reference.hasCanonicalLocation() ||
		!remoteDigestPattern.MatchString(reference.ManifestDigest) ||
		!remoteDigestPattern.MatchString(reference.TreeSHA256) ||
		!remoteDigestPattern.MatchString(reference.ParentChainSHA256) ||
		!remoteDigestPattern.MatchString(reference.RuntimeGoSHA256) ||
		!remoteDigestPattern.MatchString(reference.RuntimeDepsSHA256) {
		return errors.New("direct cache reference is invalid")
	}
	return nil
}

func (reference DirectCacheLayerRef) hasCanonicalLocation() bool {
	generation := strconv.FormatUint(reference.Generation, 10)
	return reference.Generation > 0 &&
		strings.HasSuffix(reference.DataCachePath, "/direct-cache/"+generation) &&
		strings.HasSuffix(reference.SourceObjectPrefix, "/"+generation+"/output/direct-cache/")
}

// validIdentity 校验 anchor 的 generation、种类、摘要和可选 Git 身份。
func (reference BaselineCacheRef) validIdentity(hasSourceIdentity, allowMissingManifestDigest bool) bool {
	if reference.Generation == 0 || reference.Kind != BaselineCacheKindAnchor {
		return false
	}
	if !allowMissingManifestDigest && (!remoteDigestPattern.MatchString(reference.ManifestDigest) || !hasSourceIdentity) {
		return false
	}
	return (reference.ManifestDigest == "" || remoteDigestPattern.MatchString(reference.ManifestDigest)) && ((reference.MainCommit == "" && reference.MainTree == "") || hasSourceIdentity)
}

// validStorage 校验 anchor 的 DataCache、对象前缀和 UTC 接收时间。
func (reference BaselineCacheRef) validStorage(bucket string) bool {
	return reference.DataCacheBucket == bucket && dataCacheIDPattern.MatchString(reference.DataCacheID) && reference.DataCacheBucket != "eci-system" && validBaselinePath(reference.DataCachePath) && reference.SizeGiB > 0 && validSourceObjectPrefix(reference.SourceObjectPrefix) && !reference.AcceptedAt.IsZero() && reference.AcceptedAt.Location() == time.UTC
}

func sameAnchor(left, right BaselineCacheRef) bool {
	return left.DataCacheID == right.DataCacheID && left.DataCacheBucket == right.DataCacheBucket && left.DataCachePath == right.DataCachePath
}
func matchesTopDataCache(state BaselineState, anchor BaselineCacheRef) bool {
	return anchor.DataCacheID == state.DataCacheID && anchor.DataCacheBucket == state.DataCacheBucket && anchor.DataCachePath == state.DataCachePath && anchor.SizeGiB == state.DataCacheSizeGiB
}

// validCurrentCache 校验顶层 DataCache 的稳定身份。
func validCurrentCache(state BaselineState) bool {
	return dataCacheIDPattern.MatchString(state.DataCacheID) && dataCacheBucketPattern.MatchString(state.DataCacheBucket) && state.DataCacheBucket != "eci-system" && validBaselinePath(state.DataCachePath) && state.DataCacheSizeGiB > 0 && validSourceObjectPrefix(state.SourceObjectPrefix)
}
func validBaselineTimes(createdAt, acceptedAt time.Time) bool {
	return !createdAt.IsZero() && !acceptedAt.IsZero() && createdAt.Location() == time.UTC && acceptedAt.Location() == time.UTC && !acceptedAt.Before(createdAt)
}

// validateBaselineIdentity 校验创建 generation 所需的全部不可变输入。
func validateBaselineIdentity(identity BaselineIdentity) error {
	if !baselineOIDPattern.MatchString(identity.MainCommit) || !baselineOIDPattern.MatchString(identity.MainTree) {
		return errors.New("remote baseline Git identity is invalid")
	}
	if identity.Platform != "linux/amd64" && identity.Platform != "linux/arm64" {
		return errors.New("remote baseline platform is invalid")
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

// validRemoteImageReference 校验带不可变摘要的远端镜像引用。
func validRemoteImageReference(value string) bool {
	repository, digest, ok := strings.Cut(value, "@")
	if !ok || strings.Contains(digest, "@") || !remoteDigestPattern.MatchString(digest) || repository == "" || repository != strings.ToLower(repository) || strings.ContainsAny(repository, " \t\r\n\\?#") || strings.Contains(repository, "://") {
		return false
	}
	last := repository
	if slash := strings.LastIndexByte(repository, '/'); slash >= 0 {
		last = repository[slash+1:]
	}
	return last != "" && !strings.Contains(last, ":")
}
func validBaselinePath(value string) bool {
	return value != "/" && path.IsAbs(value) && path.Clean(value) == value && len(value) <= 1024 && !strings.ContainsAny(value, ":\x00")
}
func validSourceObjectPrefix(value string) bool {
	return strings.HasPrefix(value, "baseline-artifacts/") && strings.HasSuffix(value, "/") && path.Clean("/"+value) == "/"+strings.TrimSuffix(value, "/") && !strings.ContainsAny(value, "\\\x00\r\n")
}

var (
	baselineOIDPattern     = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	dataCacheIDPattern     = regexp.MustCompile(`^edc-[a-z0-9]+$`)
	dataCacheBucketPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
)
