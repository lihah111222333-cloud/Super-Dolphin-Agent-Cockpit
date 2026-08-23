package heteroci

import (
	"fmt"
	"math"
)

const (
	// RoutingOwnerIDHexLength 是 owner identity 的固定小写十六进制长度。
	RoutingOwnerIDHexLength = 64
	// OperationIDHexLength 是幂等 operation identity 的固定小写十六进制长度。
	OperationIDHexLength = 64
)

// RoutingOwnerScope 标识 durable owner 所属的逻辑作用域。
type RoutingOwnerScope string

// RoutingOwnerID 标识一个 durable owner incarnation 的 owner identity。
type RoutingOwnerID string

// RoutingOwnerEpoch 标识一次 RoutingLeaseBook owner 生命周期。
type RoutingOwnerEpoch int64

// RoutingOwnerRevision 是 scope 内全局单调递增的 durable mutation revision。
type RoutingOwnerRevision int64

// OperationID 是 durable mutation 的幂等 identity。
type OperationID string

// OwnerFence 是 mutation 使用的四元 owner fencing 值对象。
type OwnerFence struct {
	Scope      RoutingOwnerScope    `json:"scope"`
	OwnerID    RoutingOwnerID       `json:"owner_id"`
	OwnerEpoch RoutingOwnerEpoch    `json:"owner_epoch"`
	Revision   RoutingOwnerRevision `json:"revision"`
}

// ParseRoutingOwnerScope 解析已计算出的 canonical scope digest。
func ParseRoutingOwnerScope(raw string) (RoutingOwnerScope, error) {
	scope := RoutingOwnerScope(raw)
	if err := scope.Validate(); err != nil {
		return "", err
	}
	return scope, nil
}

// Validate 检查 scope 是否为 canonical sha256 digest。
func (scope RoutingOwnerScope) Validate() error {
	if !isSHA256Digest(string(scope)) {
		return fmt.Errorf("routing owner scope is invalid")
	}
	return nil
}

// ParseRoutingOwnerID 解析固定长度的小写 owner identity。
func ParseRoutingOwnerID(raw string) (RoutingOwnerID, error) {
	ownerID := RoutingOwnerID(raw)
	if err := ownerID.Validate(); err != nil {
		return "", err
	}
	return ownerID, nil
}

// Validate 检查 owner identity 的固定十六进制形状。
func (ownerID RoutingOwnerID) Validate() error {
	if !validLowerHex(string(ownerID), RoutingOwnerIDHexLength) {
		return fmt.Errorf("routing owner id is invalid")
	}
	return nil
}

// NewRoutingOwnerEpoch 创建并校验 owner epoch。
func NewRoutingOwnerEpoch(value int64) (RoutingOwnerEpoch, error) {
	epoch := RoutingOwnerEpoch(value)
	if err := epoch.Validate(); err != nil {
		return 0, err
	}
	return epoch, nil
}

// Validate 检查 owner epoch 的 durable 边界。
func (epoch RoutingOwnerEpoch) Validate() error {
	if epoch <= 0 || epoch >= RoutingOwnerEpoch(math.MaxInt64) {
		return fmt.Errorf("routing owner epoch is invalid")
	}
	return nil
}

// NewRoutingOwnerRevision 创建并校验 mutation revision。
func NewRoutingOwnerRevision(value int64) (RoutingOwnerRevision, error) {
	revision := RoutingOwnerRevision(value)
	if err := revision.Validate(); err != nil {
		return 0, err
	}
	return revision, nil
}

// Validate 检查 revision 的 durable 边界。
func (revision RoutingOwnerRevision) Validate() error {
	if revision <= 0 || revision >= RoutingOwnerRevision(math.MaxInt64) {
		return fmt.Errorf("routing owner revision is invalid")
	}
	return nil
}

// ParseOperationID 解析固定长度的小写 operation identity。
func ParseOperationID(raw string) (OperationID, error) {
	operationID := OperationID(raw)
	if err := operationID.Validate(); err != nil {
		return "", err
	}
	return operationID, nil
}

// Validate 检查 operation identity 的固定十六进制形状。
func (operationID OperationID) Validate() error {
	if !validLowerHex(string(operationID), OperationIDHexLength) {
		return fmt.Errorf("routing operation id is invalid")
	}
	return nil
}

// NewOwnerFence 创建并校验四元 owner fence。
func NewOwnerFence(scope RoutingOwnerScope, ownerID RoutingOwnerID, ownerEpoch RoutingOwnerEpoch, revision RoutingOwnerRevision) (OwnerFence, error) {
	fence := OwnerFence{Scope: scope, OwnerID: ownerID, OwnerEpoch: ownerEpoch, Revision: revision}
	if err := fence.Validate(); err != nil {
		return OwnerFence{}, err
	}
	return fence, nil
}

// Validate 检查 owner fence 的字段和 revision/epoch 关系。
func (fence OwnerFence) Validate() error {
	if err := fence.Scope.Validate(); err != nil {
		return err
	}
	if err := fence.OwnerID.Validate(); err != nil {
		return err
	}
	if err := fence.OwnerEpoch.Validate(); err != nil {
		return err
	}
	if err := fence.Revision.Validate(); err != nil {
		return err
	}
	if fence.Revision < RoutingOwnerRevision(fence.OwnerEpoch) {
		return fmt.Errorf("routing owner revision precedes epoch")
	}
	return nil
}

// validLowerHex 检查字符串是否为指定长度的 ASCII 小写十六进制。
func validLowerHex(value string, wantLength int) bool {
	if len(value) != wantLength {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' {
			continue
		}
		if character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
