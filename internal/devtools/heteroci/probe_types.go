package heteroci

import (
	"context"
	"errors"
	"fmt"
)

const (
	// ProbeRequestSchemaVersion 是探针请求使用的冻结 wire schema 版本。
	ProbeRequestSchemaVersion = "heteroci-probe-request/v2"
	// ProbeResponseSchemaVersion 是探针响应使用的冻结 wire schema 版本。
	ProbeResponseSchemaVersion = "heteroci-probe-response/v3"
)

// ProbeRequest 是发送给节点探针的严格请求载荷。
type ProbeRequest struct {
	// SchemaVersion 是本请求使用的 wire schema 版本。
	SchemaVersion string `json:"schema_version"`
	// NodeID 是请求目标节点的稳定标识。
	NodeID string `json:"node_id"`
	// ProbeIdentity 是请求方预期的探针稳定身份。
	ProbeIdentity string `json:"probe_identity"`
	// OwnerEpoch 是发起请求的路由 owner 生命周期。
	OwnerEpoch RoutingOwnerEpoch `json:"owner_epoch"`
	// CapturedAtUnixMilli 是 controller 为整次 SnapshotSet 冻结的统一采集时间。
	CapturedAtUnixMilli int64 `json:"captured_at_unix_milli"`
}

// ProbeResponse 是节点探针返回的严格响应载荷。
type ProbeResponse struct {
	// SchemaVersion 是本响应使用的 wire schema 版本。
	SchemaVersion string `json:"schema_version"`
	// OwnerEpoch 是响应对应的路由 owner 生命周期。
	OwnerEpoch RoutingOwnerEpoch `json:"owner_epoch"`
	// CapacityBasis 是 Total 与 Available 的容量扣减口径。
	CapacityBasis SnapshotCapacityBasis `json:"capacity_basis"`
	// NodeID 是响应节点的稳定标识。
	NodeID string `json:"node_id"`
	// Endpoint 是响应节点的连接端点。
	Endpoint string `json:"endpoint"`
	// Platform 是响应节点的规范平台标识。
	Platform Platform `json:"platform"`
	// ProbeIdentity 是响应探针的稳定身份。
	ProbeIdentity string `json:"probe_identity"`
	// PhysicalAttestation 是本次 fresh probe 取得的 Ubuntu physical-host live proof。
	PhysicalAttestation PhysicalHostAttestation `json:"physical_attestation"`
	// ProbeBootID 是响应探针当前启动实例标识。
	ProbeBootID string `json:"probe_boot_id"`
	// ActiveCapabilities 是探针当前已激活并验证的能力集合。
	ActiveCapabilities []ActiveCapabilitySnapshot `json:"active_capabilities"`
	// Status 是节点当前生命周期状态。
	Status NodeStatus `json:"status"`
	// HeartbeatUnixMilli 是探针心跳时间的 Unix 毫秒时间戳。
	HeartbeatUnixMilli int64 `json:"heartbeat_unix_milli"`
	// Total 是节点实测总容量。
	Total ResourceCapacity `json:"total"`
	// Available 是扣除外部占用后的可用容量。
	Available ResourceCapacity `json:"available"`
}

// ProbeResponseBody 是探针响应体的受控读取边界。
// 实现必须在每次 Read 中观察调用方传入的 context；context 取消时，Read 必须解除阻塞并返回。
// AcquireSnapshot 将同一 context 传给每次读取，并对每个非 nil body 恰好调用一次 Close；接口不要求重复 Close 幂等。
type ProbeResponseBody interface {
	Read(context.Context, []byte) (int, error)
	Close() error
}

// ProbeTransport 把严格编码的探针请求发送到节点端点。
type ProbeTransport interface {
	// Probe 发送请求并返回尚未消费的响应体；请求编码由调用方 core 负责。
	Probe(ctx context.Context, endpoint string, request []byte) (ProbeResponseBody, error)
}

// ErrProbeTransportUnavailable 表示探针传输实现当前不可用。
var ErrProbeTransportUnavailable = errors.New("heterogeneous CI probe transport unavailable")

