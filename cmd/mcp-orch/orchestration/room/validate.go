package room

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Validate 校验认证快照，拒绝 unsigned metadata，也不从 membership 推导 grant。
func (p Principal) Validate() error {
	for _, item := range []struct{ name, value string }{
		{"principal_ref", p.PrincipalRef}, {"scope_ref", p.ScopeRef},
		{"actor_principal", p.ActorPrincipal}, {"issuer_binding", p.IssuerBinding},
	} {
		if err := validateText(ErrAuthorization, item.name, item.value, 512); err != nil {
			return err
		}
	}
	if p.ActorPrincipal != "authenticated_subject" && p.ActorPrincipal != "system_scheduler" {
		return fmt.Errorf("%w: actor_principal is not registered", ErrAuthorization)
	}
	switch p.IssuerBinding {
	case "provider_bridge", "managed_runtime", "a2a_gateway", "controller_connection":
	default:
		return fmt.Errorf("%w: issuer binding is not registered", ErrAuthorization)
	}
	if err := validateDigest(ErrAuthorization, "grant_digest", p.GrantDigest); err != nil {
		return err
	}
	return nil
}

// Validate 校验 Room 的固定状态、owner 代际和 scope 字段边界。
func (r Room) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version must equal 1", ErrInvalidState)
	}
	if err := validateRoomIdentity(r); err != nil {
		return err
	}
	return validateRoomState(r)
}

// validateRoomIdentity 校验 Room 的 owner、workspace 和 run scope 文本。
func validateRoomIdentity(r Room) error {
	for _, item := range []struct{ name, value string }{
		{"room_id", r.RoomID}, {"owner_id", r.OwnerID}, {"workspace_id", r.WorkspaceID}, {"run_scope", r.RunScope},
	} {
		if err := validateText(ErrInvalidState, item.name, item.value, 256); err != nil {
			return err
		}
	}
	return nil
}

// validateRoomState 校验 Room 的代际、摘要和 lifecycle/custody 组合。
func validateRoomState(r Room) error {
	if err := validateRoomCounters(r); err != nil {
		return err
	}
	if err := validateRoomLifecycle(r); err != nil {
		return err
	}
	return nil
}

// validateRoomCounters 校验 Room 代际、cursor 和 membership 摘要。
func validateRoomCounters(r Room) error {
	if r.TTLUnix <= 0 || r.DispatchGeneration <= 0 || r.MembershipRevision <= 0 || r.SharedEventCursor < 0 {
		return fmt.Errorf("%w: generations and ttl must be positive", ErrInvalidState)
	}
	if err := validateDigest(ErrInvalidState, "membership_snapshot_hash", r.MembershipSnapshotHash); err != nil {
		return err
	}
	return nil
}

// validateRoomLifecycle 校验 Room status/custody 组合及 close reason。
func validateRoomLifecycle(r Room) error {
	if !validRoomStatus(r.Status) || !validCustody(r.CustodyResolution) {
		return ErrInvalidState
	}
	if err := validateRoomLifecycleState(r); err != nil {
		return err
	}
	return validateRoomCloseReason(r)
}

func validateRoomLifecycleState(r Room) error {
	switch r.Status {
	case StatusOpen:
		return validateOpenRoomCustody(r.CustodyResolution)
	case StatusClosingPending:
		return validateClosingRoomCustody(r.CustodyResolution)
	case StatusClosed:
		return validateClosedRoomCustody(r.CustodyResolution, r.CloseReason)
	}
	return nil
}

func validateOpenRoomCustody(custody string) error {
	if custody == CustodyPending {
		return fmt.Errorf("%w: open room cannot have pending custody", ErrInvalidState)
	}
	return nil
}

func validateClosingRoomCustody(custody string) error {
	if custody != CustodyPending && custody != CustodyAvailable {
		return fmt.Errorf("%w: closing room custody must be pending or available", ErrInvalidState)
	}
	return nil
}

func validateClosedRoomCustody(custody, reason string) error {
	if custody != CustodyAvailable {
		return fmt.Errorf("%w: closed room custody must be available", ErrInvalidState)
	}
	if reason == "" {
		return fmt.Errorf("%w: closed room requires close_reason", ErrInvalidState)
	}
	return nil
}

