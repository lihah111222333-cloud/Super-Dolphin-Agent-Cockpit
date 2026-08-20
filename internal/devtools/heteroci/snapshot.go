package heteroci

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// NodeStatus 表示本地执行节点上报的生命周期状态。
type NodeStatus string

const (
	// NodeStatusLive 表示节点当前可提供容量。
	NodeStatusLive NodeStatus = "live"
	// NodeStatusOffline 表示节点当前不可提供容量。
	NodeStatusOffline NodeStatus = "offline"
)

const (
	// CapabilityQualificationSchemaVersion 是能力资格回执的严格 schema 标识。
	CapabilityQualificationSchemaVersion = "heteroci-capability-qualification/v1"
	// SnapshotSchemaVersion 是异构 CI 快照的严格 wire schema 标识。
	SnapshotSchemaVersion = "heteroci-snapshot/v3"
	// DockerOSXQualificationSuite 是 Docker-OSX 能力固定使用的资格套件。
	DockerOSXQualificationSuite = "docker-osx-attestation/v1"
)

// SnapshotCapacityBasis 标识快照 Available 容量的扣减口径。
type SnapshotCapacityBasis string

const (
	// SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations 表示 Available 已扣除外部/宿主占用，但未扣本 RoutingLeaseBook 的预留。
	SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations SnapshotCapacityBasis = "external-available-excludes-routing-book-reservations/v1"
)

// CapabilityQualificationReceipt 绑定一次探针启动及其资格验证证据。
type CapabilityQualificationReceipt struct {
	SchemaVersion        string      `json:"schema_version"`
	NodeID               string      `json:"node_id"`
	Runtime              RuntimeKind `json:"runtime"`
	ProbeIdentity        string      `json:"probe_identity"`
	ProbeBootID          string      `json:"probe_boot_id"`
	ImageDigest          string      `json:"image_digest"`
	SourceRevision       string      `json:"source_revision"`
	QualificationSuite   string      `json:"qualification_suite"`
	ImmutableIdentity    string      `json:"immutable_identity"`
	CompletedAtUnixMilli int64       `json:"completed_at_unix_milli"`
	Digest               string      `json:"digest"`
}

// ActiveCapabilitySnapshot 表示本次探针已实际启动并验证的运行时能力。
type ActiveCapabilitySnapshot struct {
	Runtime       RuntimeKind                     `json:"runtime"`
	ImageDigest   string                          `json:"image_digest"`
	Qualification *CapabilityQualificationReceipt `json:"qualification,omitempty"`
}

// CanonicalDigest 返回不包含 Digest 自身的确定性 SHA-256 回执摘要。
func (receipt CapabilityQualificationReceipt) CanonicalDigest() string {
	material := make([]byte, 0, 256)
	material = appendCanonicalString(material, receipt.SchemaVersion)
	material = appendCanonicalString(material, receipt.NodeID)
	material = appendCanonicalString(material, string(receipt.Runtime))
	material = appendCanonicalString(material, receipt.ProbeIdentity)
	material = appendCanonicalString(material, receipt.ProbeBootID)
	material = appendCanonicalString(material, receipt.ImageDigest)
	material = appendCanonicalString(material, receipt.SourceRevision)
	material = appendCanonicalString(material, receipt.QualificationSuite)
	material = appendCanonicalString(material, receipt.ImmutableIdentity)
	material = appendCanonicalInt64(material, receipt.CompletedAtUnixMilli)
	digest := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func appendCanonicalString(dst []byte, value string) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len([]byte(value))))
	dst = append(dst, length[:]...)
	return append(dst, []byte(value)...)
}

func appendCanonicalInt64(dst []byte, value int64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	return append(dst, encoded[:]...)
}

// ResourceCapacity 表示节点实测 CPU、内存和并发预算。
type ResourceCapacity struct {
	CPUMilli       int64 `json:"cpu_milli"`
	MemoryBytes    int64 `json:"memory_bytes"`
	MaxConcurrency int   `json:"max_concurrency"`
}

