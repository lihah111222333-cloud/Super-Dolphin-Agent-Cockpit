package trustcandidate

import "fmt"

// NewCanonicalEvent 在 ticket 绑定且 untrusted quoted text/metadata 时创建事件候选。
func NewCanonicalEvent(envelope TrustEnvelope, ticket ContentTicket) (CanonicalEvent, error) {
	if err := envelope.ValidateForCanonicalEvent(); err != nil {
		return CanonicalEvent{}, err
	}
	if err := ticket.ValidateAgainst(envelope); err != nil {
		return CanonicalEvent{}, err
	}
	event := CanonicalEvent{SchemaVersion: SchemaVersion, Envelope: envelope, ContentTicketID: ticket.TicketID}
	if err := event.Validate(ticket); err != nil {
		return CanonicalEvent{}, fmt.Errorf("%w: canonical event candidate failed validation", err)
	}
	return event, nil
}

// ToCanonicalEvent 是 NewCanonicalEvent 的 envelope 方法形式。
func (e TrustEnvelope) ToCanonicalEvent(ticket ContentTicket) (CanonicalEvent, error) {
	return NewCanonicalEvent(e, ticket)
}

// NewCanonicalControl 明确不构造 control candidate；签名/信任根 owner 在包外。
func NewCanonicalControl(envelope TrustEnvelope) (CanonicalControl, error) {
	if err := envelope.Validate(); err != nil {
		return CanonicalControl{}, err
	}
	return CanonicalControl{}, ErrControlRejected
}

// ToCanonicalControl 是 NewCanonicalControl 的 envelope 方法形式。
func (e TrustEnvelope) ToCanonicalControl() (CanonicalControl, error) {
	return NewCanonicalControl(e)
}