func validateRoomCloseReason(r Room) error {
	if r.Status != StatusClosed && r.CloseReason != "" {
		return fmt.Errorf("%w: close_reason is only valid for closed rooms", ErrInvalidState)
	}
	if r.CloseReason != "" {
		if err := validateText(ErrInvalidState, "close_reason", r.CloseReason, 256); err != nil {
			return err
		}
	}
	return nil
}

// Validate 校验 operation 形状及 self-digest；mutation 的 revision 由 Authorize 强制。
func (o Operation) Validate() error {
	if o.SchemaVersion != SchemaVersion || o.OperationVersion <= 0 {
		return fmt.Errorf("%w: unsupported schema or operation version", ErrInvalidDTO)
	}
	if err := validateOperationIdentity(o); err != nil {
		return err
	}
	if err := validateOperationRevisions(o); err != nil {
		return err
	}
	return validateOperationBinding(o)
}

// validateOperationIdentity 校验 operation 的固定文本和 kind 枚举。
func validateOperationIdentity(o Operation) error {
	for _, item := range []struct{ name, value string }{{"room_id", o.RoomID}, {"operation_id", o.OperationID}, {"kind", o.Kind}} {
		if err := validateText(ErrInvalidDTO, item.name, item.value, 256); err != nil {
			return err
		}
	}
	if !validOperationKind(o.Kind) {
		return fmt.Errorf("%w: unknown operation kind", ErrInvalidDTO)
	}
	return nil
}

// validateOperationRevisions 校验 caller 提供的 CAS revision 不得为负数。
func validateOperationRevisions(o Operation) error {
	if o.ExpectedMembershipRevision < 0 || o.ExpectedDispatchGeneration < 0 {
		return ErrInvalidDTO
	}
	return nil
}

// validateOperationBinding 校验幂等摘要、principal 和 request digest 绑定。
func validateOperationBinding(o Operation) error {
	if err := validateDigest(ErrInvalidDTO, "idempotency_key_digest", o.IdempotencyKeyDigest); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidDTO, "request_digest", o.RequestDigest); err != nil {
		return err
	}
	if err := o.Principal.Validate(); err != nil {
		return err
	}
	want, err := ComputeOperationDigest(o)
	if err != nil || want != o.RequestDigest {
		return fmt.Errorf("%w: request_digest mismatch", ErrInvalidDTO)
	}
	return nil
}

// Authorize 将 operation 绑定到精确 Room scope，不把 membership 当作 peer grant。
func (o Operation) Authorize(r Room) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := o.Validate(); err != nil {
		return err
	}
	if err := authorizeScope(o, r); err != nil {
		return err
	}
	if err := authorizeActor(o); err != nil {
		return err
	}
	if err := authorizeRevisions(o, r); err != nil {
		return err
	}
	return authorizeStatus(o, r)
}

// authorizeScope 校验 Room ID 与 run scope 的逐字绑定。
func authorizeScope(o Operation, r Room) error {
	if o.RoomID != r.RoomID || o.Principal.ScopeRef != r.RunScope {
		return fmt.Errorf("%w: room or scope mismatch", ErrAuthorization)
	}
	return nil
}

// authorizeActor 限制 scheduler 只执行 expire/cleanup。
func authorizeActor(o Operation) error {
	if o.Principal.ActorPrincipal == "system_scheduler" && o.Kind != OperationExpire && o.Kind != OperationCleanup {
		return fmt.Errorf("%w: scheduler may only expire or cleanup", ErrAuthorization)
	}
	return nil
}

// authorizeRevisions 校验 close、join、leave、broadcast、round 的 CAS revision。
func authorizeRevisions(o Operation, r Room) error {
	if o.ExpectedDispatchGeneration != r.DispatchGeneration {
		return fmt.Errorf("%w: stale dispatch generation", ErrAuthorization)
	}
	if requiresMembershipRevision(o.Kind) && o.ExpectedMembershipRevision != r.MembershipRevision {
		return fmt.Errorf("%w: stale membership revision", ErrAuthorization)
	}
	if requiresMembershipRevision(o.Kind) && o.ExpectedMembershipRevision <= 0 {
		return fmt.Errorf("%w: membership mutation requires revision", ErrAuthorization)
	}
	return nil
}