// NodeSnapshot 表示配置节点在某一时刻的实时观测。
type NodeSnapshot struct {
	NodeID             string                     `json:"node_id"`
	Endpoint           string                     `json:"endpoint"`
	Platform           Platform                   `json:"platform"`
	ProbeIdentity      string                     `json:"probe_identity"`
	ProbeBootID        string                     `json:"probe_boot_id"`
	ActiveCapabilities []ActiveCapabilitySnapshot `json:"active_capabilities"`
	Status             NodeStatus                 `json:"status"`
	HeartbeatUnixMilli int64                      `json:"heartbeat_unix_milli"`
	Total              ResourceCapacity           `json:"total"`
	// Available 是 allocatable 上限减去不属于同一 RoutingLeaseBook owner epoch 的外部/宿主占用后的容量；
	// 本 book 的 planned/started reservations 均不在此扣减，唯一扣减点为 effectiveRoutingSnapshot；
	// book 重启后的旧 epoch 占用必须由未来 producer 标记为 external。
	Available ResourceCapacity `json:"available"`
}

// SnapshotSet 表示一次同时采集的动态节点观测集合。
type SnapshotSet struct {
	SchemaVersion       string                `json:"schema_version"`
	Authority           string                `json:"authority"`
	OwnerEpoch          RoutingOwnerEpoch     `json:"owner_epoch"`
	CapacityBasis       SnapshotCapacityBasis `json:"capacity_basis"`
	CapturedAtUnixMilli int64                 `json:"captured_at_unix_milli"`
	Nodes               []NodeSnapshot        `json:"nodes"`
}

// ValidateAgainst 校验节点观测与清单严格对齐，并按状态执行容量与心跳边界。
func (set SnapshotSet) ValidateAgainst(manifest Manifest, now time.Time, heartbeatTTL time.Duration) error {
	if set.OwnerEpoch <= 0 || set.OwnerEpoch >= RoutingOwnerEpoch(math.MaxInt64) {
		return errors.New("heterogeneous CI snapshot owner epoch must be positive and below the maximum")
	}
	if set.SchemaVersion != SnapshotSchemaVersion {
		return fmt.Errorf("heterogeneous CI snapshot schema_version %q is invalid", set.SchemaVersion)
	}
	if set.Authority != AuthorityLocalNonAuthoritative {
		return fmt.Errorf("heterogeneous CI snapshot authority %q is invalid", set.Authority)
	}
	if set.CapacityBasis != SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations {
		return fmt.Errorf("heterogeneous CI snapshot capacity_basis %q is invalid", set.CapacityBasis)
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("validate heterogeneous CI snapshot manifest: %w", err)
	}
	nowUnixMilli, err := validateSnapshotTemporalBounds(set, now, heartbeatTTL)
	if err != nil {
		return err
	}
	if len(manifest.Nodes) == 0 {
		return errors.New("heterogeneous CI snapshot manifest nodes must not be empty")
	}

	configs, err := indexNodeConfigs(manifest)
	if err != nil {
		return err
	}
	return validateSnapshotNodes(set, configs, now, nowUnixMilli, heartbeatTTL)
}

// validateSnapshotTemporalBounds 校验快照捕获时间窗口并返回当前 Unix 毫秒。
func validateSnapshotTemporalBounds(set SnapshotSet, now time.Time, heartbeatTTL time.Duration) (int64, error) {
	if heartbeatTTL <= 0 {
		return 0, errors.New("heterogeneous CI snapshot heartbeat TTL must be positive")
	}
	nowUnixMilli, err := checkedUnixMilli(now)
	if err != nil {
		return 0, fmt.Errorf("heterogeneous CI snapshot validation time: %w", err)
	}
	if set.CapturedAtUnixMilli <= 0 {
		return 0, errors.New("heterogeneous CI snapshot capture time must be positive")
	}
	if set.CapturedAtUnixMilli > nowUnixMilli {
		return 0, errors.New("heterogeneous CI snapshot capture time is in the future")
	}
	captureAge := now.Sub(time.UnixMilli(set.CapturedAtUnixMilli))
	if captureAge < 0 || captureAge > heartbeatTTL {
		return 0, errors.New("heterogeneous CI snapshot capture time is stale")
	}
	return nowUnixMilli, nil
}

