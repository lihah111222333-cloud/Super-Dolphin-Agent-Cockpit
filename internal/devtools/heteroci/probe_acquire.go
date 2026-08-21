package heteroci

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"sort"
	"time"
)

const acquireProbeRequestMaxBytes = 4 << 10

// AcquireSnapshot 严格采集清单节点快照并在任一节点失败时返回零值。
func AcquireSnapshot(ctx context.Context, manifest Manifest, ownerEpoch RoutingOwnerEpoch, transport ProbeTransport, now time.Time, heartbeatTTL time.Duration) (SnapshotSet, error) {
	capturedAtUnixMilli, err := validateAcquireArguments(ctx, manifest, ownerEpoch, transport, now, heartbeatTTL)
	if err != nil {
		return SnapshotSet{}, err
	}
	nodes := sortedProbeNodes(manifest.Nodes)
	snapshots := make([]NodeSnapshot, 0, len(nodes))
	for _, config := range nodes {
		snapshot, err := acquireNodeSnapshot(ctx, config, ownerEpoch, transport, now, capturedAtUnixMilli, heartbeatTTL)
		if err != nil {
			return SnapshotSet{}, err
		}
		snapshots = append(snapshots, snapshot)
	}
	set := SnapshotSet{
		SchemaVersion:       SnapshotSchemaVersion,
		Authority:           AuthorityLocalNonAuthoritative,
		OwnerEpoch:          ownerEpoch,
		CapacityBasis:       SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		CapturedAtUnixMilli: capturedAtUnixMilli,
		Nodes:               snapshots,
	}
	if err := set.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return SnapshotSet{}, ProbeProtocolError{Field: "snapshot", Cause: err}
	}
	return set, nil
}

// validateAcquireArguments 校验采集器的上下文、owner、时间、传输和清单参数。
func validateAcquireArguments(ctx context.Context, manifest Manifest, ownerEpoch RoutingOwnerEpoch, transport ProbeTransport, now time.Time, heartbeatTTL time.Duration) (int64, error) {
	if isNilProbeValue(ctx) {
		return 0, ProbeArgumentError{Field: "ctx", Cause: errors.New("probe context is required")}
	}
	if isNilProbeValue(transport) {
		return 0, ProbeArgumentError{Field: "transport", Cause: errors.New("probe transport is required")}
	}
	if ownerEpoch <= 0 || ownerEpoch == RoutingOwnerEpoch(math.MaxInt64) {
		return 0, ProbeArgumentError{Field: "owner_epoch", Cause: errors.New("probe owner epoch must be positive and below the maximum")}
	}
	if heartbeatTTL <= 0 {
		return 0, ProbeArgumentError{Field: "heartbeat_ttl", Cause: errors.New("probe heartbeat TTL must be positive")}
	}
	capturedAtUnixMilli, err := checkedUnixMilli(now)
	if err != nil {
		return 0, ProbeArgumentError{Field: "now", Cause: err}
	}
	if capturedAtUnixMilli <= 0 {
		return 0, ProbeArgumentError{Field: "now", Cause: errors.New("probe acquisition time must be positive")}
	}
	if err := manifest.Validate(); err != nil {
		return 0, ProbeArgumentError{Field: "manifest", Cause: err}
	}
	return capturedAtUnixMilli, nil
}

// isNilProbeValue 识别 nil 接口及其携带的 nil 指针依赖。
func isNilProbeValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// sortedProbeNodes 复制并按稳定节点 ID 顺序排列清单节点。
func sortedProbeNodes(nodes []NodeConfig) []NodeConfig {
	sorted := append([]NodeConfig(nil), nodes...)
	sort.Slice(sorted, func(left, right int) bool {
		return sorted[left].NodeID < sorted[right].NodeID
	})
	return sorted
}