// authorizeStatus 禁止已 closing/closed Room 接受新的公开 mutation。
func authorizeStatus(o Operation, r Room) error {
	if r.Status != StatusOpen && o.Kind != OperationGet && o.Kind != OperationCleanup {
		return fmt.Errorf("%w: room is not open", ErrAuthorization)
	}
	return nil
}

// requiresDispatchRevision 判断 operation 是否必须携带 dispatch generation。
func requiresDispatchRevision(kind string) bool {
	return kind == OperationBroadcast || kind == OperationRound || kind == OperationClose
}

// requiresMembershipRevision 判断 join/leave 是否必须携带 membership revision。
func requiresMembershipRevision(kind string) bool {
	return kind == OperationJoin || kind == OperationLeave
}

// Validate 校验 candidate 的固定状态、代际与 inventory 摘要。
func (c AdmissionCandidate) Validate() error {
	if c.SchemaVersion != SchemaVersion || c.OperationVersion <= 0 || c.ExpectedGeneration < 0 || c.DispatchGeneration <= 0 {
		return fmt.Errorf("%w: invalid admission version or generation", ErrInvalidDTO)
	}
	if err := validateCandidateIdentity(c); err != nil {
		return err
	}
	if err := validateCandidateTransition(c); err != nil {
		return err
	}
	return validateCandidateInventory(c)
}

// validateCandidateIdentity 校验 candidate room、operation 和摘要身份。
func validateCandidateIdentity(c AdmissionCandidate) error {
	for _, item := range []struct{ name, value string }{{"room_id", c.RoomID}, {"operation_id", c.OperationID}} {
		if err := validateText(ErrInvalidDTO, item.name, item.value, 256); err != nil {
			return err
		}
	}
	if err := validateDigest(ErrInvalidDTO, "operation_digest", c.OperationDigest); err != nil {
		return err
	}
	return nil
}

// validateCandidateTransition 校验 candidate 不得直接安装 closed 状态。
func validateCandidateTransition(c AdmissionCandidate) error {
	if !validRoomStatus(c.NextState) || c.NextState == StatusClosed {
		return ErrInvalidState
	}
	if c.NextCustodyResolution != CustodyPending {
		return ErrInvalidState
	}
	return nil
}

// validateCandidateInventory 校验 provisional inventory 的非负计数和摘要。
func validateCandidateInventory(c AdmissionCandidate) error {
	if c.PreviousInventoryCount < 0 || c.CommandInventoryCount < 0 {
		return ErrInvalidState
	}
	if err := validateDigest(ErrInvalidState, "previous_inventory_root", c.PreviousInventoryRoot); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidState, "command_inventory_root", c.CommandInventoryRoot); err != nil {
		return err
	}
	return nil
}

// Validate 校验 admission head 的单链状态与 pending winner presence。
func (h AdmissionHead) Validate() error {
	if h.SchemaVersion != SchemaVersion || h.Generation < 0 || h.DispatchGeneration <= 0 {
		return fmt.Errorf("%w: invalid head generation", ErrInvalidState)
	}
	if err := validateHeadIdentity(h); err != nil {
		return err
	}
	if err := validateHeadInventory(h); err != nil {
		return err
	}
	return validateHeadWinner(h)
}

// validateHeadIdentity 校验 head room、状态和 custody 组合。
func validateHeadIdentity(h AdmissionHead) error {
	if err := validateText(ErrInvalidState, "room_id", h.RoomID, 256); err != nil {
		return err
	}
	if !validRoomStatus(h.State) || !validCustody(h.CustodyResolution) {
		return ErrInvalidState
	}
	if err := validateHeadOperation(h); err != nil {
		return err
	}
	return validateHeadState(h)
}

