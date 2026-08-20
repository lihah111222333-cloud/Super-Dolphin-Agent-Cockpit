// Package heteroci 提供 V3 与 WJBoot-V2 共享的本地异构 CI 严格清单。
package heteroci

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// ManifestSchemaVersion 是本包拥有的 wire schema 版本。
	ManifestSchemaVersion = "heteroci-manifest/v1"
	// AuthorityLocalNonAuthoritative 标记本地主机证据不具备远程权威性。
	AuthorityLocalNonAuthoritative = "LOCAL_NON_AUTHORITATIVE"

	// 以下常量是协议允许的平台标识。
	PlatformLinuxAMD64   = "linux/amd64"
	PlatformWindowsAMD64 = "windows/amd64"
	PlatformDarwinAMD64  = "darwin/amd64"
	PlatformDarwinARM64  = "darwin/arm64"

	// 以下常量是协议允许的运行时标识。
	RuntimeUbuntuContainer       = "ubuntu_container"
	RuntimeDarlingCompatRuntime  = "darling_compat_runtime"
	RuntimeWindowsNative         = "windows_native"
	RuntimeDarwinNative          = "darwin_native"
	RuntimeDockerOSXExperimental = "docker_osx_experimental"
	CloudProviderAliyunECI       = "aliyun-eci"
)

// Platform 是规范的目标平台标识。
type Platform string

// RuntimeKind 是规范的执行运行时标识。
type RuntimeKind string

// CloudProvider 是规范的云溢出 provider 标识。
type CloudProvider string

// CloudOverflowConfig 描述可选的 Linux/amd64 ECI 溢出通道。
type CloudOverflowConfig struct {
	Enabled  bool          `json:"enabled"`
	Provider CloudProvider `json:"provider"`
	Platform Platform      `json:"platform"`
}

// NodeConfig 描述一个动态声明的本地执行节点。
type NodeConfig struct {
	NodeID        string             `json:"node_id"`
	Endpoint      string             `json:"endpoint"`
	ProbeIdentity string             `json:"probe_identity"`
	Platform      Platform           `json:"platform"`
	Capabilities  []CapabilityConfig `json:"capabilities"`
}

// CapabilityConfig 描述节点上一个已完成资格验证的运行时。
type CapabilityConfig struct {
	Runtime               RuntimeKind `json:"runtime"`
	Image                 string      `json:"image"`
	ImageDigest           string      `json:"image_digest"`
	SourceRevision        string      `json:"source_revision"`
	QualificationSuite    string      `json:"qualification_suite"`
	ExperimentalOptIn     bool        `json:"experimental_opt_in"`
	AppleHardwareAttested bool        `json:"apple_hardware_attested"`
	KVM                   bool        `json:"kvm"`
	CompliantImage        bool        `json:"compliant_image"`
	ImmutableIdentity     string      `json:"immutable_identity"`
}

// Manifest 是严格、本地且不具备远程权威性的异构 CI 清单。
type Manifest struct {
	SchemaVersion string              `json:"schema_version"`
	Authority     string              `json:"authority"`
	CloudOverflow CloudOverflowConfig `json:"cloud_overflow"`
	Nodes         []NodeConfig        `json:"nodes"`
}

// DecodeManifest 从 r 严格解码并校验一个异构 CI 清单。
func DecodeManifest(r io.Reader) (Manifest, error) {
	if r == nil {
		return Manifest{}, errors.New("heterogeneous CI manifest reader is required")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read heterogeneous CI manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return Manifest{}, fmt.Errorf("heterogeneous CI manifest exceeds %d-byte limit", maxManifestBytes)
	}
	if err := validateManifestJSON(data); err != nil {
		return Manifest{}, err
	}
	if err := validateManifestRootFields(data); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode heterogeneous CI manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Validate 校验清单的完整契约。
func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("heterogeneous CI manifest schema_version %q is invalid", manifest.SchemaVersion)
	}
	if manifest.Authority != AuthorityLocalNonAuthoritative {
		return fmt.Errorf("heterogeneous CI manifest authority %q is invalid", manifest.Authority)
	}
	if err := manifest.CloudOverflow.validate(); err != nil {
		return err
	}
	if len(manifest.Nodes) == 0 {
		return errors.New("heterogeneous CI manifest nodes must not be empty")
	}
	if err := validateManifestNodes(manifest.Nodes, manifest.CloudOverflow.Enabled); err != nil {
		return err
	}
	return nil
}

