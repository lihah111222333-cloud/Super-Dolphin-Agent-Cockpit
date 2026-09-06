package waitprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

// Provision 先做精确 replay，再逐目标创建 grant 和 wait。
// WaitHandleID 留空并由 accesswait owner 生成；失败时不自动清理。
func (a *Adapter) Provision(ctx context.Context, request Request) (Result, error) {
	now, err := validateProvisionInputs(a, ctx, request)
	if err != nil {
		return Result{}, err
	}
	prior, found, err := a.readProvisionReplay(ctx, request)
	if err != nil {
		return Result{}, err
	}
	if found {
		return prior, nil
	}

	result := Result{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, Items: make([]Item, 0, len(request.Targets))}
	issued := now.UTC()
	for _, target := range request.Targets {
		item, err, retain := a.provisionTarget(ctx, request, target, issued)
		if err != nil {
			if retain {
				result.Items = append(result.Items, item)
			}
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// validateProvisionInputs 在任何 owner 副作用前拒绝 nil 端口、nil context 和非法请求。
func validateProvisionInputs(a *Adapter, ctx context.Context, request Request) (time.Time, error) {
	if a == nil || isNil(a.access) || isNil(a.cleanup) || isNil(a.reader) || isNil(a.clock) {
		return time.Time{}, ErrNilPort
	}
	if ctx == nil {
		return time.Time{}, errors.New("wait provision context is nil")
	}
	now := a.clock()
	if err := request.ValidateAt(now); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// readProvisionReplay 读取 canonical replay，并区分缺失与真实读取错误。
func (a *Adapter) readProvisionReplay(ctx context.Context, request Request) (Result, bool, error) {
	prior, err := a.reader.ReadWaitProvision(ctx, request.OperationKey)
	if err == nil {
		if prior.OperationDigest != request.OperationDigest {
			return Result{}, false, ErrDigestConflict
		}
		if err := prior.ValidateFor(request); err != nil {
			return Result{}, false, errors.Join(ErrReplayConflict, err)
		}
		return prior, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Result{}, false, err
	}
	return Result{}, false, nil
}

// provisionTarget 为一个 target 保持稳定 grant ID、幂等绑定和 owner-minted wait。
func (a *Adapter) provisionTarget(ctx context.Context, request Request, target Target, issued time.Time) (Item, error, bool) {
	grantRequest := buildGrantRequest(request, target, issued)
	grant, err := a.access.CreateGrant(ctx, grantRequest)
	if err != nil {
		item := Item{Key: target.Key, Grant: grant}
		return item, errors.Join(ErrPartial, fmt.Errorf("target %q grant: %w", target.Key, err)), grant.GrantID != ""
	}
	if err := validateProvisionedGrant(request, target, grant, issued, grantRequest); err != nil {
		return Item{Key: target.Key, Grant: grant}, errors.Join(ErrPartial, fmt.Errorf("target %q grant response: %w", target.Key, err)), true
	}

	waitRequest := buildWaitRequest(request, target, grant)
	waitHandle, err := a.access.CreateWait(ctx, waitRequest)
	item := Item{Key: target.Key, Grant: grant, Wait: waitHandle}
	if err != nil {
		return item, errors.Join(ErrPartial, fmt.Errorf("target %q wait: %w", target.Key, err)), true
	}
	if err := validateProvisionedWait(request, target, grant, waitHandle, waitRequest); err != nil {
		return item, errors.Join(ErrPartial, fmt.Errorf("target %q wait response: %w", target.Key, err)), true
	}
	return item, nil, true
}

// buildGrantRequest 生成带稳定 grant ID 和 request digest 的 owner 请求。
func buildGrantRequest(request Request, target Target, issued time.Time) accesswait.AccessGrantRequest {
	return accesswait.AccessGrantRequest{
		GrantID: grantIDFor(request, target), CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		Source: request.SourceFence, Target: target.TargetFence, CanRead: true,
		IdempotencyNamespace: expectedGrantNamespace(request, target),
		IdempotencyKeyDigest: digestFor("grant", request.OperationKey, request.OperationDigest, target.Key),
		RequestDigest:        request.OperationDigest, IssuedAt: issued, ExpiresAt: request.DeadlineAt,
	}
}

// buildWaitRequest 生成留空 WaitHandleID、绑定 canonical cursor 的 owner 请求。
func buildWaitRequest(request Request, target Target, grant accesswait.AccessGrant) accesswait.WaitRequest {
	return accesswait.WaitRequest{
		GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: request.CallerPrincipalRef,
		ScopeRef: request.ScopeRef, Selector: target.Selector, AfterSequence: target.AfterSequence,
		AfterCursor: target.AfterCursor, DeadlineAt: request.DeadlineAt,
		IdempotencyNamespace: expectedWaitNamespace(request, target),
		IdempotencyKeyDigest: digestFor("wait", request.OperationKey, request.OperationDigest, target.Key),
		RequestDigest:        request.OperationDigest,
	}
}

// Provision 是 package-level convenience 入口，复用同一组 replay 与 owner 语义。
func Provision(ctx context.Context, port Port, request Request, reader ReplayReader) (Result, error) {
	a, err := New(Config{Access: port, Cleanup: port, Reader: reader})
	if err != nil {
		return Result{}, err
	}
	return a.Provision(ctx, request)
}

// Cleanup 显式取消 wait 并撤销 grant；Provision 永不隐式调用它，并汇总全部错误。
func (a *Adapter) Cleanup(ctx context.Context, request Request, result Result, reason string) error {
	if err := validateCleanupInputs(a, ctx); err != nil {
		return err
	}
	if err := validateCleanupResult(request, result); err != nil {
		return err
	}
	if err := boundedText("cleanup_reason", reason, 512); err != nil {
		return err
	}
	if err := requireCompleteGrantsWithoutReader(a.grantReader, result); err != nil {
		return err
	}
	var errs []error
	for i, item := range result.Items {
		errs = append(errs, a.cleanupItem(ctx, request, item, request.Targets[i], reason)...)
	}
	return errors.Join(errs...)
}

// validateCleanupInputs 在清理前执行端口和 context 的 fail-fast 检查。
func validateCleanupInputs(a *Adapter, ctx context.Context) error {
	if a == nil || isNil(a.cleanup) || isNil(a.clock) {
		return ErrNilPort
	}
	if ctx == nil {
		return errors.New("wait provision context is nil")
	}
	return nil
}

// requireCompleteGrantsWithoutReader 禁止没有 durable reader 时清理不完整 grant。
func requireCompleteGrantsWithoutReader(reader GrantReader, result Result) error {
	if reader != nil {
		return nil
	}
	for _, item := range result.Items {
		if !grantLooksComplete(item.Grant) {
			return ErrReplayConflict
		}
	}
	return nil
}

// cleanupItem 确认 durable grant 后按 wait、grant 顺序执行两个显式清理副作用。
func (a *Adapter) cleanupItem(ctx context.Context, request Request, item Item, target Target, reason string) []error {
	grant, err := a.authoritativeCleanupGrant(ctx, request, target, item)
	if err != nil {
		return []error{err}
	}
	var errs []error
	if item.Wait.WaitHandleID != "" {
		if err := a.cancelCleanupWait(ctx, request, item, grant, reason); err != nil {
			errs = append(errs, err)
		}
	}
	if item.Grant.GrantID != "" {
		if err := a.revokeCleanupGrant(ctx, request, item, grant, reason); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// authoritativeCleanupGrant 从 owner 读取并验证 grant，避免 opaque id 被跨操作滥用。
func (a *Adapter) authoritativeCleanupGrant(ctx context.Context, request Request, target Target, item Item) (accesswait.AccessGrant, error) {
	grant := item.Grant
	if a.grantReader == nil {
		return grant, nil
	}
	authoritative, err := a.grantReader.GetGrant(ctx, item.Grant.GrantID)
	if err != nil {
		return accesswait.AccessGrant{}, fmt.Errorf("target %q confirm grant: %w", item.Key, errors.Join(ErrReplayConflict, err))
	}
	if err := validateCleanupGrant(request, target, item, authoritative); err != nil {
		return accesswait.AccessGrant{}, fmt.Errorf("target %q grant confirmation: %w", item.Key, errors.Join(ErrReplayConflict, err))
	}
	return authoritative, nil
}

// cancelCleanupWait 取消单个 wait，并保留当前 owner revision 与取消时间。
func (a *Adapter) cancelCleanupWait(ctx context.Context, request Request, item Item, grant accesswait.AccessGrant, reason string) error {
	_, err := a.cleanup.CancelWait(ctx, accesswait.CancelRequest{WaitHandleID: item.Wait.WaitHandleID, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef, GrantRevision: grant.Revision, ReasonRef: reason, CancelledAt: a.clock().UTC()})
	if err != nil {
		return fmt.Errorf("target %q cancel wait: %w", item.Key, err)
	}
	return nil
}

// revokeCleanupGrant 撤销单个 grant，并携带操作 digest 形成 durable 审计绑定。
func (a *Adapter) revokeCleanupGrant(ctx context.Context, request Request, item Item, grant accesswait.AccessGrant, reason string) error {
	_, err := a.cleanup.RevokeGrant(ctx, accesswait.RevokeGrantRequest{GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef, ReasonRef: reason, RequestDigest: request.OperationDigest, RevokedAt: a.clock().UTC()})
	if err != nil {
		return fmt.Errorf("target %q revoke grant: %w", item.Key, err)
	}
	return nil
}

// validateCleanupGrant confirms that a durable grant belongs to this exact
// operation. An opaque id, caller/scope pair and revision are not sufficient:
// the request digest, idempotency binding and both fence snapshots must match.
// 该校验在任何 revoke 副作用前执行，避免 opaque grant 跨操作复用。
func validateCleanupGrant(request Request, target Target, item Item, grant accesswait.AccessGrant) error {
	if item.Grant.GrantID != grant.GrantID || item.Grant.Revision != grant.Revision {
		return ErrReplayConflict
	}
	expected := accesswait.AccessGrantRequest{
		GrantID: grant.GrantID, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		Source: request.SourceFence, Target: target.TargetFence, CanRead: true,
		RequestDigest: request.OperationDigest, ExpiresAt: request.DeadlineAt, IssuedAt: grant.IssuedAt,
		IdempotencyNamespace: expectedGrantNamespace(request, target), IdempotencyKeyDigest: digestFor("grant", request.OperationKey, request.OperationDigest, target.Key),
	}
	if err := validateProvisionedGrant(request, target, grant, grant.IssuedAt, expected); err != nil {
		return errors.Join(ErrReplayConflict, err)
	}
	return nil
}

// validateCleanupResult 接受完整或部分 provisioning 输出，并限制其 target 前缀。
// A failed CreateWait has no wait id to cancel, but its already-created grant
// remains a mandatory revoke item; accepting only a target prefix keeps the
// operation bounded and prevents callers from cleaning an unrelated grant.
func validateCleanupResult(request Request, result Result) error {
	if result.OperationKey != request.OperationKey || result.OperationDigest != request.OperationDigest || len(result.Items) > len(request.Targets) {
		return ErrReplayConflict
	}
	for i, item := range result.Items {
		if err := validateCleanupItem(request, request.Targets[i], item); err != nil {
			return err
		}
	}
	return nil
}

// validateCleanupItem 校验部分结果中的 target、grant 回显和 wait 回显绑定。
func validateCleanupItem(request Request, target Target, item Item) error {
	if item.Key != target.Key || item.Grant.GrantID == "" || item.Grant.Revision <= 0 {
		return ErrReplayConflict
	}
	if err := validateCleanupGrantEcho(request, target, item.Grant); err != nil {
		return err
	}
	if err := validateCleanupWaitEcho(request, target, item); err != nil {
		return err
	}
	return nil
}

// validateCleanupGrantEcho 校验已回显 grant 字段，未知 owner 结果可省略证明字段。
func validateCleanupGrantEcho(request Request, target Target, grant accesswait.AccessGrant) error {
	if err := validateCleanupGrantIdentityEcho(request, grant); err != nil {
		return err
	}
	if err := validateCleanupGrantFenceEcho(request, target, grant); err != nil {
		return err
	}
	if !grantLooksComplete(grant) {
		return nil
	}
	expected := accesswait.AccessGrantRequest{
		GrantID: grant.GrantID, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		Source: request.SourceFence, Target: target.TargetFence, CanRead: true,
		RequestDigest: request.OperationDigest, IssuedAt: grant.IssuedAt, ExpiresAt: request.DeadlineAt,
		IdempotencyNamespace: expectedGrantNamespace(request, target),
		IdempotencyKeyDigest: digestFor("grant", request.OperationKey, request.OperationDigest, target.Key),
	}
	if err := validateProvisionedGrant(request, target, grant, grant.IssuedAt, expected); err != nil {
		return ErrReplayConflict
	}
	return nil
}

// validateCleanupGrantIdentityEcho 校验可选回显的 caller、scope 和能力字段。
func validateCleanupGrantIdentityEcho(request Request, grant accesswait.AccessGrant) error {
	if grant.CallerPrincipalRef != "" && grant.CallerPrincipalRef != request.CallerPrincipalRef {
		return ErrReplayConflict
	}
	if grant.ScopeRef != "" && grant.ScopeRef != request.ScopeRef {
		return ErrReplayConflict
	}
	if grant.CanSend {
		return ErrReplayConflict
	}
	return nil
}

// validateCleanupGrantFenceEcho 校验可选回显的 source/target fence 快照。
func validateCleanupGrantFenceEcho(request Request, target Target, grant accesswait.AccessGrant) error {
	if !isZeroFence(grant.Source) && grant.Source != request.SourceFence {
		return ErrReplayConflict
	}
	if !isZeroFence(grant.Target) && grant.Target != target.TargetFence {
		return ErrReplayConflict
	}
	return nil
}

// validateCleanupWaitEcho 校验存在的 wait handle 回显字段，保留未知 owner 取消能力。
func validateCleanupWaitEcho(request Request, target Target, item Item) error {
	wait := item.Wait
	if wait.WaitHandleID == "" {
		return nil
	}
	if err := validateCleanupWaitIdentityEcho(request, item); err != nil {
		return err
	}
	if err := validateCleanupWaitSelectorEcho(target, wait); err != nil {
		return err
	}
	if err := validateCleanupWaitTimingEcho(request, wait); err != nil {
		return err
	}
	return nil
}

// validateCleanupWaitIdentityEcho 校验 wait 的 grant、revision、caller 和 scope 回显。
func validateCleanupWaitIdentityEcho(request Request, item Item) error {
	wait := item.Wait
	if wait.GrantID != "" && wait.GrantID != item.Grant.GrantID {
		return ErrReplayConflict
	}
	if wait.GrantRevision != 0 && wait.GrantRevision != item.Grant.Revision {
		return ErrReplayConflict
	}
	if wait.CallerPrincipalRef != "" && wait.CallerPrincipalRef != request.CallerPrincipalRef {
		return ErrReplayConflict
	}
	if wait.ScopeRef != "" && wait.ScopeRef != request.ScopeRef {
		return ErrReplayConflict
	}
	return nil
}

// validateCleanupWaitSelectorEcho 校验 wait selector 的可选回显。
func validateCleanupWaitSelectorEcho(target Target, wait accesswait.WaitHandle) error {
	if !isZeroSelector(wait.Selector) && !reflect.DeepEqual(wait.Selector, target.Selector) {
		return ErrReplayConflict
	}
	return nil
}

// validateCleanupWaitTimingEcho 校验 wait deadline 和 request digest 的可选回显。
func validateCleanupWaitTimingEcho(request Request, wait accesswait.WaitHandle) error {
	if !wait.DeadlineAt.IsZero() && !wait.DeadlineAt.Equal(request.DeadlineAt) {
		return ErrReplayConflict
	}
	if wait.RequestDigest != "" && wait.RequestDigest != request.OperationDigest {
		return ErrReplayConflict
	}
	return nil
}

func expectedGrantNamespace(request Request, target Target) string {
	return "wait-provision/" + request.OperationKey + "/grant/" + target.Key
}

func expectedWaitNamespace(request Request, target Target) string {
	return "wait-provision/" + request.OperationKey + "/wait/" + target.Key
}

// validateProvisionedGrant 校验 owner 返回的 grant 与请求、稳定幂等键完全一致。
func validateProvisionedGrant(request Request, target Target, grant accesswait.AccessGrant, issued time.Time, expected accesswait.AccessGrantRequest) error {
	if err := grant.Validate(); err != nil {
		return errors.Join(ErrInvalidResult, err)
	}
	if err := validateGrantIdentity(request, grant, expected); err != nil {
		return err
	}
	if err := validateGrantFenceAndCapabilities(request, target, grant, expected); err != nil {
		return err
	}
	if err := validateGrantTimingAndDigest(request, grant, issued, expected); err != nil {
		return err
	}
	return nil
}

// validateGrantIdentity 校验 grant 状态及其稳定 opaque ID。
func validateGrantIdentity(request Request, grant accesswait.AccessGrant, expected accesswait.AccessGrantRequest) error {
	if grant.Status != "active" || grant.GrantID == "" || grant.GrantID != expected.GrantID {
		return ErrInvalidResult
	}
	if grant.CallerPrincipalRef != request.CallerPrincipalRef || grant.ScopeRef != request.ScopeRef {
		return ErrInvalidResult
	}
	return nil
}

// validateGrantFenceAndCapabilities 校验 source/target fence 与读写能力。
func validateGrantFenceAndCapabilities(request Request, target Target, grant accesswait.AccessGrant, expected accesswait.AccessGrantRequest) error {
	if grant.Source != request.SourceFence || grant.Target != target.TargetFence {
		return ErrInvalidResult
	}
	if grant.CanRead != expected.CanRead || grant.CanSend != expected.CanSend {
		return ErrInvalidResult
	}
	return nil
}

// validateGrantTimingAndDigest 校验幂等绑定、操作摘要和有效期。
func validateGrantTimingAndDigest(request Request, grant accesswait.AccessGrant, issued time.Time, expected accesswait.AccessGrantRequest) error {
	if grant.IdempotencyNamespace != expected.IdempotencyNamespace || grant.IdempotencyKeyDigest != expected.IdempotencyKeyDigest {
		return ErrInvalidResult
	}
	if grant.RequestDigest != request.OperationDigest || !grant.IssuedAt.Equal(issued) || !grant.ExpiresAt.Equal(request.DeadlineAt) {
		return ErrInvalidResult
	}
	return nil
}

// validateProvisionedWait 校验 server-minted wait 与 grant、selector、cursor 完全一致。
func validateProvisionedWait(request Request, target Target, grant accesswait.AccessGrant, handle accesswait.WaitHandle, expected accesswait.WaitRequest) error {
	if err := handle.Validate(); err != nil {
		return errors.Join(ErrInvalidResult, err)
	}
	if err := validateWaitIdentity(request, grant, handle); err != nil {
		return err
	}
	if err := validateWaitSelectorAndCursor(request, target, handle); err != nil {
		return err
	}
	if err := validateWaitTimingAndDigest(request, handle, expected); err != nil {
		return err
	}
	return nil
}

// validateWaitIdentity 校验 server-minted wait handle 与 grant 的身份绑定。
func validateWaitIdentity(request Request, grant accesswait.AccessGrant, handle accesswait.WaitHandle) error {
	if handle.WaitHandleID == "" || handle.GrantID != grant.GrantID || handle.GrantRevision != grant.Revision {
		return ErrInvalidResult
	}
	if handle.CallerPrincipalRef != request.CallerPrincipalRef || handle.ScopeRef != request.ScopeRef || handle.SourceAgentID != request.SourceFence.AgentID {
		return ErrInvalidResult
	}
	return nil
}

// validateWaitSelectorAndCursor 校验 wait 的 target fence、selector 与 canonical cursor。
func validateWaitSelectorAndCursor(request Request, target Target, handle accesswait.WaitHandle) error {
	if handle.Target != target.TargetFence || !reflect.DeepEqual(handle.Selector, target.Selector) {
		return ErrInvalidResult
	}
	if handle.AfterSequence != target.AfterSequence || handle.AfterCursor != target.AfterCursor {
		return ErrInvalidResult
	}
	return nil
}

// validateWaitTimingAndDigest 校验 wait deadline、幂等键和操作摘要。
func validateWaitTimingAndDigest(request Request, handle accesswait.WaitHandle, expected accesswait.WaitRequest) error {
	if !handle.DeadlineAt.Equal(request.DeadlineAt) {
		return ErrInvalidResult
	}
	if handle.IdempotencyNamespace != expected.IdempotencyNamespace || handle.IdempotencyKeyDigest != expected.IdempotencyKeyDigest || handle.RequestDigest != request.OperationDigest {
		return ErrInvalidResult
	}
	return nil
}

// grantLooksComplete 判断 grant 是否具备无需 durable reader 的完整证明字段。
func grantLooksComplete(grant accesswait.AccessGrant) bool {
	return grantIdentityPresent(grant) && grantFencesPresent(grant) && grantProofPresent(grant) && grantTimestampsPresent(grant)
}

// grantIdentityPresent 判断 grant 是否带有 caller、scope 和状态身份字段。
func grantIdentityPresent(grant accesswait.AccessGrant) bool {
	return grant.CallerPrincipalRef != "" && grant.ScopeRef != "" && grant.Status != ""
}

// grantFencesPresent 判断 grant 是否包含完整 source/target fence 快照。
func grantFencesPresent(grant accesswait.AccessGrant) bool {
	return !isZeroFence(grant.Source) && !isZeroFence(grant.Target)
}

// grantProofPresent 判断 grant 是否包含幂等键、摘要和 schema 证明。
func grantProofPresent(grant accesswait.AccessGrant) bool {
	return grant.IdempotencyNamespace != "" && grant.IdempotencyKeyDigest != "" && grant.RequestDigest != "" && grant.SchemaVersion != 0
}

// grantTimestampsPresent 判断 grant 是否包含完整生命周期时间戳。
func grantTimestampsPresent(grant accesswait.AccessGrant) bool {
	return !grant.IssuedAt.IsZero() && !grant.ExpiresAt.IsZero() && !grant.CreatedAt.IsZero() && !grant.UpdatedAt.IsZero()
}

func isZeroFence(f accesswait.FenceSnapshot) bool { return f == (accesswait.FenceSnapshot{}) }

// isZeroSelector 判断 selector 是否完全为空，用于识别 owner 未回显的可选证明。
func isZeroSelector(s accesswait.EventSelector) bool {
	return s.Version == 0 && s.SourceID == "" && s.SourceEpoch == "" && s.Lane == "" && s.TargetAgentID == "" && len(s.EventTypes) == 0
}

// grantIDFor derives a bounded opaque identity from the complete stable
// operation tuple. It is not a bearer capability: every owner read/use still
// requires the caller, scope, grant revision and fence checks.
func grantIDFor(request Request, target Target) string {
	return "grant:" + digestFor("grant-id", request.OperationKey, request.OperationDigest, target.Key)
}

func digestFor(domain, key, digest, target string) string {
	h := sha256.New()
	h.Write([]byte("v3.orchestration.wait-provision." + domain + "\x00"))
	h.Write([]byte(key))
	h.Write([]byte{0})
	h.Write([]byte(digest))
	h.Write([]byte{0})
	h.Write([]byte(target))
	return hex.EncodeToString(h.Sum(nil))
}

// boundedText 校验外部文本的边界、空白规范、UTF-8 和 NUL 安全性。
func boundedText(name, value string, max int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > max || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrInvalidRequest, name)
	}
	return nil
}

// Validate 检查请求边界，不从名称或路径推导任何 authority。
func (r Request) Validate() error { return r.ValidateAt(time.Now().UTC()) }

// ValidateAt 按指定 UTC 参考时间检查请求边界，并在 owner 调用前拒绝无效输入。
func (r Request) ValidateAt(now time.Time) error {
	if err := validateRequestIdentity(r); err != nil {
		return err
	}
	if err := validateRequestDeadline(r, now); err != nil {
		return err
	}
	if err := validateRequestTargetList(r); err != nil {
		return err
	}
	return nil
}

// validateRequestIdentity 校验 operation、caller、scope 和 source fence 的基础身份。
func validateRequestIdentity(r Request) error {
	if err := boundedText("operation_key", r.OperationKey, 256); err != nil {
		return err
	}
	if err := accesswaitDigest(r.OperationDigest, "operation_digest"); err != nil {
		return err
	}
	if err := boundedText("caller_principal_ref", r.CallerPrincipalRef, 512); err != nil {
		return err
	}
	if err := boundedText("scope_ref", r.ScopeRef, 512); err != nil {
		return err
	}
	if err := r.SourceFence.ValidateBound(); err != nil {
		return errors.Join(ErrInvalidRequest, err)
	}
	return nil
}

// validateRequestDeadline 强制 deadline 使用未来 UTC 时间且最多距离当前 60 秒。
func validateRequestDeadline(r Request, now time.Time) error {
	if r.DeadlineAt.IsZero() || r.DeadlineAt.Location() != time.UTC || !r.DeadlineAt.After(now) || r.DeadlineAt.Sub(now) > 60*time.Second {
		return fmt.Errorf("%w: deadline_at must be a future UTC time within 60 seconds", ErrInvalidRequest)
	}
	return nil
}

// validateRequestTargetList 校验 target 数量、唯一性以及每个 target 的 fence/cursor。
func validateRequestTargetList(r Request) error {
	if len(r.Targets) == 0 || len(r.Targets) > maxTargets {
		return fmt.Errorf("%w: targets must contain 1..32 items", ErrInvalidRequest)
	}
	seen := make(map[string]struct{}, len(r.Targets))
	for i, target := range r.Targets {
		if err := validateRequestTarget(r, target, i, seen); err != nil {
			return err
		}
	}
	return nil
}

// validateRequestTarget 校验单个 target 的 key、命名空间、fence 和 selector 绑定。
func validateRequestTarget(r Request, target Target, index int, seen map[string]struct{}) error {
	if err := boundedText(fmt.Sprintf("target[%d].key", index), target.Key, 128); err != nil {
		return err
	}
	if _, ok := seen[target.Key]; ok {
		return fmt.Errorf("%w: duplicate target key", ErrInvalidRequest)
	}
	seen[target.Key] = struct{}{}
	if err := validateTargetNamespace(r, target); err != nil {
		return err
	}
	if err := validateTargetFenceAndSelector(r, target); err != nil {
		return err
	}
	return validateTargetCursor(target)
}

// validateTargetNamespace 确保 operation/target 组合不会超出 owner 幂等命名空间上限。
func validateTargetNamespace(r Request, target Target) error {
	grantLen := len("wait-provision/") + len(r.OperationKey) + len("/grant/") + len(target.Key)
	waitLen := len("wait-provision/") + len(r.OperationKey) + len("/wait/") + len(target.Key)
	if grantLen > 256 || waitLen > 256 {
		return fmt.Errorf("%w: operation and target key exceed idempotency namespace bound", ErrInvalidRequest)
	}
	return nil
}

// validateTargetFenceAndSelector 校验 target fence 与 selector 的双向 agent/epoch 绑定。
func validateTargetFenceAndSelector(r Request, target Target) error {
	if err := target.TargetFence.ValidateBound(); err != nil {
		return errors.Join(ErrInvalidRequest, err)
	}
	if target.TargetFence.AgentID == r.SourceFence.AgentID {
		return fmt.Errorf("%w: source and target agents must differ", ErrInvalidRequest)
	}
	if target.Selector.TargetAgentID != target.TargetFence.AgentID {
		return fmt.Errorf("%w: target selector fence mismatch", ErrInvalidRequest)
	}
	if err := target.Selector.Validate(); err != nil {
		return errors.Join(ErrInvalidRequest, err)
	}
	selectorOnSource := target.Selector.SourceID == "task/"+r.SourceFence.AgentID && target.Selector.SourceEpoch == r.SourceFence.IncarnationID
	selectorOnTarget := target.Selector.SourceID == "task/"+target.TargetFence.AgentID && target.Selector.SourceEpoch == target.TargetFence.IncarnationID
	if !selectorOnSource && !selectorOnTarget {
		return fmt.Errorf("%w: selector source is not bound to source or target fence", ErrInvalidRequest)
	}
	return nil
}

// validateTargetCursor 强制 after sequence 与 canonical cursor 成对出现。
func validateTargetCursor(target Target) error {
	if target.AfterSequence < 0 {
		return fmt.Errorf("%w: negative after sequence", ErrInvalidRequest)
	}
	if target.AfterSequence == 0 {
		if target.AfterCursor != "" {
			return fmt.Errorf("%w: initial cursor must be empty", ErrInvalidRequest)
		}
		return nil
	}
	if target.AfterCursor == "" {
		return fmt.Errorf("%w: after cursor is required after the initial sequence", ErrInvalidRequest)
	}
	canonicalCursor, err := eventstore.CanonicalCursor(target.Selector.SourceID, target.Selector.SourceEpoch, target.Selector.Lane, target.AfterSequence)
	if err != nil {
		return errors.Join(ErrInvalidRequest, err)
	}
	if target.AfterCursor != canonicalCursor {
		return fmt.Errorf("%w: after cursor does not match selector source tuple", ErrInvalidRequest)
	}
	return nil
}

func accesswaitDigest(value, name string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be lowercase sha-256 hex", ErrInvalidRequest, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s must be hexadecimal", ErrInvalidRequest, name)
	}
	return nil
}

// ValidateFor 要求 replay 结果按 target 顺序完整呈现，并精确绑定全部 owner 字段。
func (r Result) ValidateFor(request Request) error {
	if r.OperationKey != request.OperationKey || r.OperationDigest != request.OperationDigest || len(r.Items) != len(request.Targets) {
		return ErrReplayConflict
	}
	for i, item := range r.Items {
		target := request.Targets[i]
		if item.Key != target.Key || item.Grant.GrantID == "" || item.Wait.WaitHandleID == "" {
			return ErrReplayConflict
		}
		expectedGrant := accesswait.AccessGrantRequest{
			GrantID: item.Grant.GrantID, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
			Source: request.SourceFence, Target: target.TargetFence, CanRead: true,
			IdempotencyNamespace: expectedGrantNamespace(request, target), IdempotencyKeyDigest: digestFor("grant", request.OperationKey, request.OperationDigest, target.Key), RequestDigest: request.OperationDigest,
			IssuedAt: item.Grant.IssuedAt, ExpiresAt: request.DeadlineAt,
		}
		if err := validateProvisionedGrant(request, target, item.Grant, item.Grant.IssuedAt, expectedGrant); err != nil {
			return ErrReplayConflict
		}
		expectedWait := accesswait.WaitRequest{
			GrantID: item.Grant.GrantID, GrantRevision: item.Grant.Revision, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
			Selector: target.Selector, AfterSequence: target.AfterSequence, AfterCursor: target.AfterCursor, DeadlineAt: request.DeadlineAt,
			IdempotencyNamespace: expectedWaitNamespace(request, target), IdempotencyKeyDigest: digestFor("wait", request.OperationKey, request.OperationDigest, target.Key), RequestDigest: request.OperationDigest,
		}
		if err := validateProvisionedWait(request, target, item.Grant, item.Wait, expectedWait); err != nil {
			return ErrReplayConflict
		}
	}
	return nil
}
