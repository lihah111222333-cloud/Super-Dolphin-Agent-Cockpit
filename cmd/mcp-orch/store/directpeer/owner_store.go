package directpeer

import (
	"context"
	"crypto/subtle"
	"errors"
)

// ownerStore isolates owner-only state transitions while its methods remain
// promoted through Store for compatibility with existing callers.
type ownerStore struct {
	parent *Store
}

// OwnerCapability 返回绑定当前 Store 实例的 owner 能力。
func (o *ownerStore) OwnerCapability() (OwnerCapability, error) {
	if o == nil || o.parent == nil || o.parent.db == nil {
		return OwnerCapability{}, errors.New("direct peer store database is required")
	}
	return OwnerCapability{store: o.parent, token: o.parent.ownerToken}, nil
}

// validateOwnerCapability 校验 owner 能力是否绑定当前 Store 且令牌匹配。
func (o *ownerStore) validateOwnerCapability(capability OwnerCapability) error {
	if o == nil || o.parent == nil || o.parent.db == nil || capability.store != o.parent || subtle.ConstantTimeCompare(capability.token[:], o.parent.ownerToken[:]) != 1 {
		return ErrOwnerCapability
	}
	return nil
}

// MarkSubmittedByOwner 由 owner 能力记录 typed provider submission。
func (o *ownerStore) MarkSubmittedByOwner(ctx context.Context, capability OwnerCapability, messageID, submissionID string) (Message, error) {
	if err := o.validateOwnerCapability(capability); err != nil {
		return Message{}, err
	}
	return o.parent.markSubmitted(ctx, messageID, submissionID)
}

// MarkDeliveryFailedByOwner 由 owner 能力记录明确的提交前投递失败。
func (o *ownerStore) MarkDeliveryFailedByOwner(ctx context.Context, capability OwnerCapability, messageID, failure string) (Message, error) {
	if err := o.validateOwnerCapability(capability); err != nil {
		return Message{}, err
	}
	return o.parent.markDeliveryFailed(ctx, messageID, failure)
}