// validateManifestNodes 校验节点集合唯一性并证明本地 Linux/amd64 溢出能力。
func validateManifestNodes(nodes []NodeConfig, cloudOverflowEnabled bool) error {
	seen := make(map[string]struct{}, len(nodes))
	seenEndpoints := make(map[string]struct{}, len(nodes))
	seenProbeIdentities := make(map[string]struct{}, len(nodes))
	hasLocalLinuxUbuntu := false
	for index, node := range nodes {
		if err := node.validate(index); err != nil {
			return err
		}
		if err := validateNodeUniqueness(node, seen, seenEndpoints, seenProbeIdentities); err != nil {
			return err
		}
		hasLocalLinuxUbuntu = hasLocalLinuxUbuntu || nodeHasRuntime(node, RuntimeUbuntuContainer)
	}
	if cloudOverflowEnabled && !hasLocalLinuxUbuntu {
		return errors.New("heterogeneous CI cloud_overflow requires a local linux/amd64 node with ubuntu_container capability")
	}
	return nil
}

// validateNodeUniqueness 校验节点 ID、端点与探针身份均为跨节点唯一值。
func validateNodeUniqueness(node NodeConfig, seen, seenEndpoints, seenProbeIdentities map[string]struct{}) error {
	if _, exists := seen[node.NodeID]; exists {
		return fmt.Errorf("heterogeneous CI manifest node %q is duplicated", node.NodeID)
	}
	seen[node.NodeID] = struct{}{}
	if _, exists := seenEndpoints[node.Endpoint]; exists {
		return fmt.Errorf("heterogeneous CI node endpoint %q is duplicated", node.Endpoint)
	}
	seenEndpoints[node.Endpoint] = struct{}{}
	if _, exists := seenProbeIdentities[node.ProbeIdentity]; exists {
		return fmt.Errorf("heterogeneous CI node %q probe_identity %q is duplicated", node.NodeID, node.ProbeIdentity)
	}
	seenProbeIdentities[node.ProbeIdentity] = struct{}{}
	return nil
}

// nodeHasRuntime 判断节点平台与运行时是否组成指定的有效能力。
func nodeHasRuntime(node NodeConfig, runtime RuntimeKind) bool {
	if node.Platform != PlatformLinuxAMD64 {
		return false
	}
	for _, capability := range node.Capabilities {
		if capability.Runtime == runtime {
			return true
		}
	}
	return false
}

// validate 校验云溢出配置的 one-of 语义。
func (config CloudOverflowConfig) validate() error {
	if !config.Enabled {
		return validateDisabledCloudOverflow(config)
	}
	return validateEnabledCloudOverflow(config)
}

// validateDisabledCloudOverflow 校验关闭状态不携带任何 provider 身份。
func validateDisabledCloudOverflow(config CloudOverflowConfig) error {
	if config.Provider != "" || config.Platform != "" {
		return errors.New("heterogeneous CI cloud_overflow disabled config must not contain provider or platform")
	}
	return nil
}

// validateEnabledCloudOverflow 校验启用状态只能使用规范 ECI 溢出通道。
func validateEnabledCloudOverflow(config CloudOverflowConfig) error {
	if config.Provider != CloudProviderAliyunECI || config.Platform != PlatformLinuxAMD64 {
		return errors.New("heterogeneous CI cloud_overflow must use aliyun-eci on linux/amd64")
	}
	return nil
}

// validate 校验节点身份、平台与动态能力列表。
func (node NodeConfig) validate(index int) error {
	if err := validateNodeIdentity(node, index); err != nil {
		return err
	}
	if !isCanonicalPlatform(node.Platform) {
		return fmt.Errorf("heterogeneous CI node %q platform %q is invalid", node.NodeID, node.Platform)
	}
	return validateNodeCapabilities(node)
}

// validateNodeIdentity 校验节点 ID 与执行端点的稳定非空身份。
func validateNodeIdentity(node NodeConfig, index int) error {
	if strings.TrimSpace(node.NodeID) == "" || node.NodeID != strings.TrimSpace(node.NodeID) || hasControl(node.NodeID) {
		return fmt.Errorf("heterogeneous CI node %d node_id is invalid", index)
	}
	if strings.TrimSpace(node.Endpoint) == "" || node.Endpoint != strings.TrimSpace(node.Endpoint) || hasControl(node.Endpoint) {
		return fmt.Errorf("heterogeneous CI node %q endpoint is invalid", node.NodeID)
	}
	if !isSHA256Digest(node.ProbeIdentity) {
		return fmt.Errorf("heterogeneous CI node %q probe_identity must be an immutable sha256 digest", node.NodeID)
	}
	return nil
}