// validateHeadOperation 校验 head 中 operation 身份字段的一致性。
func validateHeadOperation(h AdmissionHead) error {
	if h.OperationID == "" {
		if h.OperationVersion != 0 || h.OperationDigest != "" {
			return fmt.Errorf("%w: head operation identity must be all absent or present", ErrInvalidState)
		}
		return nil
	}
	if err := validateText(ErrInvalidState, "operation_id", h.OperationID, 256); err != nil {
		return err
	}
	if h.OperationVersion <= 0 {
		return fmt.Errorf("%w: head operation_version must be positive", ErrInvalidState)
	}
	return validateDigest(ErrInvalidState, "operation_digest", h.OperationDigest)
}

// validateHeadState 校验 head 状态与 custody 的生命周期组合。
func validateHeadState(h AdmissionHead) error {
	switch h.State {
	case StatusOpen:
		// open+pending is the provisional fan-out admission state and must
		// retain an operation winner (checked below).
	case StatusClosingPending:
		if h.CustodyResolution != CustodyPending && h.CustodyResolution != CustodyAvailable {
			return fmt.Errorf("%w: closing head custody must be pending or available", ErrInvalidState)
		}
	case StatusClosed:
		if h.CustodyResolution != CustodyAvailable {
			return fmt.Errorf("%w: closed head custody must be available", ErrInvalidState)
		}
	}
	return nil
}

// validateHeadInventory 校验 head inventory root 与 count 的边界。
func validateHeadInventory(h AdmissionHead) error {
	if h.PreviousInventoryCount < 0 || h.CommandInventoryCount < 0 {
		return ErrInvalidState
	}
	if err := validateDigest(ErrInvalidState, "previous_inventory_root", h.PreviousInventoryRoot); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidState, "command_inventory_root", h.CommandInventoryRoot); err != nil {
		return err
	}
	return nil
}

// validateHeadWinner 校验 pending custody 必须绑定 operation winner。
func validateHeadWinner(h AdmissionHead) error {
	if h.CustodyResolution == CustodyPending {
		if h.OperationID == "" || h.OperationDigest == "" {
			return fmt.Errorf("%w: pending head lacks winner", ErrInvalidState)
		}
		if err := validateDigest(ErrInvalidState, "operation_digest", h.OperationDigest); err != nil {
			return err
		}
		if h.ReceiptDigest != "" {
			return fmt.Errorf("%w: pending head cannot carry receipt digest", ErrInvalidState)
		}
	} else if h.ReceiptDigest != "" {
		if err := validateDigest(ErrInvalidState, "receipt_digest", h.ReceiptDigest); err != nil {
			return err
		}
	}
	return nil
}

// Validate 校验 admission result 的 operation/result identity binding。
// A result may omit ReceiptDigest while custody is still pending; once present
// it remains an opaque, content-addressed proof for the exact winning head.
func (r AdmissionResult) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.Generation < 0 {
		return ErrInvalidState
	}
	if err := validateAdmissionResultIdentity(r); err != nil {
		return err
	}
	return validateAdmissionResultReceipt(r)
}

// validateAdmissionResultIdentity 校验 admission result 的文本与 digest 绑定。
func validateAdmissionResultIdentity(r AdmissionResult) error {
	for _, item := range []struct {
		name, value string
	}{
		{"room_id", r.RoomID}, {"operation_id", r.OperationID}, {"operation_digest", r.OperationDigest}, {"admission_digest", r.AdmissionDigest},
	} {
		if item.name == "operation_digest" || item.name == "admission_digest" {
			if err := validateDigest(ErrInvalidState, item.name, item.value); err != nil {
				return err
			}
			continue
		}
		if err := validateText(ErrInvalidState, item.name, item.value, 256); err != nil {
			return err
		}
	}
	if r.OperationDigest != r.AdmissionDigest {
		return fmt.Errorf("%w: admission digest is not bound to operation digest", ErrInvalidState)
	}
	return nil
}

func validateAdmissionResultReceipt(r AdmissionResult) error {
	if r.ReceiptDigest != "" {
		if err := validateDigest(ErrInvalidState, "receipt_digest", r.ReceiptDigest); err != nil {
			return err
		}
	}
	return nil
}