// validateSnapshotNodes 校验快照节点与清单严格一一对应并检查每个节点。
func validateSnapshotNodes(set SnapshotSet, configs map[string]NodeConfig, now time.Time, nowUnixMilli int64, heartbeatTTL time.Duration) error {
	if len(set.Nodes) != len(configs) {
		return fmt.Errorf("heterogeneous CI snapshot node count %d does not match manifest count %d", len(set.Nodes), len(configs))
	}

	seen := make(map[string]struct{}, len(set.Nodes))
	for index, snapshot := range set.Nodes {
		config, ok := configs[snapshot.NodeID]
		if !ok {
			return fmt.Errorf("heterogeneous CI snapshot node %d %q is not configured", index, snapshot.NodeID)
		}
		if _, duplicate := seen[snapshot.NodeID]; duplicate {
			return fmt.Errorf("heterogeneous CI snapshot node %q is duplicated", snapshot.NodeID)
		}
		seen[snapshot.NodeID] = struct{}{}
		if err := validateNodeSnapshot(snapshot, config, set.CapturedAtUnixMilli, now, nowUnixMilli, heartbeatTTL); err != nil {
			return err
		}
	}
	for nodeID := range configs {
		if _, present := seen[nodeID]; !present {
			return fmt.Errorf("heterogeneous CI snapshot is missing configured node %q", nodeID)
		}
	}
	return nil
}

// validateNodeSnapshot 校验单个节点的身份、能力、状态、容量和心跳。
func validateNodeSnapshot(snapshot NodeSnapshot, config NodeConfig, capturedAtUnixMilli int64, now time.Time, nowUnixMilli int64, heartbeatTTL time.Duration) error {
	if snapshot.Platform != config.Platform {
		return fmt.Errorf("heterogeneous CI snapshot node %q platform %q does not match manifest platform %q", snapshot.NodeID, snapshot.Platform, config.Platform)
	}
	if snapshot.Endpoint != config.Endpoint {
		return fmt.Errorf("heterogeneous CI snapshot node %q endpoint does not match manifest endpoint", snapshot.NodeID)
	}
	if snapshot.ProbeIdentity != config.ProbeIdentity {
		return fmt.Errorf("heterogeneous CI snapshot node %q probe identity does not match manifest probe identity", snapshot.NodeID)
	}
	if err := validateActiveCapabilities(snapshot, config, capturedAtUnixMilli); err != nil {
		return err
	}
	if err := validateSnapshotStatus(snapshot); err != nil {
		return err
	}
	if err := validateSnapshotCapacity(snapshot.NodeID, snapshot.Status, snapshot.Total, snapshot.Available); err != nil {
		return err
	}
	return validateHeartbeat(snapshot.NodeID, snapshot.Status, snapshot.HeartbeatUnixMilli, capturedAtUnixMilli, now, nowUnixMilli, heartbeatTTL)
}

// validateSnapshotStatus 校验节点状态、探针启动与显式能力声明；offline 必须使用非 nil 空切片。
func validateSnapshotStatus(snapshot NodeSnapshot) error {
	if snapshot.ActiveCapabilities == nil {
		return fmt.Errorf("heterogeneous CI snapshot node %q active capabilities must be non-nil", snapshot.NodeID)
	}
	if err := validateLiveProbeBootID(snapshot); err != nil {
		return err
	}
	if snapshot.Status == NodeStatusOffline && snapshot.ProbeBootID != "" {
		return fmt.Errorf("heterogeneous CI offline snapshot node %q probe boot id must be empty", snapshot.NodeID)
	}
	if snapshot.Status == NodeStatusOffline && len(snapshot.ActiveCapabilities) != 0 {
		return fmt.Errorf("heterogeneous CI offline snapshot node %q must report no active capabilities", snapshot.NodeID)
	}
	if snapshot.Status == NodeStatusLive && len(snapshot.ActiveCapabilities) == 0 {
		return fmt.Errorf("heterogeneous CI live snapshot node %q must report an active capability", snapshot.NodeID)
	}
	return nil
}

// validateLiveProbeBootID 校验 live 节点的探针启动身份非空且无控制字符。
func validateLiveProbeBootID(snapshot NodeSnapshot) error {
	if snapshot.Status != NodeStatusLive {
		return nil
	}
	if strings.TrimSpace(snapshot.ProbeBootID) == "" || snapshot.ProbeBootID != strings.TrimSpace(snapshot.ProbeBootID) || hasControl(snapshot.ProbeBootID) {
		return fmt.Errorf("heterogeneous CI live snapshot node %q probe boot id must not be empty", snapshot.NodeID)
	}
	return nil
}

// indexNodeConfigs 建立 manifest 节点索引并按顺序校验节点能力。
func indexNodeConfigs(manifest Manifest) (map[string]NodeConfig, error) {
	configs := make(map[string]NodeConfig, len(manifest.Nodes))
	for index, config := range manifest.Nodes {
		if err := validateNodeConfigForIndex(index, config, configs); err != nil {
			return nil, err
		}
		configs[config.NodeID] = config
	}
	return configs, nil
}