// validateNodeCapabilities 校验节点动态能力列表及其唯一运行时身份。
func validateNodeCapabilities(node NodeConfig) error {
	if len(node.Capabilities) == 0 {
		return fmt.Errorf("heterogeneous CI node %q capabilities must not be empty", node.NodeID)
	}
	seen := make(map[RuntimeKind]struct{}, len(node.Capabilities))
	for capabilityIndex, capability := range node.Capabilities {
		if err := capability.validate(node.Platform, node.NodeID, capabilityIndex); err != nil {
			return err
		}
		if _, exists := seen[capability.Runtime]; exists {
			return fmt.Errorf("heterogeneous CI node %q capability runtime %q is duplicated", node.NodeID, capability.Runtime)
		}
		seen[capability.Runtime] = struct{}{}
	}
	return nil
}

// validate 校验运行时与节点平台的兼容关系及其资格证据。
func (capability CapabilityConfig) validate(platform Platform, nodeID string, index int) error {
	if !isCanonicalRuntime(capability.Runtime) {
		return fmt.Errorf("heterogeneous CI node %q capability %d runtime %q is invalid", nodeID, index, capability.Runtime)
	}
	switch capability.Runtime {
	case RuntimeUbuntuContainer:
		return validateUbuntuCapability(capability, platform, nodeID, index)
	case RuntimeDarlingCompatRuntime:
		return validateDarlingCapability(capability, platform, nodeID, index)
	case RuntimeWindowsNative:
		return validateWindowsNativeCapability(capability, platform, nodeID, index)
	case RuntimeDarwinNative:
		return validateDarwinNativeCapability(capability, platform, nodeID, index)
	case RuntimeDockerOSXExperimental:
		return validateDockerOSXCapability(capability, platform, nodeID, index)
	default:
		return errors.New("heterogeneous CI runtime dispatch is incomplete")
	}
}

// validateUbuntuCapability 校验 Linux/amd64 Ubuntu 容器能力。
func validateUbuntuCapability(capability CapabilityConfig, platform Platform, nodeID string, index int) error {
	if platform != PlatformLinuxAMD64 {
		return fmt.Errorf("heterogeneous CI node %q ubuntu_container requires linux/amd64", nodeID)
	}
	if err := validateContainerImage(capability, nodeID, index); err != nil {
		return err
	}
	return rejectDarlingAndAttestationFields(capability, nodeID, index)
}

// validateDarlingCapability 校验 Linux/amd64 Darling 兼容运行时资格。
func validateDarlingCapability(capability CapabilityConfig, platform Platform, nodeID string, index int) error {
	if platform != PlatformLinuxAMD64 {
		return fmt.Errorf("heterogeneous CI node %q darling_compat_runtime requires linux/amd64", nodeID)
	}
	if err := validateContainerImage(capability, nodeID, index); err != nil {
		return err
	}
	if !strings.HasPrefix(capability.SourceRevision, "https://github.com/darlinghq/darling@") ||
		!validPinnedRevision(strings.TrimPrefix(capability.SourceRevision, "https://github.com/darlinghq/darling@")) {
		return fmt.Errorf("heterogeneous CI node %q Darling source_revision is not pinned to an official revision", nodeID)
	}
	if strings.TrimSpace(capability.QualificationSuite) == "" || capability.QualificationSuite != strings.TrimSpace(capability.QualificationSuite) || hasControl(capability.QualificationSuite) {
		return fmt.Errorf("heterogeneous CI node %q Darling qualification_suite is required", nodeID)
	}
	return rejectAttestationFields(capability, nodeID, index)
}

// validateWindowsNativeCapability 校验 Windows 原生能力只能在 Windows 节点声明。
func validateWindowsNativeCapability(capability CapabilityConfig, platform Platform, nodeID string, index int) error {
	if platform != PlatformWindowsAMD64 {
		return fmt.Errorf("heterogeneous CI node %q windows_native requires windows/amd64", nodeID)
	}
	return rejectContainerAndAttestationFields(capability, nodeID, index)
}

