package contract

import (
	"context"
	"errors"
	"fmt"
)

// ErrRoomCapabilityNotSupported 是 Room 尚未具备完整祖先时的稳定错误哨兵。
var ErrRoomCapabilityNotSupported = errors.New("capability_not_supported")

// RoomCapability 是 Room 领域的固定能力集合；名称不随 provider 或工具列表动态变化。
type RoomCapability string

const (
	RoomManageCapability    RoomCapability = "room.manage"
	RoomGetCapability       RoomCapability = "room.get"
	RoomBroadcastCapability RoomCapability = "room.broadcast"
	RoomRoundCapability     RoomCapability = "room.round"
	RoomCloseCapability     RoomCapability = "room.close"
)

// Valid 判断能力是否属于 Room 合同的闭合集合。
func (c RoomCapability) Valid() bool {
	switch c {
	case RoomManageCapability, RoomGetCapability, RoomBroadcastCapability, RoomRoundCapability, RoomCloseCapability:
		return true
	default:
		return false
	}
}

// RoomCapabilityDisabledError 描述 disabled gate 的固定能力与脱敏原因。
type RoomCapabilityDisabledError struct {
	Capability RoomCapability
	Reason     string
}

// Error 只回显白名单能力和原因，避免把内部路径、凭据或 provider 信息泄漏到 wire。
func (e RoomCapabilityDisabledError) Error() string {
	capability := "capability_unknown"
	if e.Capability.Valid() {
		capability = string(e.Capability)
	}
	reason := roomCapabilityReason(e.Reason)
	return fmt.Sprintf("%s: subject=room capability=%s reason=%s", ErrRoomCapabilityNotSupported, capability, reason)
}

// Unwrap 允许统一 tool/error catalog 用 errors.Is 识别 capability_not_supported。
func (e RoomCapabilityDisabledError) Unwrap() error { return ErrRoomCapabilityNotSupported }

// NewRoomCapabilityDisabled 创建不携带敏感上下文的 Room disabled 错误。
func NewRoomCapabilityDisabled(capability RoomCapability, reason string) error {
	return RoomCapabilityDisabledError{Capability: capability, Reason: roomCapabilityReason(reason)}
}

// RoomCapabilityGate 是未来 Room owner 的窄接入点；disabled 实现不拥有任何 mutation。
type RoomCapabilityGate interface {
	Require(context.Context, RoomCapability) error
}

// DisabledRoomCapabilityGate 在 Room 祖先未闭合期间始终 fail-closed，不写数据库或调用 provider。
type DisabledRoomCapabilityGate struct{}

// Require 检查 context 后返回稳定的 disabled 错误；未知能力不会被当作已启用。
func (DisabledRoomCapabilityGate) Require(ctx context.Context, capability RoomCapability) error {
	if ctx == nil {
		return errors.New("room capability gate requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !capability.Valid() {
		return NewRoomCapabilityDisabled(capability, "unknown_capability")
	}
	return NewRoomCapabilityDisabled(capability, "room_ancestors_unclosed")
}

func roomCapabilityReason(reason string) string {
	switch reason {
	case "unknown_capability", "room_ancestors_unclosed", "draft_dependency", "owner_unwired", "receipt_missing", "principal_missing":
		return reason
	default:
		return "disabled"
	}
}
