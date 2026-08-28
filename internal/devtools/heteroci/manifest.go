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
	ManifestSchemaVersion = "heteroci-manifest/v2"
	// AuthorityLocalNonAuthoritative 标记本地主机证据不具备远程权威性。
	AuthorityLocalNonAuthoritative = "LOCAL_NON_AUTHORITATIVE"
	// ResultStatusNotVerified 标记本地异构执行结果尚未取得远程权威资格。
	ResultStatusNotVerified = "NOT_VERIFIED"

	// PlatformLinuxAMD64 是唯一 physical host 平台；PlatformWindowsAMD64 仅表示 Wine logical target。
	PlatformLinuxAMD64   = "linux/amd64"
	PlatformWindowsAMD64 = "windows/amd64"

	// 远程 CI 只接受 Ubuntu 容器和 Ubuntu/Wine 容器两个 logical runtime。
	RuntimeUbuntuContainer     = "ubuntu_container"
	RuntimeUbuntuWineContainer = "ubuntu_wine_container"
)

// Platform 是规范的目标平台标识。
type Platform string

// RuntimeKind 是规范的执行运行时标识。
type RuntimeKind string

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
	Runtime            RuntimeKind `json:"runtime"`
	Image              string      `json:"image"`
	ImageDigest        string      `json:"image_digest"`
	SourceRevision     string      `json:"source_revision"`
	QualificationSuite string      `json:"qualification_suite"`
}

// Manifest 是严格、本地且不具备远程权威性的异构 CI 清单。
type Manifest struct {
	SchemaVersion string       `json:"schema_version"`
	Authority     string       `json:"authority"`
	Nodes         []NodeConfig `json:"nodes"`
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

// Validate 校验清单的通用动态节点契约。
func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("heterogeneous CI manifest schema_version %q is invalid", manifest.SchemaVersion)
	}
	if manifest.Authority != AuthorityLocalNonAuthoritative {
		return fmt.Errorf("heterogeneous CI manifest authority %q is invalid", manifest.Authority)
	}
	if err := validateManifestNodes(manifest.Nodes); err != nil {
		return err
	}
	return nil
}

// ValidateSingleUbuntuWineExecutionPool 校验单个 caller-supplied Linux 宿主及其 Ubuntu/Wine 能力。
func (manifest Manifest) ValidateSingleUbuntuWineExecutionPool() error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if len(manifest.Nodes) != 1 {
		return fmt.Errorf("heterogeneous CI Ubuntu Wine execution pool requires exactly 1 node")
	}
	node := manifest.Nodes[0]
	if node.Platform != PlatformLinuxAMD64 {
		return fmt.Errorf("heterogeneous CI Ubuntu Wine node %q must use physical linux/amd64", node.NodeID)
	}
	if len(node.Capabilities) != 2 || node.Capabilities[0].Runtime != RuntimeUbuntuContainer || node.Capabilities[1].Runtime != RuntimeUbuntuWineContainer {
		return fmt.Errorf("heterogeneous CI Ubuntu Wine node %q must declare sorted ubuntu_container and ubuntu_wine_container capabilities", node.NodeID)
	}
	return nil
}

// validateManifestNodes 校验动态节点集合唯一性。
func validateManifestNodes(nodes []NodeConfig) error {
	if len(nodes) == 0 {
		return errors.New("heterogeneous CI manifest nodes must not be empty")
	}
	seen := make(map[string]struct{}, len(nodes))
	seenEndpoints := make(map[string]struct{}, len(nodes))
	seenProbeIdentities := make(map[string]struct{}, len(nodes))
	for index, node := range nodes {
		if err := node.validate(index); err != nil {
			return err
		}
		if err := validateNodeUniqueness(node, seen, seenEndpoints, seenProbeIdentities); err != nil {
			return err
		}
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

// validate 校验节点身份、平台与动态能力列表。
func (node NodeConfig) validate(index int) error {
	if err := validateNodeIdentity(node, index); err != nil {
		return err
	}
	if node.Platform != PlatformLinuxAMD64 {
		return fmt.Errorf("heterogeneous CI node %q physical platform %q is invalid", node.NodeID, node.Platform)
	}
	return validateNodeCapabilities(node)
}

// ValidateNodeConfig 校验单个 Ubuntu physical host probe 配置节点的通用动态能力，不执行整套路由池约束。
func ValidateNodeConfig(node NodeConfig) error {
	return node.validate(0)
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
	case RuntimeUbuntuWineContainer:
		return validateUbuntuWineCapability(capability, platform, nodeID, index)
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
	return rejectWineOnlyFields(capability, nodeID, index)
}

// validateUbuntuWineCapability 校验 Linux/amd64 Ubuntu 容器内的 Wine Windows 能力。
func validateUbuntuWineCapability(capability CapabilityConfig, platform Platform, nodeID string, index int) error {
	if platform != PlatformLinuxAMD64 {
		return fmt.Errorf("heterogeneous CI node %q ubuntu_wine_container requires linux/amd64", nodeID)
	}
	if err := validateContainerImage(capability, nodeID, index); err != nil {
		return err
	}
	if !validWineSourceRevision(capability.SourceRevision) {
		return fmt.Errorf("heterogeneous CI node %q Wine source_revision is not pinned", nodeID)
	}
	if strings.TrimSpace(capability.QualificationSuite) == "" || capability.QualificationSuite != strings.TrimSpace(capability.QualificationSuite) || hasControl(capability.QualificationSuite) {
		return fmt.Errorf("heterogeneous CI node %q Wine qualification_suite is required", nodeID)
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

func rejectWineOnlyFields(capability CapabilityConfig, nodeID string, index int) error {
	if capability.SourceRevision != "" || capability.QualificationSuite != "" {
		return fmt.Errorf("heterogeneous CI node %q capability %d must not carry Wine source or qualification fields", nodeID, index)
	}
	return nil
}

func validWineSourceRevision(value string) bool {
	if validPinnedRevision(value) {
		return true
	}
	separator := strings.LastIndexByte(value, '@')
	if separator <= len("https://") || !strings.HasPrefix(value, "https://") || separator == len(value)-1 {
		return false
	}
	source := value[:separator]
	if strings.TrimSpace(source) != source || hasControl(source) || strings.ContainsAny(source, " \t") {
		return false
	}
	return validPinnedRevision(value[separator+1:])
}

// validPinnedRevision 校验来源 revision 是固定 tag 或完整小写 commit。
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

// validPinnedCommit 校验来源 commit 是完整的小写 SHA-1。
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
	case RuntimeUbuntuContainer, RuntimeUbuntuWineContainer:
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
	case PlatformLinuxAMD64, PlatformWindowsAMD64:
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

// validateManifestRootFields 拒绝缺失的顶层字段。
func validateManifestRootFields(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("heterogeneous CI manifest root must be an object: %w", err)
	}
	for _, field := range []string{"schema_version", "authority", "nodes"} {
		if _, exists := fields[field]; !exists {
			return fmt.Errorf("heterogeneous CI manifest root field %q is required", field)
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