// validateDarwinNativeCapability 校验 Darwin 原生能力只能在对应 Darwin 节点声明。
func validateDarwinNativeCapability(capability CapabilityConfig, platform Platform, nodeID string, index int) error {
	if platform != PlatformDarwinAMD64 && platform != PlatformDarwinARM64 {
		return fmt.Errorf("heterogeneous CI node %q darwin_native requires a Darwin platform", nodeID)
	}
	return rejectContainerAndAttestationFields(capability, nodeID, index)
}

// validateDockerOSXCapability 校验 Docker-OSX 的实验性完整证明集合。
func validateDockerOSXCapability(capability CapabilityConfig, platform Platform, nodeID string, index int) error {
	if platform != PlatformLinuxAMD64 {
		return fmt.Errorf("heterogeneous CI node %q docker_osx_experimental requires linux/amd64", nodeID)
	}
	if err := validateContainerImage(capability, nodeID, index); err != nil {
		return err
	}
	if err := validateDockerOSXAttestations(capability, nodeID); err != nil {
		return err
	}
	return validateDockerOSXIdentity(capability, nodeID)
}

// validateDockerOSXAttestations 校验 Docker-OSX 的四项显式实验性证明。
func validateDockerOSXAttestations(capability CapabilityConfig, nodeID string) error {
	if !capability.ExperimentalOptIn || !capability.AppleHardwareAttested || !capability.KVM || !capability.CompliantImage {
		return fmt.Errorf("heterogeneous CI node %q docker_osx_experimental requires experimental opt-in, Apple hardware attestation, KVM, and a compliant image", nodeID)
	}
	return nil
}

// validateDockerOSXIdentity 校验 Docker-OSX guest 固定身份及字段边界。
func validateDockerOSXIdentity(capability CapabilityConfig, nodeID string) error {
	if strings.TrimSpace(capability.ImmutableIdentity) == "" || capability.ImmutableIdentity != strings.TrimSpace(capability.ImmutableIdentity) || hasControl(capability.ImmutableIdentity) {
		return fmt.Errorf("heterogeneous CI node %q docker_osx_experimental immutable_identity is required", nodeID)
	}
	if capability.SourceRevision != "" || capability.QualificationSuite != "" {
		return fmt.Errorf("heterogeneous CI node %q docker_osx_experimental must not carry Darling source or qualification fields", nodeID)
	}
	return nil
}

// validateContainerImage 校验容器镜像仓库与 immutable sha256 摘要。
func validateContainerImage(capability CapabilityConfig, nodeID string, index int) error {
	if strings.TrimSpace(capability.Image) == "" || capability.Image != strings.TrimSpace(capability.Image) || hasControl(capability.Image) || strings.Contains(capability.Image, "@") {
		return fmt.Errorf("heterogeneous CI node %q capability %d image repository is invalid", nodeID, index)
	}
	if strings.HasSuffix(strings.ToLower(capability.Image), ":latest") {
		return fmt.Errorf("heterogeneous CI node %q capability %d image must not use latest", nodeID, index)
	}
	if !isSHA256Digest(capability.ImageDigest) {
		return fmt.Errorf("heterogeneous CI node %q capability %d image_digest must be an immutable sha256 digest", nodeID, index)
	}
	return nil
}

func rejectDarlingAndAttestationFields(capability CapabilityConfig, nodeID string, index int) error {
	if capability.SourceRevision != "" || capability.QualificationSuite != "" {
		return fmt.Errorf("heterogeneous CI node %q capability %d must not carry Darling source or qualification fields", nodeID, index)
	}
	return rejectAttestationFields(capability, nodeID, index)
}

// rejectAttestationFields 拒绝非 Docker-OSX 能力携带实验性证明字段。
func rejectAttestationFields(capability CapabilityConfig, nodeID string, index int) error {
	if capability.ExperimentalOptIn || capability.AppleHardwareAttested || capability.KVM || capability.CompliantImage || capability.ImmutableIdentity != "" {
		return fmt.Errorf("heterogeneous CI node %q capability %d must not carry Docker-OSX attestation fields", nodeID, index)
	}
	return nil
}

func rejectContainerAndAttestationFields(capability CapabilityConfig, nodeID string, index int) error {
	if capability.Image != "" || capability.ImageDigest != "" || capability.SourceRevision != "" || capability.QualificationSuite != "" {
		return fmt.Errorf("heterogeneous CI node %q capability %d native runtime must not carry image or qualification fields", nodeID, index)
	}
	return rejectAttestationFields(capability, nodeID, index)
}

