package room

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// DisabledService is the sole candidate implementation. It never writes state,
// contacts a provider, opens SQLite, or submits to RecoveryCustody.
type DisabledService struct{}

// NewDisabledService 创建唯一的 fail-closed Room service 候选。
func NewDisabledService() *DisabledService { return &DisabledService{} }

// Candidate is the explicit disabled-by-default Room candidate exposed to
// future adapters. Enabled is intentionally a constant false; there is no
// local switch that can promote this contract into a production owner.
type Candidate struct{}

// NewCandidate 创建默认 disabled 且不可本地升级的 Room candidate。
func NewCandidate() Candidate { return Candidate{} }

// Enabled 明确报告 candidate 未取得生产启用资格。
func (Candidate) Enabled() bool { return false }

// Submit 在校验 operation 后始终返回 typed capability-not-supported。
func (Candidate) Submit(ctx context.Context, operation Operation) (Operation, error) {
	return (&DisabledService{}).Submit(ctx, operation)
}

// ValidateFanout 校验 DTO 后拒绝任何尚未获批的 Room fan-out。
func (Candidate) ValidateFanout(ctx context.Context, operation Operation, result FanoutResult) error {
	return (&DisabledService{}).ValidateFanout(ctx, operation, result)
}

// Submit 校验取消和 DTO 后始终 fail-closed，不创建 mutation。
func (s *DisabledService) Submit(ctx context.Context, operation Operation) (Operation, error) {
	if ctx == nil {
		return Operation{}, errors.New("room disabled service requires context")
	}
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	if err := operation.Validate(); err != nil {
		return Operation{}, err
	}
	return Operation{}, disabledError(operation.Kind)
}

// ValidateFanout 校验 fan-out receipt 形状后返回 disabled typed error。
func (s *DisabledService) ValidateFanout(ctx context.Context, operation Operation, result FanoutResult) error {
	if ctx == nil {
		return errors.New("room disabled service requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	if operation.Kind != OperationBroadcast && operation.Kind != OperationRound {
		return ErrInvalidDTO
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if err := validateFanoutOperationBinding(operation, result); err != nil {
		return err
	}
	return disabledError(operation.Kind)
}

func validateFanoutOperationBinding(operation Operation, result FanoutResult) error {
	if result.RoomID != operation.RoomID {
		return fmt.Errorf("%w: fanout result room id does not match operation", ErrOperationConflict)
	}
	if result.OperationID != operation.OperationID {
		return fmt.Errorf("%w: fanout result operation id does not match operation", ErrOperationConflict)
	}
	if result.OperationDigest != "" && result.OperationDigest != operation.RequestDigest {
		return fmt.Errorf("%w: fanout result operation digest does not match operation", ErrOperationConflict)
	}
	return validateFanoutReceiptBindings(operation, result.Receipts)
}

func validateFanoutReceiptBindings(operation Operation, receipts []TargetReceipt) error {
	for _, receipt := range receipts {
		if err := validateFanoutReceiptBinding(operation, receipt); err != nil {
			return err
		}
	}
	return nil
}

func validateFanoutReceiptBinding(operation Operation, receipt TargetReceipt) error {
	if receipt.OperationID != "" && receipt.OperationID != operation.OperationID {
		return fmt.Errorf("%w: target receipt operation id does not match operation", ErrOperationConflict)
	}
	if receipt.OperationDigest != "" && receipt.OperationDigest != operation.RequestDigest {
		return fmt.Errorf("%w: target receipt operation digest does not match operation", ErrOperationConflict)
	}
	return nil
}

func disabledError(kind string) error {
	return fmt.Errorf("%w: %w", ErrCapabilityDisabled, contract.NewRoomCapabilityDisabled(capabilityFor(kind), "room_candidate_disabled"))
}

// capabilityFor 将 operation kind 映射到内部固定 Room capability 集合。
func capabilityFor(kind string) contract.RoomCapability {
	switch kind {
	case OperationCreate, OperationJoin, OperationLeave:
		return contract.RoomManageCapability
	case OperationGet:
		return contract.RoomGetCapability
	case OperationBroadcast:
		return contract.RoomBroadcastCapability
	case OperationRound:
		return contract.RoomRoundCapability
	case OperationClose, OperationExpire, OperationCleanup:
		return contract.RoomCloseCapability
	default:
		return contract.RoomManageCapability
	}
}

// RequireService rejects nil and typed-nil implementations. A non-nil service
// remains a contract seam; this helper does not mark Room as production-ready.
// RequireService 拒绝 nil/typed-nil service，保持候选装配 fail-closed。
func RequireService(service Service) error {
	if service == nil {
		return ErrCapabilityDisabled
	}
	value := reflect.ValueOf(service)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return ErrCapabilityDisabled
		}
	}
	return nil
}

var _ Service = (*DisabledService)(nil)