// validateNodeConfigForIndex 校验节点身份、平台、能力数量和能力列表。
func validateNodeConfigForIndex(index int, config NodeConfig, configs map[string]NodeConfig) error {
	if strings.TrimSpace(config.NodeID) == "" || config.NodeID != strings.TrimSpace(config.NodeID) {
		return fmt.Errorf("heterogeneous CI manifest node %d has an empty node_id", index)
	}
	if _, duplicate := configs[config.NodeID]; duplicate {
		return fmt.Errorf("heterogeneous CI manifest node %q is duplicated", config.NodeID)
	}
	if config.Platform == "" {
		return fmt.Errorf("heterogeneous CI manifest node %q has no platform", config.NodeID)
	}
	if len(config.Capabilities) == 0 {
		return fmt.Errorf("heterogeneous CI manifest node %q has no capabilities", config.NodeID)
	}
	return validateConfiguredCapabilities(config)
}

// validateConfiguredCapabilities 校验节点能力运行时唯一且具备必要资格。
func validateConfiguredCapabilities(config NodeConfig) error {
	capabilities := make(map[RuntimeKind]struct{}, len(config.Capabilities))
	for capabilityIndex, capability := range config.Capabilities {
		if capability.Runtime == "" {
			return fmt.Errorf("heterogeneous CI manifest node %q capability %d has no runtime", config.NodeID, capabilityIndex)
		}
		if _, duplicate := capabilities[capability.Runtime]; duplicate {
			return fmt.Errorf("heterogeneous CI manifest node %q capability %q is duplicated", config.NodeID, capability.Runtime)
		}
		capabilities[capability.Runtime] = struct{}{}
		if isDarlingRuntime(capability.Runtime) && !darlingCapabilityQualified(capability) {
			return fmt.Errorf("heterogeneous CI manifest node %q Darling capability is not qualified", config.NodeID)
		}
	}
	return nil
}

// validateActiveCapabilities 校验 live 快照能力是清单能力的无重复且完整集合。
func validateActiveCapabilities(snapshot NodeSnapshot, config NodeConfig, capturedAtUnixMilli int64) error {
	if snapshot.ActiveCapabilities == nil {
		return fmt.Errorf("heterogeneous CI snapshot node %q active capabilities must be non-nil", snapshot.NodeID)
	}
	configuredByRuntime := make(map[RuntimeKind]CapabilityConfig, len(config.Capabilities))
	for _, capability := range config.Capabilities {
		if _, duplicate := configuredByRuntime[capability.Runtime]; duplicate {
			return fmt.Errorf("heterogeneous CI manifest node %q capability %q is duplicated", snapshot.NodeID, capability.Runtime)
		}
		configuredByRuntime[capability.Runtime] = capability
	}
	seen := make(map[RuntimeKind]struct{}, len(snapshot.ActiveCapabilities))
	for index, active := range snapshot.ActiveCapabilities {
		runtime := active.Runtime
		if runtime == "" {
			return fmt.Errorf("heterogeneous CI snapshot node %q active capability %d runtime is empty", snapshot.NodeID, index)
		}
		if _, duplicate := seen[runtime]; duplicate {
			return fmt.Errorf("heterogeneous CI snapshot node %q active capability %q is duplicated", snapshot.NodeID, runtime)
		}
		configured, exists := configuredByRuntime[runtime]
		if !exists {
			return fmt.Errorf("heterogeneous CI snapshot node %q active capability %q is not configured", snapshot.NodeID, runtime)
		}
		if err := validateActiveCapabilitySnapshot(snapshot, active, configured, capturedAtUnixMilli); err != nil {
			return err
		}
		seen[runtime] = struct{}{}
	}
	if snapshot.Status != NodeStatusLive {
		return nil
	}
	return validateActiveCapabilitySetComplete(snapshot.NodeID, seen, configuredByRuntime)
}

