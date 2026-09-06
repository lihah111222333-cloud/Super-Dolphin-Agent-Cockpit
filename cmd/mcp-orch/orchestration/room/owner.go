package room

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// Member is the immutable authorization snapshot consumed by the candidate
// owner. It is not a grant issuer and never contains a bearer or credential.
type Member struct {
	PrincipalRef string `json:"principal_ref"`
	Subject      string `json:"subject"`
	Role         string `json:"role"`
	Active       bool   `json:"active"`
}

// PrincipalVerifier 是上游认证 owner 的唯一成员授权端口。
// Room 不把调用方传入的 Member 快照或 GrantDigest 视为授权事实。
type PrincipalVerifier interface {
	Verify(context.Context, Room, Principal) error
}

const (
	MemberOwner       = "owner"
	MemberParticipant = "participant"
)

// FanoutPlan fixes the target denominator before any receipt is observed.
type FanoutPlan struct {
	SchemaVersion      int64    `json:"schema_version"`
	RoomID             string   `json:"room_id"`
	OperationID        string   `json:"operation_id"`
	OperationDigest    string   `json:"operation_digest"`
	MembershipRevision int64    `json:"membership_revision"`
	TargetIDs          []string `json:"target_ids"`
}

// Owner is a candidate-only, transaction-neutral Room owner. Its state is
// process-local and non-authoritative; no persistence or external effect is
// hidden behind these methods.
type Owner struct {
	mu         sync.Mutex
	room       Room
	members    map[string]Member
	arbiter    *Arbiter
	operations map[string]Operation
	candidates map[string]AdmissionCandidate
	byKey      map[string]string
	plans      map[string]FanoutPlan
	results    map[string]FanoutResult
	closePlans map[string][]string
	inventory  []string
	verifier   PrincipalVerifier
}

// NewOwner 创建隔离的 Room 候选 owner；可选成员快照必须显式提供。
// The variadic form permits an
// empty membership snapshot while keeping membership explicit when supplied.
func NewOwner(room Room, snapshots ...[]Member) (*Owner, error) {
	return newOwner(room, nil, snapshots...)
}

// NewOwnerWithVerifier 创建带显式上游成员 verifier 的候选 owner。
func NewOwnerWithVerifier(room Room, verifier PrincipalVerifier, snapshots ...[]Member) (*Owner, error) {
	if err := requirePrincipalVerifier(verifier); err != nil {
		return nil, err
	}
	return newOwner(room, verifier, snapshots...)
}

// requirePrincipalVerifier 拒绝 nil 和 typed-nil 的认证 owner，保持授权 fail-closed。
func requirePrincipalVerifier(verifier PrincipalVerifier) error {
	if verifier == nil {
		return ErrAuthorization
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return ErrAuthorization
		}
	}
	return nil
}

// newOwner 构造带可选 verifier 的内部 owner，并严格校验快照形状。
func newOwner(room Room, verifier PrincipalVerifier, snapshots ...[]Member) (*Owner, error) {
	if err := room.Validate(); err != nil {
		return nil, err
	}
	members := make(map[string]Member)
	if len(snapshots) > 1 {
		return nil, ErrMemberConflict
	}
	if len(snapshots) == 1 {
		for _, member := range snapshots[0] {
			if err := validateMember(member); err != nil {
				return nil, err
			}
			if _, exists := members[member.PrincipalRef]; exists {
				return nil, ErrMemberConflict
			}
			members[member.PrincipalRef] = member
		}
	}
	head := AdmissionHead{SchemaVersion: SchemaVersion, RoomID: room.RoomID, Generation: 0,
		State: StatusOpen, CustodyResolution: CustodyAvailable, DispatchGeneration: room.DispatchGeneration,
		PreviousInventoryRoot: emptyInventoryRoot(), CommandInventoryRoot: emptyInventoryRoot()}
	arbiter, err := NewArbiter(head)
	if err != nil {
		return nil, err
	}
	owner := &Owner{room: room, members: members, arbiter: arbiter, operations: make(map[string]Operation),
		byKey: make(map[string]string), candidates: make(map[string]AdmissionCandidate), plans: make(map[string]FanoutPlan), results: make(map[string]FanoutResult), closePlans: make(map[string][]string), verifier: verifier}
	return owner, nil
}