// acquireNodeSnapshot 发送一次探针请求并校验其响应生成节点快照。
func acquireNodeSnapshot(ctx context.Context, config NodeConfig, ownerEpoch RoutingOwnerEpoch, transport ProbeTransport, now time.Time, capturedAtUnixMilli int64, heartbeatTTL time.Duration) (NodeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return NodeSnapshot{}, ProbeTransportError{NodeID: config.NodeID, Cause: snapshotProbeExternalError(err).cause}
	}
	request, err := marshalProbeRequest(config, ownerEpoch, capturedAtUnixMilli)
	if err != nil {
		return NodeSnapshot{}, err
	}
	return acquireProbeNode(ctx, config, ownerEpoch, transport, request, now, capturedAtUnixMilli, heartbeatTTL)
}

// acquireProbeNode 处理一次传输、响应流关闭和响应解码的严格边界。
func acquireProbeNode(ctx context.Context, config NodeConfig, ownerEpoch RoutingOwnerEpoch, transport ProbeTransport, request []byte, now time.Time, capturedAtUnixMilli int64, heartbeatTTL time.Duration) (NodeSnapshot, error) {
	body, err := transport.Probe(ctx, config.Endpoint, request)
	bodyNil := isNilProbeValue(body)
	ctxErr := ctx.Err()
	var ctxCause error
	if ctxErr != nil {
		ctxCause = snapshotProbeExternalError(ctxErr).cause
	}
	var transportErr probeExternalErrorSnapshot
	if err != nil {
		transportErr = snapshotProbeExternalError(err)
	}
	if bodyNil && ctxErr == nil && transportErr.onlyUnavailable {
		return offlineProbeSnapshot(config), nil
	}
	if err != nil {
		return failedProbeTransport(config, body, bodyNil, transportErr, ctxCause)
	}
	if ctxCause != nil {
		return failedProbeTransport(config, body, bodyNil, probeExternalErrorSnapshot{}, ctxCause)
	}
	if bodyNil {
		return NodeSnapshot{}, ProbeTransportError{NodeID: config.NodeID, Cause: errors.New("probe transport returned a nil response body")}
	}
	return decodeProbeNode(ctx, config, ownerEpoch, body, now, capturedAtUnixMilli, heartbeatTTL)
}

// failedProbeTransport 关闭传输返回的非空响应体并组合已经快照化的安全根因。
func failedProbeTransport(config NodeConfig, body ProbeResponseBody, bodyNil bool, transportErr probeExternalErrorSnapshot, ctxCause error) (NodeSnapshot, error) {
	causes := []error{transportErr.cause}
	if !bodyNil {
		closeErr := body.Close()
		if closeErr != nil {
			causes = append(causes, snapshotProbeExternalError(closeErr).cause)
		}
	}
	if ctxCause != nil {
		causes = append(causes, ctxCause)
	}
	return NodeSnapshot{}, ProbeTransportError{NodeID: config.NodeID, Cause: errors.Join(causes...)}
}

// probeResponseReader 将每次标准读取绑定到同一探针上下文。
type probeResponseReader struct {
	ctx  context.Context
	body ProbeResponseBody
}

var _ io.Reader = probeResponseReader{}

// Read 使用保存的上下文读取响应体，不启动额外 goroutine。
func (reader probeResponseReader) Read(p []byte) (int, error) {
	return reader.body.Read(reader.ctx, p)
}