// ProbeArgumentError 表示探针调用参数违反了输入约束。
type ProbeArgumentError struct {
	// Field 是违反约束的安全字段名。
	Field string
	// Cause 是参数校验失败的根因。
	Cause error
}

// Error 返回不包含端点、探针身份或请求内容的参数错误描述。
func (err ProbeArgumentError) Error() string {
	if err.Field == "" {
		return "heterogeneous CI probe argument is invalid"
	}
	return fmt.Sprintf("heterogeneous CI probe argument field %q is invalid", err.Field)
}

// Unwrap 返回参数校验失败的根因。
func (err ProbeArgumentError) Unwrap() error {
	return err.Cause
}

// ProbeTransportError 表示探针传输失败。
type ProbeTransportError struct {
	// NodeID 是失败节点的安全稳定标识。
	NodeID string
	// Cause 是传输失败的根因。
	Cause error
}

// Error 返回不包含端点、探针身份或响应内容的传输错误描述。
func (err ProbeTransportError) Error() string {
	if err.NodeID == "" {
		return "heterogeneous CI probe transport failed"
	}
	return fmt.Sprintf("heterogeneous CI probe transport failed for node %q", err.NodeID)
}

// Unwrap 返回传输失败的根因。
func (err ProbeTransportError) Unwrap() error {
	return err.Cause
}

// ProbeMalformedError 表示探针响应无法按 wire 格式解析。
type ProbeMalformedError struct {
	// NodeID 是响应节点的安全稳定标识。
	NodeID string
	// Cause 是响应格式错误的根因。
	Cause error
}

// Error 返回不包含端点、探针身份或响应内容的格式错误描述。
func (err ProbeMalformedError) Error() string {
	if err.NodeID == "" {
		return "heterogeneous CI probe response is malformed"
	}
	return fmt.Sprintf("heterogeneous CI probe response for node %q is malformed", err.NodeID)
}

// Unwrap 返回响应格式错误的根因。
func (err ProbeMalformedError) Unwrap() error {
	return err.Cause
}

// ProbeProtocolError 表示探针响应违反协议字段约束。
type ProbeProtocolError struct {
	// NodeID 是响应节点的安全稳定标识。
	NodeID string
	// Field 是违反约束的安全字段名。
	Field string
	// Cause 是协议校验失败的根因。
	Cause error
}

// Error 返回不包含端点、探针身份或响应内容的协议错误描述。
func (err ProbeProtocolError) Error() string {
	if err.NodeID == "" && err.Field == "" {
		return "heterogeneous CI probe protocol violation"
	}
	if err.Field == "" {
		return fmt.Sprintf("heterogeneous CI probe protocol violation for node %q", err.NodeID)
	}
	if err.NodeID == "" {
		return fmt.Sprintf("heterogeneous CI probe protocol field %q is invalid", err.Field)
	}
	return fmt.Sprintf("heterogeneous CI probe protocol field %q is invalid for node %q", err.Field, err.NodeID)
}

// Unwrap 返回协议校验失败的根因。
func (err ProbeProtocolError) Unwrap() error {
	return err.Cause
}

// ProbeIdentityDriftError 表示响应身份与请求或清单不一致。
type ProbeIdentityDriftError struct {
	// NodeID 是发生身份漂移的安全节点标识。
	NodeID string
	// Field 是发生漂移的安全字段名。
	Field string
	// Cause 是身份漂移的根因。
	Cause error
}

// Error 返回不包含端点、探针身份或响应内容的身份漂移描述。
func (err ProbeIdentityDriftError) Error() string {
	if err.NodeID == "" && err.Field == "" {
		return "heterogeneous CI probe identity drift"
	}
	if err.Field == "" {
		return fmt.Sprintf("heterogeneous CI probe identity drift for node %q", err.NodeID)
	}
	if err.NodeID == "" {
		return fmt.Sprintf("heterogeneous CI probe identity drift at field %q", err.Field)
	}
	return fmt.Sprintf("heterogeneous CI probe identity drift at field %q for node %q", err.Field, err.NodeID)
}

// Unwrap 返回身份漂移的根因。
func (err ProbeIdentityDriftError) Unwrap() error {
	return err.Cause
}