// Validate 校验 target receipt 的状态、资源决策与 receipt/error presence。
func (r TargetReceipt) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return ErrTargetReceipt
	}
	if r.ErrorCode == "capability_not_supported" {
		return fmt.Errorf("%w: capability_not_supported is a typed error, not a receipt", ErrTargetReceipt)
	}
	if err := validateReceiptIdentity(r); err != nil {
		return err
	}
	return validateReceiptStatus(r)
}

// validateReceiptIdentity 校验 target mapping、delivery branch 与 resource decision。
func validateReceiptIdentity(r TargetReceipt) error {
	if err := validateReceiptOperationIdentity(r); err != nil {
		return err
	}
	if err := validateReceiptTargetIdentity(r); err != nil {
		return err
	}
	return validateReceiptDeliveryIdentity(r)
}

// validateReceiptOperationIdentity 校验 receipt 可选 operation 身份的绑定。
func validateReceiptOperationIdentity(r TargetReceipt) error {
	if r.OperationID != "" {
		if err := validateText(ErrTargetReceipt, "operation_id", r.OperationID, 256); err != nil {
			return err
		}
	}
	if r.OperationDigest != "" {
		if r.OperationID == "" {
			return fmt.Errorf("%w: operation_digest requires operation_id", ErrTargetReceipt)
		}
		if err := validateDigest(ErrTargetReceipt, "operation_digest", r.OperationDigest); err != nil {
			return err
		}
	}
	return nil
}

func validateReceiptTargetIdentity(r TargetReceipt) error {
	if err := validateText(ErrTargetReceipt, "target_id", r.TargetID, 256); err != nil {
		return err
	}
	for _, item := range []struct{ name, value string }{{"target_mapping_digest", r.TargetMappingDigest}, {"delivery_branch", r.DeliveryBranch}, {"resource_decision", r.ResourceDecision}} {
		if err := validateText(ErrTargetReceipt, item.name, item.value, 512); err != nil {
			return err
		}
	}
	return nil
}

func validateReceiptDeliveryIdentity(r TargetReceipt) error {
	if r.DeliveryBranch != "uniform" && r.DeliveryBranch != "per_target" {
		return fmt.Errorf("%w: unsupported delivery branch", ErrTargetReceipt)
	}
	switch r.ResourceDecision {
	case "reserved", "denied", "exhausted", "quarantined":
	default:
		return fmt.Errorf("%w: unsupported resource decision", ErrTargetReceipt)
	}
	return nil
}

// validateReceiptStatus 校验不同 receipt status 的字段 presence 互斥。
func validateReceiptStatus(r TargetReceipt) error {
	switch r.Status {
	case ReceiptAccepted, ReceiptProviderAcknowledged, ReceiptObservedInContext:
		return validateAcceptedReceipt(r)
	case ReceiptRejected:
		return validateRejectedReceipt(r)
	case ReceiptOutcomeUnknown:
		return validateUnknownReceipt(r)
	default:
		return ErrTargetReceipt
	}
}

// validateAcceptedReceipt 校验成功 receipt 必须有 reserved resource 和证明摘要。
func validateAcceptedReceipt(r TargetReceipt) error {
	if r.ResourceDecision != "reserved" || r.ErrorCode != "" {
		return ErrTargetReceipt
	}
	if err := validateText(ErrTargetReceipt, "receipt_id", r.ReceiptID, 256); err != nil {
		return err
	}
	return validateDigest(ErrTargetReceipt, "receipt_digest", r.ReceiptDigest)
}

// validateRejectedReceipt 校验拒绝 receipt 只能携带错误而不能伪造 receipt fence。
func validateRejectedReceipt(r TargetReceipt) error {
	if r.ResourceDecision != "denied" && r.ResourceDecision != "exhausted" && r.ResourceDecision != "quarantined" {
		return ErrTargetReceipt
	}
	if r.ReceiptID != "" || r.ReceiptDigest != "" {
		return ErrTargetReceipt
	}
	return validateText(ErrTargetReceipt, "error_code", r.ErrorCode, 128)
}