// isProbeDecodeTransportError 识别 codec 读取、取消和截止时间传输失败。
func isProbeDecodeTransportError(err error) bool {
	if err == nil {
		return false
	}
	var transportErr ProbeTransportError
	return errors.As(err, &transportErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// probeDecodeCauseWithoutMalformedCategory 只剥离顶层格式分类外壳，避免遍历任意根因链。
func probeDecodeCauseWithoutMalformedCategory(err error) error {
	switch malformed := err.(type) {
	case *ProbeMalformedError:
		if malformed == nil {
			return errors.New(ProbeMalformedError{}.Error())
		}
		if malformed.Cause != nil {
			return malformed.Cause
		}
		return errors.New(malformed.Error())
	case ProbeMalformedError:
		if malformed.Cause != nil {
			return malformed.Cause
		}
		return errors.New(malformed.Error())
	default:
		return err
	}
}

// decodeProbeNode 读取并关闭响应流，再将严格响应交给快照构造器。
func decodeProbeNode(ctx context.Context, config NodeConfig, ownerEpoch RoutingOwnerEpoch, body ProbeResponseBody, now time.Time, capturedAtUnixMilli int64, heartbeatTTL time.Duration) (NodeSnapshot, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		ctxCause := snapshotProbeExternalError(ctxErr).cause
		closeErr := body.Close()
		if closeErr != nil {
			ctxCause = errors.Join(ctxCause, snapshotProbeExternalError(closeErr).cause)
		}
		return NodeSnapshot{}, ProbeTransportError{NodeID: config.NodeID, Cause: ctxCause}
	}
	response, decodeErr := DecodeProbeResponse(probeResponseReader{ctx: ctx, body: body})
	closeErr := body.Close()
	var closeCause error
	if closeErr != nil {
		closeCause = snapshotProbeExternalError(closeErr).cause
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		causes := []error{snapshotProbeExternalError(ctxErr).cause}
		if decodeErr != nil {
			causes = append(causes, probeDecodeCauseWithoutMalformedCategory(decodeErr))
		}
		if closeCause != nil {
			causes = append(causes, closeCause)
		}
		return NodeSnapshot{}, ProbeTransportError{NodeID: config.NodeID, Cause: errors.Join(causes...)}
	}
	if closeCause != nil {
		decodeCause := probeDecodeCauseWithoutMalformedCategory(decodeErr)
		return NodeSnapshot{}, ProbeTransportError{NodeID: config.NodeID, Cause: errors.Join(decodeCause, closeCause)}
	}
	if decodeErr != nil {
		if isProbeDecodeTransportError(decodeErr) {
			return NodeSnapshot{}, ProbeTransportError{NodeID: config.NodeID, Cause: decodeErr}
		}
		return NodeSnapshot{}, ProbeMalformedError{NodeID: config.NodeID, Cause: decodeErr}
	}
	return buildProbeSnapshot(config, ownerEpoch, response, now, capturedAtUnixMilli, heartbeatTTL)
}

// marshalProbeRequest 由 core 以结构体字段顺序生成唯一探针请求字节。
func marshalProbeRequest(config NodeConfig, ownerEpoch RoutingOwnerEpoch, capturedAtUnixMilli int64) ([]byte, error) {
	request := ProbeRequest{
		SchemaVersion:       ProbeRequestSchemaVersion,
		NodeID:              config.NodeID,
		ProbeIdentity:       config.ProbeIdentity,
		OwnerEpoch:          ownerEpoch,
		CapturedAtUnixMilli: capturedAtUnixMilli,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, ProbeArgumentError{Field: "request", Cause: err}
	}
	if len(encoded) > acquireProbeRequestMaxBytes {
		return nil, ProbeArgumentError{Field: "request", Cause: errors.New("probe request exceeds the 4 KiB limit")}
	}
	return encoded, nil
}

// offlineProbeSnapshot 为不可用节点构造显式空能力和零容量快照。
func offlineProbeSnapshot(config NodeConfig) NodeSnapshot {
	return NodeSnapshot{
		NodeID:             config.NodeID,
		Endpoint:           config.Endpoint,
		Platform:           config.Platform,
		ProbeIdentity:      config.ProbeIdentity,
		ProbeBootID:        "",
		ActiveCapabilities: make([]ActiveCapabilitySnapshot, 0),
		Status:             NodeStatusOffline,
		HeartbeatUnixMilli: 0,
		Total:              ResourceCapacity{},
		Available:          ResourceCapacity{},
	}
}

// cloneProbeActiveCapabilities 深拷贝能力并保留 nil 与显式空切片语义。
func cloneProbeActiveCapabilities(source []ActiveCapabilitySnapshot) []ActiveCapabilitySnapshot {
	if source == nil {
		return nil
	}
	clone := make([]ActiveCapabilitySnapshot, len(source))
	copy(clone, source)
	for index, active := range clone {
		if active.Qualification == nil {
			continue
		}
		receipt := *active.Qualification
		clone[index].Qualification = &receipt
	}
	return clone
}

// buildProbeSnapshot 校验响应头身份并通过现有节点快照验证器。
func buildProbeSnapshot(config NodeConfig, ownerEpoch RoutingOwnerEpoch, response ProbeResponse, now time.Time, capturedAtUnixMilli int64, heartbeatTTL time.Duration) (NodeSnapshot, error) {
	if err := validateProbeResponseHeader(config, ownerEpoch, response); err != nil {
		return NodeSnapshot{}, err
	}
	active := cloneProbeActiveCapabilities(response.ActiveCapabilities)
	sort.SliceStable(active, func(left, right int) bool {
		return active[left].Runtime < active[right].Runtime
	})
	snapshot := NodeSnapshot{
		NodeID:             response.NodeID,
		Endpoint:           response.Endpoint,
		Platform:           response.Platform,
		ProbeIdentity:      response.ProbeIdentity,
		ProbeBootID:        response.ProbeBootID,
		ActiveCapabilities: active,
		Status:             response.Status,
		HeartbeatUnixMilli: response.HeartbeatUnixMilli,
		Total:              response.Total,
		Available:          response.Available,
	}
	if err := validateActiveCapabilityIdentity(snapshot, config); err != nil {
		return NodeSnapshot{}, ProbeIdentityDriftError{NodeID: config.NodeID, Field: "active_capabilities", Cause: err}
	}
	if err := validateActiveCapabilities(snapshot, config, capturedAtUnixMilli); err != nil {
		return NodeSnapshot{}, ProbeProtocolError{NodeID: config.NodeID, Field: "active_capabilities", Cause: err}
	}
	if err := validateNodeSnapshot(snapshot, config, capturedAtUnixMilli, now, capturedAtUnixMilli, heartbeatTTL); err != nil {
		return NodeSnapshot{}, ProbeProtocolError{NodeID: config.NodeID, Field: "node_snapshot", Cause: err}
	}
	return snapshot, nil
}

// validateActiveCapabilityIdentity 预检能力集合及其不可变身份字段。
func validateActiveCapabilityIdentity(snapshot NodeSnapshot, config NodeConfig) error {
	if snapshot.ActiveCapabilities == nil {
		return nil
	}
	configured, err := probeCapabilityConfigIndex(config)
	if err != nil {
		return err
	}
	seen := make(map[RuntimeKind]struct{}, len(snapshot.ActiveCapabilities))
	hasEmptyRuntime := false
	for _, active := range snapshot.ActiveCapabilities {
		emptyRuntime, itemErr := validateProbeCapabilityIdentityItem(snapshot, active, configured, seen)
		if itemErr != nil {
			return itemErr
		}
		hasEmptyRuntime = hasEmptyRuntime || emptyRuntime
	}
	if hasEmptyRuntime {
		return nil
	}
	if len(seen) != len(configured) {
		return errors.New("active capability runtime set does not match manifest")
	}
	return nil
}

// probeCapabilityConfigIndex 建立并检查清单运行时身份索引。
func probeCapabilityConfigIndex(config NodeConfig) (map[RuntimeKind]CapabilityConfig, error) {
	configured := make(map[RuntimeKind]CapabilityConfig, len(config.Capabilities))
	for _, capability := range config.Capabilities {
		if _, duplicate := configured[capability.Runtime]; duplicate {
			return nil, errors.New("manifest capability runtime is duplicated")
		}
		configured[capability.Runtime] = capability
	}
	return configured, nil
}

// validateProbeCapabilityIdentityItem 预检一个运行时及其资格回执身份。
func validateProbeCapabilityIdentityItem(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, configured map[RuntimeKind]CapabilityConfig, seen map[RuntimeKind]struct{}) (bool, error) {
	if active.Runtime == "" {
		return true, nil
	}
	if _, duplicate := seen[active.Runtime]; duplicate {
		return false, errors.New("active capability runtime is duplicated")
	}
	expected, exists := configured[active.Runtime]
	if !exists {
		return false, errors.New("active capability runtime is not configured")
	}
	if active.ImageDigest != expected.ImageDigest {
		return false, errors.New("active capability image digest drifted")
	}
	if err := validateProbeQualificationIdentity(snapshot, active, expected); err != nil {
		return false, err
	}
	seen[active.Runtime] = struct{}{}
	return false, nil
}

// validateProbeQualificationIdentity 预检已存在资格回执的绑定身份字段。
func validateProbeQualificationIdentity(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, configured CapabilityConfig) error {
	receipt := active.Qualification
	if receipt == nil || !probeRuntimeRequiresQualification(active.Runtime) {
		return nil
	}
	expectedSuite := configured.QualificationSuite
	if active.Runtime == RuntimeDockerOSXExperimental {
		expectedSuite = DockerOSXQualificationSuite
	}
	if !probeQualificationIdentityMatches(snapshot, active, configured, expectedSuite) {
		return errors.New("qualification identity drifted from the active capability")
	}
	return nil
}

// probeRuntimeRequiresQualification 返回需要资格回执的运行时集合。
func probeRuntimeRequiresQualification(runtime RuntimeKind) bool {
	return runtime == RuntimeDarlingCompatRuntime || runtime == RuntimeDockerOSXExperimental
}

// probeQualificationIdentityMatches 比较资格回执的节点、探针和资格身份字段。
func probeQualificationIdentityMatches(snapshot NodeSnapshot, active ActiveCapabilitySnapshot, configured CapabilityConfig, expectedSuite string) bool {
	receipt := active.Qualification
	return receipt.NodeID == snapshot.NodeID &&
		receipt.Runtime == active.Runtime &&
		receipt.ProbeIdentity == snapshot.ProbeIdentity &&
		receipt.ProbeBootID == snapshot.ProbeBootID &&
		receipt.ImageDigest == active.ImageDigest &&
		receipt.SourceRevision == configured.SourceRevision &&
		receipt.QualificationSuite == expectedSuite &&
		receipt.ImmutableIdentity == configured.ImmutableIdentity
}

// validateProbeResponseHeader 校验响应的 schema、状态、容量口径、owner 和节点身份。
func validateProbeResponseHeader(config NodeConfig, ownerEpoch RoutingOwnerEpoch, response ProbeResponse) error {
	if response.SchemaVersion != ProbeResponseSchemaVersion {
		return ProbeProtocolError{NodeID: config.NodeID, Field: "schema_version", Cause: errors.New("probe response schema version is invalid")}
	}
	if response.Status != NodeStatusLive {
		return ProbeProtocolError{NodeID: config.NodeID, Field: "status", Cause: errors.New("probe response status must be live")}
	}
	if response.CapacityBasis != SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations {
		return ProbeProtocolError{NodeID: config.NodeID, Field: "capacity_basis", Cause: errors.New("probe response capacity basis is invalid")}
	}
	if response.OwnerEpoch != ownerEpoch {
		return ProbeIdentityDriftError{NodeID: config.NodeID, Field: "owner_epoch", Cause: errors.New("probe response owner epoch does not match")}
	}
	if response.NodeID != config.NodeID {
		return ProbeIdentityDriftError{NodeID: config.NodeID, Field: "node_id", Cause: errors.New("probe response node identity does not match")}
	}
	if response.Endpoint != config.Endpoint {
		return ProbeIdentityDriftError{NodeID: config.NodeID, Field: "endpoint", Cause: errors.New("probe response endpoint identity does not match")}
	}
	if response.Platform != config.Platform {
		return ProbeIdentityDriftError{NodeID: config.NodeID, Field: "platform", Cause: errors.New("probe response platform identity does not match")}
	}
	if response.ProbeIdentity != config.ProbeIdentity {
		return ProbeIdentityDriftError{NodeID: config.NodeID, Field: "probe_identity", Cause: errors.New("probe response probe identity does not match")}
	}
	return nil
}