// validPinnedRevision 校验 Darling revision 是固定 tag 或完整小写 commit。
func validPinnedRevision(revision string) bool {
	if revision == "" || strings.TrimSpace(revision) != revision || hasControl(revision) || strings.ContainsAny(revision, " \t") {
		return false
	}
	if tag, ok := strings.CutPrefix(revision, "refs/tags/"); ok {
		return validPinnedTag(tag)
	}
	return validPinnedCommit(revision)
}

// validPinnedTag 校验 tag 非空且不是浮动分支名。
func validPinnedTag(tag string) bool {
	if tag == "" {
		return false
	}
	lower := strings.ToLower(tag)
	return lower != "latest" && lower != "main" && lower != "master" && lower != "head"
}

// validPinnedCommit 校验 Darling commit 是完整的小写 SHA-1。
func validPinnedCommit(commit string) bool {
	if len(commit) != 40 {
		return false
	}
	for _, character := range commit {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func isCanonicalRuntime(runtime RuntimeKind) bool {
	switch runtime {
	case RuntimeUbuntuContainer, RuntimeDarlingCompatRuntime, RuntimeWindowsNative, RuntimeDarwinNative, RuntimeDockerOSXExperimental:
		return true
	default:
		return false
	}
}

// isSHA256Digest 判断字符串是否为规范小写 sha256 摘要。
func isSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

const maxManifestBytes = 1 << 20

func isCanonicalPlatform(platform Platform) bool {
	switch platform {
	case PlatformLinuxAMD64, PlatformWindowsAMD64, PlatformDarwinAMD64, PlatformDarwinARM64:
		return true
	default:
		return false
	}
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

func validateManifestJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSONValue(decoder); err != nil {
		return fmt.Errorf("validate heterogeneous CI manifest JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("heterogeneous CI manifest contains trailing JSON values")
		}
		return fmt.Errorf("decode heterogeneous CI manifest trailer: %w", err)
	}
	return nil
}

// validateManifestRootFields 拒绝缺失的顶层字段及 cloud_overflow 子字段。
func validateManifestRootFields(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("heterogeneous CI manifest root must be an object: %w", err)
	}
	for _, field := range []string{"schema_version", "authority", "cloud_overflow", "nodes"} {
		if _, exists := fields[field]; !exists {
			return fmt.Errorf("heterogeneous CI manifest root field %q is required", field)
		}
	}
	var cloudOverflow map[string]json.RawMessage
	if err := json.Unmarshal(fields["cloud_overflow"], &cloudOverflow); err != nil {
		return fmt.Errorf("heterogeneous CI manifest cloud_overflow must be an object: %w", err)
	}
	for _, field := range []string{"enabled", "provider", "platform"} {
		if _, exists := cloudOverflow[field]; !exists {
			return fmt.Errorf("heterogeneous CI manifest cloud_overflow field %q is required", field)
		}
	}
	return nil
}

// walkJSONValue 递归扫描 JSON，拒绝 null 与重复对象 key。
func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case nil:
		return errors.New("heterogeneous CI manifest must not contain null")
	case json.Delim:
		return walkJSONContainer(decoder, value)
	}
	return nil
}

// walkJSONContainer 递归扫描对象或数组容器的子值。
func walkJSONContainer(decoder *json.Decoder, delimiter json.Delim) error {
	switch delimiter {
	case '{':
		return walkJSONObject(decoder)
	case '[':
		return walkJSONArray(decoder)
	default:
		return errors.New("heterogeneous CI manifest has an unexpected delimiter")
	}
}

// walkJSONObject 校验对象 key 唯一并递归检查每个 value。
func walkJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("heterogeneous CI manifest object key is not a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("heterogeneous CI manifest object key %q is duplicated", key)
		}
		seen[key] = struct{}{}
		if err := walkJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectJSONClosingDelimiter(decoder, '}')
}

// walkJSONArray 递归检查数组中的所有值并校验闭合分隔符。
func walkJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := walkJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectJSONClosingDelimiter(decoder, ']')
}

// expectJSONClosingDelimiter 校验容器按预期闭合。
func expectJSONClosingDelimiter(decoder *json.Decoder, expected json.Delim) error {
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != expected {
		return errors.New("heterogeneous CI manifest container is not closed")
	}
	return nil
}