// validateActiveCapabilitySetComplete 校验快照能力覆盖清单中的全部运行时，集合顺序不参与语义。
func validateActiveCapabilitySetComplete(nodeID string, seen map[RuntimeKind]struct{}, configured map[RuntimeKind]CapabilityConfig) error {
	if len(seen) != len(configured) {
		return fmt.Errorf("heterogeneous CI snapshot node %q active capabilities must exactly match manifest capabilities", nodeID)
	}
	for runtime := range configured {
		if _, exists := seen[runtime]; !exists {
			return fmt.Errorf("heterogeneous CI snapshot node %q active capability %q is missing", nodeID, runtime)
		}
	}
	return nil
}

// validateActiveCapabilitySnapshot 按运行时分派能力摘要和资格回执校验。
func validateActiveCapabilitySnapshot(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, configured CapabilityConfig, capturedAtUnixMilli int64) error {
	switch active.Runtime {
	case RuntimeUbuntuContainer:
		return validateUbuntuActiveCapability(snapshot, active, configured)
	case RuntimeWindowsNative, RuntimeDarwinNative:
		return validateNativeActiveCapability(snapshot, active)
	case RuntimeDarlingCompatRuntime:
		return validateDarlingActiveCapability(snapshot, active, configured, capturedAtUnixMilli)
	case RuntimeDockerOSXExperimental:
		return validateDockerOSXActiveCapability(snapshot, active, configured, capturedAtUnixMilli)
	default:
		return fmt.Errorf("heterogeneous CI snapshot node %q active capability %q is unsupported", snapshot.NodeID, active.Runtime)
	}
}

// validateUbuntuActiveCapability 校验 Ubuntu 容器的镜像摘要与回执边界。
func validateUbuntuActiveCapability(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, configured CapabilityConfig) error {
	if active.ImageDigest != configured.ImageDigest {
		return fmt.Errorf("heterogeneous CI snapshot node %q ubuntu_container image digest does not match manifest", snapshot.NodeID)
	}
	if active.Qualification != nil {
		return fmt.Errorf("heterogeneous CI snapshot node %q ubuntu_container must not carry a qualification receipt", snapshot.NodeID)
	}
	return nil
}

// validateNativeActiveCapability 校验原生 Windows 或 Darwin 能力不携带容器字段。
func validateNativeActiveCapability(snapshot NodeSnapshot, active ActiveCapabilitySnapshot) error {
	if active.ImageDigest != "" {
		return fmt.Errorf("heterogeneous CI snapshot node %q native runtime must not carry an image digest", snapshot.NodeID)
	}
	if active.Qualification != nil {
		return fmt.Errorf("heterogeneous CI snapshot node %q native runtime must not carry a qualification receipt", snapshot.NodeID)
	}
	return nil
}

// validateDarlingActiveCapability 校验 Darling 镜像、资格回执及来源绑定。
func validateDarlingActiveCapability(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, configured CapabilityConfig, capturedAtUnixMilli int64) error {
	if active.ImageDigest != configured.ImageDigest {
		return fmt.Errorf("heterogeneous CI snapshot node %q Darling image digest does not match manifest", snapshot.NodeID)
	}
	if active.Qualification == nil {
		return fmt.Errorf("heterogeneous CI snapshot node %q Darling capability requires a qualification receipt", snapshot.NodeID)
	}
	if err := validateQualificationReceipt(snapshot, active, configured, capturedAtUnixMilli); err != nil {
		return err
	}
	if active.Qualification.SourceRevision != configured.SourceRevision {
		return fmt.Errorf("heterogeneous CI snapshot node %q Darling source revision does not match manifest", snapshot.NodeID)
	}
	if active.Qualification.QualificationSuite != configured.QualificationSuite {
		return fmt.Errorf("heterogeneous CI snapshot node %q Darling qualification suite does not match manifest", snapshot.NodeID)
	}
	if active.Qualification.ImmutableIdentity != "" {
		return fmt.Errorf("heterogeneous CI snapshot node %q Darling immutable identity must be empty", snapshot.NodeID)
	}
	return nil
}