// NewRoomOwner 创建带有描述性名称的同一候选 owner。
func NewRoomOwner(room Room, members []Member) (*Owner, error) { return NewOwner(room, members) }

// NewFixtureOwner 仅供本地合同测试，明确使用 non-authoritative verifier stub。
func NewFixtureOwner(room Room, members []Member) (*Owner, error) {
	return NewOwnerWithVerifier(room, fixtureVerifier{members: members}, members)
}

type fixtureVerifier struct{ members []Member }

// Verify 仅按测试快照模拟成员存在性，不是生产认证实现。
func (v fixtureVerifier) Verify(_ context.Context, _ Room, principal Principal) error {
	for _, member := range v.members {
		if member.PrincipalRef == principal.PrincipalRef && member.Active {
			return nil
		}
	}
	return ErrAuthorization
}

func validateMember(member Member) error {
	if err := validateText(ErrAuthorization, "principal_ref", member.PrincipalRef, 256); err != nil {
		return err
	}
	if err := validateText(ErrAuthorization, "subject", member.Subject, 256); err != nil {
		return err
	}
	if member.Role != MemberOwner && member.Role != MemberParticipant {
		return fmt.Errorf("%w: unknown member role", ErrAuthorization)
	}
	return nil
}

// Snapshot 返回隔离的 Room projection，不暴露 owner 内部状态。
func (o *Owner) Snapshot() (Room, error) {
	if o == nil {
		return Room{}, ErrOwnerClosed
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.room, nil
}

// Result 返回已 finalize 的逐目标结果；缺少记录时返回稳定冲突错误。
func (o *Owner) Result(operationID string) (FanoutResult, error) {
	if o == nil {
		return FanoutResult{}, ErrOperationConflict
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	result, ok := o.results[operationID]
	if !ok {
		return FanoutResult{}, ErrOperationConflict
	}
	return cloneResult(result), nil
}

// Members 返回按 principal_ref 排序的成员快照副本。
func (o *Owner) Members() ([]Member, error) {
	if o == nil {
		return nil, ErrOwnerClosed
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	keys := make([]string, 0, len(o.members))
	for key := range o.members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Member, 0, len(keys))
	for _, key := range keys {
		result = append(result, o.members[key])
	}
	return result, nil
}

// Authorize 同时执行 Room scope 合同和冻结成员快照校验。
// It enforces both the Room scope contract and the frozen membership
// snapshot. Membership never implies private peer or cross-room authority.
func (o *Owner) Authorize(ctx context.Context, operation Operation) error {
	if ctx == nil {
		return ErrAuthorization
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o == nil {
		return ErrAuthorization
	}
	o.mu.Lock()
	room := o.room
	o.mu.Unlock()
	if err := operation.Authorize(room); err != nil {
		return err
	}
	if o.verifier == nil {
		return ErrAuthorization
	}
	if err := o.verifier.Verify(ctx, room, operation.Principal); err != nil {
		return fmt.Errorf("%w: principal verifier rejected", ErrAuthorization)
	}
	if operation.Principal.ActorPrincipal == "system_scheduler" {
		return nil
	}
	return nil
}

// Admit 执行一次不可变 admission CAS 并记录幂等关系。
// It performs a single immutable admission CAS and remembers idempotency.
// The caller must provide the already-authorized operation digest.
func (o *Owner) Admit(ctx context.Context, operation Operation, candidate AdmissionCandidate) (AdmissionResult, error) {
	if err := o.Authorize(ctx, operation); err != nil {
		return AdmissionResult{}, err
	}
	if err := candidate.Validate(); err != nil {
		return AdmissionResult{}, err
	}
	if err := validateAdmissionForOperation(operation, candidate); err != nil {
		return AdmissionResult{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if previous, exists := o.operations[operation.OperationID]; exists && previous != operation {
		return AdmissionResult{}, ErrOperationConflict
	}
	if previousID, exists := o.byKey[operation.IdempotencyKeyDigest]; exists {
		previous := o.operations[previousID]
		if previous != operation {
			return AdmissionResult{}, ErrOperationConflict
		}
		return o.arbiter.TryAdmit(o.candidates[previousID])
	}
	result, err := o.arbiter.TryAdmit(candidate)
	if err != nil {
		return AdmissionResult{}, err
	}
	o.operations[operation.OperationID] = operation
	o.candidates[operation.OperationID] = candidate
	o.byKey[operation.IdempotencyKeyDigest] = operation.OperationID
	return result, nil
}

// validateAdmissionForOperation 校验 candidate 与 operation 的状态转换绑定。
func validateAdmissionForOperation(operation Operation, candidate AdmissionCandidate) error {
	if candidate.RoomID != operation.RoomID || candidate.OperationID != operation.OperationID || candidate.OperationDigest != operation.RequestDigest {
		return ErrOperationConflict
	}
	if operation.Kind == OperationClose && candidate.NextState != StatusClosingPending {
		return ErrInvalidState
	}
	if operation.Kind != OperationClose && candidate.NextState != StatusOpen {
		return ErrInvalidState
	}
	return nil
}

// PlanFanout 为 broadcast/round 冻结规范化 target denominator。
func (o *Owner) PlanFanout(ctx context.Context, operation Operation, targetIDs []string) (FanoutPlan, error) {
	if operation.Kind != OperationBroadcast && operation.Kind != OperationRound {
		return FanoutPlan{}, ErrInvalidDTO
	}
	if err := o.Authorize(ctx, operation); err != nil {
		return FanoutPlan{}, err
	}
	if err := o.requireMembershipRevision(operation); err != nil {
		return FanoutPlan{}, err
	}
	copyIDs, err := copyTargetIDs(targetIDs)
	if err != nil {
		return FanoutPlan{}, err
	}
	plan := FanoutPlan{SchemaVersion: SchemaVersion, RoomID: operation.RoomID, OperationID: operation.OperationID,
		OperationDigest: operation.RequestDigest, MembershipRevision: operation.ExpectedMembershipRevision, TargetIDs: copyIDs}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.authorizeTargets(copyIDs); err != nil {
		return FanoutPlan{}, err
	}
	if previous, exists := o.plans[operation.OperationID]; exists {
		if !samePlan(previous, plan) {
			return FanoutPlan{}, ErrOperationConflict
		}
		return clonePlan(previous), nil
	}
	o.plans[operation.OperationID] = clonePlan(plan)
	return plan, nil
}

// FinalizeFanout 校验每个 target receipt 并收敛原始 admission。
// It validates every target receipt and resolves the original
// admission. outcome_unknown deliberately leaves custody pending.
func (o *Owner) FinalizeFanout(ctx context.Context, operation Operation, result FanoutResult) error {
	if err := o.Authorize(ctx, operation); err != nil {
		return err
	}
	if operation.Kind != OperationBroadcast && operation.Kind != OperationRound {
		return ErrInvalidDTO
	}
	if err := validateFanoutForOperation(operation, result); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	plan, exists := o.plans[operation.OperationID]
	if !exists || !sameIDs(plan.TargetIDs, result.PlannedTargetIDs) {
		return ErrTargetReceipt
	}
	if hasUnknown(result) {
		return ErrOutcomeUnknown
	}
	return o.recordResult(operation, result)
}

// BeginClose 安装 closing CAS winner，与 fan-out 在同一 head 上竞争。
// It competes with fan-out on the
// same head and cannot claim a provisional operation.
func (o *Owner) BeginClose(ctx context.Context, operation Operation, targetIDs ...[]string) (AdmissionResult, error) {
	if operation.Kind != OperationClose {
		return AdmissionResult{}, ErrInvalidDTO
	}
	if err := o.Authorize(ctx, operation); err != nil {
		return AdmissionResult{}, err
	}
	o.mu.Lock()
	head := o.arbiter.Head()
	if len(targetIDs) > 1 {
		o.mu.Unlock()
		return AdmissionResult{}, ErrTargetReceipt
	}
	if len(targetIDs) == 1 {
		if err := validateTargetIDs(targetIDs[0]); err != nil {
			o.mu.Unlock()
			return AdmissionResult{}, err
		}
		if int64(len(targetIDs[0])) != head.CommandInventoryCount {
			o.mu.Unlock()
			return AdmissionResult{}, ErrTargetReceipt
		}
		if len(o.inventory) > 0 && !sameIDs(o.inventory, targetIDs[0]) {
			o.mu.Unlock()
			return AdmissionResult{}, ErrTargetReceipt
		}
		o.closePlans[operation.OperationID] = append([]string(nil), targetIDs[0]...)
	}
	o.mu.Unlock()
	candidate := AdmissionCandidate{SchemaVersion: SchemaVersion, RoomID: operation.RoomID, OperationID: operation.OperationID,
		OperationVersion: operation.OperationVersion, OperationDigest: operation.RequestDigest, ExpectedGeneration: head.Generation,
		NextState: StatusClosingPending, NextCustodyResolution: CustodyPending, DispatchGeneration: head.DispatchGeneration,
		PreviousInventoryRoot: head.CommandInventoryRoot, PreviousInventoryCount: head.CommandInventoryCount,
		CommandInventoryRoot: head.CommandInventoryRoot, CommandInventoryCount: head.CommandInventoryCount}
	result, err := o.Admit(ctx, operation, candidate)
	if err != nil {
		o.mu.Lock()
		delete(o.closePlans, operation.OperationID)
		o.mu.Unlock()
	}
	return result, err
}

// FinalizeClose 仅在完整且非 unknown drain receipts 后关闭 Room。
func (o *Owner) FinalizeClose(ctx context.Context, operation Operation, result FanoutResult, reason string) error {
	if operation.Kind != OperationClose {
		return ErrInvalidDTO
	}
	if err := validateCloseForOperation(operation, result, reason); err != nil {
		return err
	}
	if o.isClosed() {
		if err := o.authorizeClosedReplay(ctx, operation); err != nil {
			return err
		}
		if replay, err := o.closeReplay(operation, result, reason); replay {
			return err
		}
	}
	if err := o.Authorize(ctx, operation); err != nil {
		return err
	}
	return o.applyClose(operation, result, reason)
}

// Abort 恢复 previous inventory 并重新打开 closing operation。
// It restores the previous inventory and reopens a closing operation; it
// never creates a retry or changes the operation identity.
func (o *Owner) Abort(ctx context.Context, operation Operation, receiptDigest string) error {
	if operation.Kind != OperationClose {
		return ErrResolutionConflict
	}
	if o == nil {
		return ErrResolutionConflict
	}
	if o.abortTerminal() {
		return ErrResolutionConflict
	}
	if err := o.Authorize(ctx, operation); err != nil {
		return err
	}
	if err := validateDigest(ErrResolutionConflict, "receipt_digest", receiptDigest); err != nil {
		return err
	}
	return o.applyAbort(operation, receiptDigest)
}

func (o *Owner) applyAbort(operation Operation, receiptDigest string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.room.Status == StatusClosed || o.arbiter.Head().State != StatusClosingPending {
		return ErrResolutionConflict
	}
	if err := o.arbiter.Resolve(operation.OperationID, operation.RequestDigest, CustodyAborted, receiptDigest); err != nil {
		return err
	}
	if o.room.Status == StatusClosingPending {
		o.room.Status, o.room.CustodyResolution, o.room.CloseReason = StatusOpen, CustodyAborted, ""
	}
	return nil
}

// closeReplay 识别已关闭 Room 的精确 finalize replay，拒绝漂移输入。
func (o *Owner) closeReplay(operation Operation, result FanoutResult, reason string) (bool, error) {
	if o == nil {
		return true, ErrResolutionConflict
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.room.Status != StatusClosed {
		return false, nil
	}
	previous, exists := o.results[operation.OperationID]
	if exists && o.room.CloseReason == reason && digestValue(previous) == digestValue(result) {
		return true, nil
	}
	return true, ErrResolutionConflict
}

func (o *Owner) isClosed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.room.Status == StatusClosed
}

// authorizeClosedReplay 在 closed replay 前证明原 operation 和成员身份。
func (o *Owner) authorizeClosedReplay(ctx context.Context, operation Operation) error {
	if err := validateClosedReplayContext(ctx); err != nil {
		return err
	}
	recorded, room, verifier := o.closedReplayIdentity(operation)
	if !closedReplayMatches(recorded, operation, verifier) {
		return ErrAuthorization
	}
	if err := verifyClosedPrincipal(ctx, verifier, room, operation.Principal); err != nil {
		return err
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	if err := operation.Principal.Validate(); err != nil {
		return err
	}
	if operation.RoomID != room.RoomID || operation.Principal.ScopeRef != room.RunScope {
		return ErrAuthorization
	}
	return nil
}

func closedReplayMatches(recorded, operation Operation, verifier PrincipalVerifier) bool {
	return verifier != nil && recorded == operation
}

// verifyClosedPrincipal 在 closed replay 前重新调用显式认证 owner。
func verifyClosedPrincipal(ctx context.Context, verifier PrincipalVerifier, room Room, principal Principal) error {
	if err := verifier.Verify(ctx, room, principal); err != nil {
		return fmt.Errorf("%w: principal verifier rejected", ErrAuthorization)
	}
	return nil
}

func validateClosedReplayContext(ctx context.Context) error {
	if ctx == nil {
		return ErrAuthorization
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (o *Owner) closedReplayIdentity(operation Operation) (Operation, Room, PrincipalVerifier) {
	o.mu.Lock()
	defer o.mu.Unlock()
	recorded, exists := o.operations[operation.OperationID]
	if !exists {
		return Operation{}, o.room, o.verifier
	}
	return recorded, o.room, o.verifier
}

// applyClose 在锁内完成 close receipt、head 和 projection 的原子候选更新。
func (o *Owner) applyClose(operation Operation, result FanoutResult, reason string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.arbiter.Head().State != StatusClosingPending {
		return ErrResolutionConflict
	}
	if expected, exists := o.closePlans[operation.OperationID]; exists && !sameIDs(expected, result.PlannedTargetIDs) {
		return ErrTargetReceipt
	}
	if _, exists := o.closePlans[operation.OperationID]; !exists && int64(len(result.PlannedTargetIDs)) != o.arbiter.Head().CommandInventoryCount {
		return ErrTargetReceipt
	}
	if err := o.arbiter.Resolve(operation.OperationID, operation.RequestDigest, CustodyAvailable, digestValue(result)); err != nil {
		return err
	}
	o.room.Status, o.room.CustodyResolution, o.room.CloseReason = StatusClosed, CustodyAvailable, reason
	o.results[operation.OperationID] = cloneResult(result)
	return nil
}

func (o *Owner) abortTerminal() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.room.Status == StatusClosed || o.arbiter.Head().State != StatusClosingPending
}

func copyTargetIDs(targetIDs []string) ([]string, error) {
	if len(targetIDs) == 0 || len(targetIDs) > MaxFanoutTargets {
		return nil, ErrTargetReceipt
	}
	copyIDs := append([]string(nil), targetIDs...)
	if err := validateTargetIDs(copyIDs); err != nil {
		return nil, err
	}
	return copyIDs, nil
}

func (o *Owner) authorizeTargets(targetIDs []string) error {
	for _, target := range targetIDs {
		if !o.activeSubject(target) {
			return ErrAuthorization
		}
	}
	return nil
}

func (o *Owner) requireMembershipRevision(operation Operation) error {
	o.mu.Lock()
	revision := o.room.MembershipRevision
	o.mu.Unlock()
	if operation.ExpectedMembershipRevision <= 0 || operation.ExpectedMembershipRevision != revision {
		return ErrAuthorization
	}
	return nil
}

// validateFanoutForOperation 校验 fanout 结果与 operation 的 room/identity 绑定。
func validateFanoutForOperation(operation Operation, result FanoutResult) error {
	if err := result.Validate(); err != nil {
		return err
	}
	if result.RoomID != operation.RoomID || result.OperationID != operation.OperationID ||
		(result.OperationDigest != "" && result.OperationDigest != operation.RequestDigest) {
		return ErrOperationConflict
	}
	return nil
}

func hasUnknown(result FanoutResult) bool {
	for _, receipt := range result.Receipts {
		if receipt.Status == ReceiptOutcomeUnknown {
			return true
		}
	}
	return false
}

func (o *Owner) recordResult(operation Operation, result FanoutResult) error {
	digest := digestValue(result)
	if previous, exists := o.results[operation.OperationID]; exists {
		if digestValue(previous) != digest {
			return ErrOperationConflict
		}
		return nil
	}
	if err := o.arbiter.Resolve(operation.OperationID, operation.RequestDigest, CustodyAvailable, digest); err != nil {
		return err
	}
	o.results[operation.OperationID] = cloneResult(result)
	o.inventory = append([]string(nil), result.PlannedTargetIDs...)
	return nil
}

func validateCloseForOperation(operation Operation, result FanoutResult, reason string) error {
	if err := result.Validate(); err != nil {
		return err
	}
	if result.RoomID != operation.RoomID || result.OperationID != operation.OperationID {
		return ErrOperationConflict
	}
	if hasUnknown(result) {
		return ErrOutcomeUnknown
	}
	return validateText(ErrInvalidState, "close_reason", reason, 256)
}

// samePlan 比较完整的冻结 fanout plan 身份。
func samePlan(left, right FanoutPlan) bool {
	return left.SchemaVersion == right.SchemaVersion && left.RoomID == right.RoomID && left.OperationID == right.OperationID &&
		left.OperationDigest == right.OperationDigest && left.MembershipRevision == right.MembershipRevision && sameIDs(left.TargetIDs, right.TargetIDs)
}

// validateTargetIDs 校验 target 集合有界、唯一且按字节序排列。
func validateTargetIDs(targetIDs []string) error {
	if len(targetIDs) > MaxFanoutTargets {
		return ErrTargetReceipt
	}
	for index, target := range targetIDs {
		if err := validateText(ErrTargetReceipt, "target_id", target, 256); err != nil {
			return err
		}
		if index > 0 && targetIDs[index-1] >= target {
			return ErrTargetReceipt
		}
	}
	return nil
}

func (o *Owner) activeSubject(subject string) bool {
	for _, member := range o.members {
		if member.Active && member.Subject == subject {
			return true
		}
	}
	return false
}

func sameIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func clonePlan(plan FanoutPlan) FanoutPlan {
	plan.TargetIDs = append([]string(nil), plan.TargetIDs...)
	return plan
}

func cloneResult(result FanoutResult) FanoutResult {
	result.PlannedTargetIDs = append([]string(nil), result.PlannedTargetIDs...)
	result.Receipts = append([]TargetReceipt(nil), result.Receipts...)
	return result
}

func emptyInventoryRoot() string { return digestValue("orch.room.inventory.empty.v1") }

func digestValue(value any) string {
	encoded := fmt.Sprintf("%#v", value)
	sum := sha256.Sum256(append([]byte("orch.room.owner.v1\x00"), encoded...))
	return hex.EncodeToString(sum[:])
}