// validateUnknownReceipt 校验 outcome_unknown 只保留原 attempt 的 receipt 线索。
func validateUnknownReceipt(r TargetReceipt) error {
	if r.ResourceDecision == "reserved" {
		return ErrTargetReceipt
	}
	if err := validateText(ErrTargetReceipt, "error_code", r.ErrorCode, 128); err != nil {
		return err
	}
	if err := validateText(ErrTargetReceipt, "receipt_id", r.ReceiptID, 256); err != nil {
		return err
	}
	return validateDigest(ErrTargetReceipt, "receipt_digest", r.ReceiptDigest)
}

// Validate 校验 planned target 与逐项 receipt 完整匹配及 aggregate 状态。
func (f FanoutResult) Validate() error {
	if err := validateFanoutHeader(f); err != nil {
		return err
	}
	planned, err := validatePlannedTargets(f.PlannedTargetIDs)
	if err != nil {
		return err
	}
	if err := validateFanoutReceipts(f); err != nil {
		return err
	}
	rejected, unknown, seen, err := validateReceiptSetWithUnknown(f.Receipts, planned)
	if err != nil {
		return err
	}
	if len(seen) != len(planned) {
		return fmt.Errorf("%w: planned target lacks receipt", ErrTargetReceipt)
	}
	return validateFanoutStatusWithUnknown(f.Status, rejected, unknown, len(f.Receipts))
}

func validateFanoutReceipts(f FanoutResult) error {
	for index, receipt := range f.Receipts {
		if err := validateFanoutReceiptOrder(f, index, receipt); err != nil {
			return err
		}
	}
	return nil
}

// validateFanoutReceiptOrder 校验 receipt 顺序及其 operation 摘要绑定。
func validateFanoutReceiptOrder(f FanoutResult, index int, receipt TargetReceipt) error {
	if receipt.TargetID != f.PlannedTargetIDs[index] {
		return fmt.Errorf("%w: receipts must follow planned target order", ErrTargetReceipt)
	}
	if receipt.OperationID != "" && receipt.OperationID != f.OperationID {
		return fmt.Errorf("%w: target receipt operation id does not match fanout operation", ErrOperationConflict)
	}
	if f.OperationDigest != "" && receipt.OperationDigest != "" && receipt.OperationDigest != f.OperationDigest {
		return fmt.Errorf("%w: target receipt operation digest does not match fanout operation", ErrOperationConflict)
	}
	return nil
}

// validateFanoutHeader 校验 fan-out envelope 的 schema、scope 文本和数量一致性。
func validateFanoutHeader(f FanoutResult) error {
	if f.SchemaVersion != SchemaVersion || f.OperationID == "" || len(f.Receipts) == 0 || len(f.PlannedTargetIDs) == 0 || len(f.PlannedTargetIDs) != len(f.Receipts) {
		return ErrInvalidDTO
	}
	if err := validateText(ErrInvalidDTO, "room_id", f.RoomID, 256); err != nil {
		return err
	}
	if err := validateText(ErrInvalidDTO, "operation_id", f.OperationID, 256); err != nil {
		return err
	}
	if f.OperationDigest != "" {
		return validateDigest(ErrInvalidDTO, "operation_digest", f.OperationDigest)
	}
	return nil
}

// validatePlannedTargets 动态校验 planned target 集合无重复且文本有界。
func validatePlannedTargets(targets []string) (map[string]struct{}, error) {
	if len(targets) > MaxFanoutTargets {
		return nil, fmt.Errorf("%w: target count exceeds maximum", ErrTargetReceipt)
	}
	planned := make(map[string]struct{}, len(targets))
	var previous string
	for index, targetID := range targets {
		if err := validateText(ErrInvalidDTO, "planned_target_id", targetID, 256); err != nil {
			return nil, err
		}
		if index > 0 && bytes.Compare([]byte(previous), []byte(targetID)) >= 0 {
			return nil, fmt.Errorf("%w: planned targets must use canonical byte order", ErrTargetReceipt)
		}
		if _, exists := planned[targetID]; exists {
			return nil, fmt.Errorf("%w: duplicate planned target", ErrTargetReceipt)
		}
		planned[targetID] = struct{}{}
		previous = targetID
	}
	return planned, nil
}