// validateDockerOSXActiveCapability 校验 Docker-OSX 镜像、回执和实验身份。
func validateDockerOSXActiveCapability(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, configured CapabilityConfig, capturedAtUnixMilli int64) error {
	if active.ImageDigest != configured.ImageDigest {
		return fmt.Errorf("heterogeneous CI snapshot node %q Docker-OSX image digest does not match manifest", snapshot.NodeID)
	}
	if active.Qualification == nil {
		return fmt.Errorf("heterogeneous CI snapshot node %q Docker-OSX capability requires a qualification receipt", snapshot.NodeID)
	}
	if err := validateQualificationReceipt(snapshot, active, configured, capturedAtUnixMilli); err != nil {
		return err
	}
	if active.Qualification.SourceRevision != "" {
		return fmt.Errorf("heterogeneous CI snapshot node %q Docker-OSX source revision must be empty", snapshot.NodeID)
	}
	if active.Qualification.QualificationSuite != DockerOSXQualificationSuite {
		return fmt.Errorf("heterogeneous CI snapshot node %q Docker-OSX qualification suite is invalid", snapshot.NodeID)
	}
	if active.Qualification.ImmutableIdentity != configured.ImmutableIdentity {
		return fmt.Errorf("heterogeneous CI snapshot node %q Docker-OSX immutable identity does not match manifest", snapshot.NodeID)
	}
	return nil
}

// validateQualificationReceipt 校验资格回执的 schema、身份、时间和摘要。
func validateQualificationReceipt(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, configured CapabilityConfig, capturedAtUnixMilli int64) error {
	receipt := active.Qualification
	if receipt == nil {
		return fmt.Errorf("heterogeneous CI snapshot node %q capability %q qualification receipt is required", snapshot.NodeID, active.Runtime)
	}
	if receipt.SchemaVersion != CapabilityQualificationSchemaVersion {
		return fmt.Errorf("heterogeneous CI snapshot node %q capability %q qualification schema is invalid", snapshot.NodeID, active.Runtime)
	}
	if err := validateQualificationIdentity(snapshot, active, receipt); err != nil {
		return err
	}
	if receipt.ImageDigest != configured.ImageDigest {
		return fmt.Errorf("heterogeneous CI snapshot node %q capability %q qualification image digest does not match manifest", snapshot.NodeID, active.Runtime)
	}
	if receipt.CompletedAtUnixMilli <= 0 || receipt.CompletedAtUnixMilli > capturedAtUnixMilli {
		return fmt.Errorf("heterogeneous CI snapshot node %q capability %q qualification completion time is invalid", snapshot.NodeID, active.Runtime)
	}
	if receipt.Digest == "" || receipt.Digest != receipt.CanonicalDigest() {
		return fmt.Errorf("heterogeneous CI snapshot node %q capability %q qualification digest is invalid", snapshot.NodeID, active.Runtime)
	}
	return nil
}

// validateQualificationIdentity 确认资格回执绑定当前节点能力探针身份。
func validateQualificationIdentity(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, receipt *CapabilityQualificationReceipt) error {
	if receipt.NodeID != snapshot.NodeID || receipt.Runtime != active.Runtime || receipt.ProbeIdentity != snapshot.ProbeIdentity || receipt.ProbeBootID != snapshot.ProbeBootID || receipt.ImageDigest != active.ImageDigest {
		return fmt.Errorf("heterogeneous CI snapshot node %q capability %q qualification identity is not bound to the snapshot", snapshot.NodeID, active.Runtime)
	}
	return nil
}

// validateSnapshotCapacity 校验节点总容量、可用容量和状态边界。
func validateSnapshotCapacity(nodeID string, status NodeStatus, total, available ResourceCapacity) error {
	if err := validateNonNegativeCapacity(nodeID, total, available); err != nil {
		return err
	}
	if err := validateAvailableCapacityBounds(nodeID, total, available); err != nil {
		return err
	}
	switch status {
	case NodeStatusLive:
		if err := validatePositiveTotalCapacity(nodeID, total); err != nil {
			return err
		}
	case NodeStatusOffline:
		if total != (ResourceCapacity{}) {
			return fmt.Errorf("heterogeneous CI offline snapshot node %q must report zero total capacity", nodeID)
		}
		if available != (ResourceCapacity{}) {
			return fmt.Errorf("heterogeneous CI offline snapshot node %q must report zero available capacity", nodeID)
		}
	default:
		return fmt.Errorf("heterogeneous CI snapshot node %q has unknown status %q", nodeID, status)
	}
	return nil
}

// validateAvailableCapacityBounds 校验可用容量不超过节点总容量。
func validateAvailableCapacityBounds(nodeID string, total, available ResourceCapacity) error {
	if available.CPUMilli > total.CPUMilli || available.MemoryBytes > total.MemoryBytes || available.MaxConcurrency > total.MaxConcurrency {
		return fmt.Errorf("heterogeneous CI snapshot node %q available capacity exceeds total capacity", nodeID)
	}
	return nil
}