// validateReceiptSet 校验每个 receipt 都对应一个 planned target。
// validateReceiptSet preserves the original helper contract for package-local
// callers while the fan-out validator also tracks outcome_unknown separately.
func validateReceiptSet(receipts []TargetReceipt, planned map[string]struct{}) (int, map[string]struct{}, error) {
	rejected, _, seen, err := validateReceiptSetWithUnknown(receipts, planned)
	return rejected, seen, err
}

// validateReceiptSetWithUnknown 校验逐目标 receipt 集合并统计 rejected/unknown。
func validateReceiptSetWithUnknown(receipts []TargetReceipt, planned map[string]struct{}) (int, int, map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(receipts))
	rejected := 0
	unknown := 0
	for index, receipt := range receipts {
		if err := receipt.Validate(); err != nil {
			return 0, 0, nil, err
		}
		if index >= len(planned) {
			return 0, 0, nil, fmt.Errorf("%w: receipt exceeds planned target count", ErrTargetReceipt)
		}
		if _, exists := seen[receipt.TargetID]; exists {
			return 0, 0, nil, fmt.Errorf("%w: duplicate target", ErrTargetReceipt)
		}
		if _, exists := planned[receipt.TargetID]; !exists {
			return 0, 0, nil, fmt.Errorf("%w: receipt target is not planned", ErrTargetReceipt)
		}
		seen[receipt.TargetID] = struct{}{}
		if receipt.Status == ReceiptRejected {
			rejected++
		} else if receipt.Status == ReceiptOutcomeUnknown {
			unknown++
		}
	}
	return rejected, unknown, seen, nil
}

// validateFanoutStatus 校验 aggregate status 不覆盖任何 target item outcome。
// validateFanoutStatus preserves the original package-local helper contract.
func validateFanoutStatus(status string, rejected, total int) error {
	return validateFanoutStatusWithUnknown(status, rejected, 0, total)
}

func validateFanoutStatusWithUnknown(status string, rejected, unknown, total int) error {
	switch status {
	case FanoutApplied:
		return validateAppliedFanout(rejected, unknown)
	case FanoutPartial:
		return validatePartialFanout(rejected, unknown, total)
	case FanoutRejected:
		return validateRejectedFanout(rejected, unknown, total)
	default:
		return ErrInvalidDTO
	}
}

func validateAppliedFanout(rejected, unknown int) error {
	if rejected != 0 || unknown != 0 {
		return ErrInvalidDTO
	}
	return nil
}

func validatePartialFanout(rejected, unknown, total int) error {
	incomplete := rejected + unknown
	if incomplete == 0 || (incomplete == total && unknown == 0) {
		return ErrInvalidDTO
	}
	return nil
}

func validateRejectedFanout(rejected, unknown, total int) error {
	if rejected != total || unknown != 0 {
		return ErrInvalidDTO
	}
	return nil
}

func validRoomStatus(status string) bool {
	return status == StatusOpen || status == StatusClosingPending || status == StatusClosed
}
func validCustody(status string) bool {
	return status == CustodyPending || status == CustodyAvailable || status == CustodyAborted
}
func validOperationKind(kind string) bool {
	switch kind {
	case OperationCreate, OperationJoin, OperationLeave, OperationGet, OperationClose, OperationExpire, OperationCleanup, OperationBroadcast, OperationRound:
		return true
	}
	return false
}

// validateDigest 校验摘要是小写十六进制 SHA-256 文本。
func validateDigest(base error, name, digest string) error {
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return fmt.Errorf("%w: %s must be lowercase sha256", base, name)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("%w: %s is not hex", base, name)
	}
	return nil
}

// ComputeOperationDigest 计算 operation 摘要并排除 request_digest 自引用。
func ComputeOperationDigest(o Operation) (string, error) {
	copy := o
	copy.RequestDigest = ""
	encoded, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("orch.room.operation/v1\x00"), encoded...))
	return hex.EncodeToString(sum[:]), nil
}

// FinalizeOperationDigest 计算并写入不可变 operation request digest。
func FinalizeOperationDigest(o Operation) (Operation, error) {
	digest, err := ComputeOperationDigest(o)
	if err != nil {
		return Operation{}, err
	}
	o.RequestDigest = digest
	return o, nil
}