// validatePositiveTotalCapacity 校验 live 节点的总容量为正值。
func validatePositiveTotalCapacity(nodeID string, total ResourceCapacity) error {
	if total.CPUMilli <= 0 || total.MemoryBytes <= 0 || total.MaxConcurrency <= 0 {
		return fmt.Errorf("heterogeneous CI live snapshot node %q must report positive total capacity", nodeID)
	}
	return nil
}

// validateNonNegativeCapacity 拒绝总容量或可用容量中的负值。
func validateNonNegativeCapacity(nodeID string, total, available ResourceCapacity) error {
	if total.CPUMilli < 0 || total.MemoryBytes < 0 || total.MaxConcurrency < 0 {
		return fmt.Errorf("heterogeneous CI snapshot node %q has negative total capacity", nodeID)
	}
	if available.CPUMilli < 0 || available.MemoryBytes < 0 || available.MaxConcurrency < 0 {
		return fmt.Errorf("heterogeneous CI snapshot node %q has negative available capacity", nodeID)
	}
	return nil
}

// validateHeartbeat 校验节点心跳；live 必须存在且新鲜，offline 允许零值或非负历史时间但拒绝未来值。
func validateHeartbeat(nodeID string, status NodeStatus, heartbeatUnixMilli, capturedAtUnixMilli int64, now time.Time, nowUnixMilli int64, heartbeatTTL time.Duration) error {
	heartbeat, present, err := validateHeartbeatTimestamp(nodeID, heartbeatUnixMilli, capturedAtUnixMilli, now, nowUnixMilli)
	if err != nil {
		return err
	}
	if status != NodeStatusLive {
		return nil
	}
	if !present {
		return fmt.Errorf("heterogeneous CI snapshot node %q heartbeat must be positive", nodeID)
	}
	age := now.Sub(heartbeat)
	if age < 0 || age > heartbeatTTL {
		return fmt.Errorf("heterogeneous CI live snapshot node %q heartbeat is stale", nodeID)
	}
	return nil
}

// validateHeartbeatTimestamp 校验心跳非负、不得晚于捕获/当前时间，并返回是否存在历史心跳。
func validateHeartbeatTimestamp(nodeID string, heartbeatUnixMilli, capturedAtUnixMilli int64, now time.Time, nowUnixMilli int64) (time.Time, bool, error) {
	if heartbeatUnixMilli < 0 {
		return time.Time{}, false, fmt.Errorf("heterogeneous CI snapshot node %q heartbeat must be non-negative", nodeID)
	}
	if heartbeatUnixMilli == 0 {
		return time.Time{}, false, nil
	}
	if heartbeatUnixMilli > capturedAtUnixMilli || heartbeatUnixMilli > nowUnixMilli {
		return time.Time{}, false, fmt.Errorf("heterogeneous CI snapshot node %q heartbeat is in the future", nodeID)
	}
	heartbeat := time.UnixMilli(heartbeatUnixMilli)
	if heartbeat.After(now) {
		return time.Time{}, false, fmt.Errorf("heterogeneous CI snapshot node %q heartbeat is in the future", nodeID)
	}
	return heartbeat, true, nil
}

func darlingCapabilityQualified(capability CapabilityConfig) bool {
	return strings.TrimSpace(capability.Image) != "" &&
		strings.TrimSpace(capability.ImageDigest) != "" &&
		strings.TrimSpace(capability.SourceRevision) != "" &&
		strings.TrimSpace(capability.QualificationSuite) != ""
}

func isDarlingRuntime(runtime RuntimeKind) bool {
	return runtime == RuntimeDarlingCompatRuntime
}

// checkedUnixMilli 将时间安全转换为 Unix 毫秒并拒绝溢出。
func checkedUnixMilli(value time.Time) (int64, error) {
	seconds := value.Unix()
	nanos := int64(value.Nanosecond())
	if seconds > math.MaxInt64/1000 || seconds < math.MinInt64/1000 {
		return 0, errors.New("time cannot be represented as Unix milliseconds")
	}
	base := seconds * 1000
	extra := nanos / int64(time.Millisecond)
	if extra > 0 && base > math.MaxInt64-extra {
		return 0, errors.New("time Unix millisecond conversion overflows")
	}
	if extra < 0 && base < math.MinInt64-extra {
		return 0, errors.New("time Unix millisecond conversion underflows")
	}
	return base + extra, nil
}
