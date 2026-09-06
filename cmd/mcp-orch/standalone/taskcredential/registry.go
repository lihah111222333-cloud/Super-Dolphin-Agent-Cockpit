// Package taskcredential 提供 standalone task HTTP session 的短期内存 credential owner。
package taskcredential

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const (
	credentialEntropyBytes = 32
	credentialRedaction    = "[REDACTED]"
	leaseIDTextLimit       = 512
)

var (
	// ErrCredentialUnavailable 对外统一表示 credential 不存在、未激活、过期、已撤销或 owner 已关闭。
	ErrCredentialUnavailable = errors.New("task credential is unavailable")
	// ErrRegistryInvalid 表示 registry 缺少必需依赖或内部结构无效。
	ErrRegistryInvalid = errors.New("task credential registry is invalid")
	// ErrCredentialInputInvalid 表示签发或激活参数不满足 credential owner 契约。
	ErrCredentialInputInvalid = errors.New("task credential input is invalid")
)

// CurrentIdentityVerifier 在 credential 激活和每次认证时复验身份仍是 durable current。
type CurrentIdentityVerifier interface {
	Verify(context.Context, common.TaskSessionIdentity) error
}

// IssuedCredential 暂存尚未交给 child transport 的唯一 raw bearer 副本。
//
// registry 从不持有该对象；调用方必须通过 TakeBearer 显式且至多一次地取走 bearer，
// 或在 materialization 失败、取消和关闭路径调用 Close/Discard 清零未消费的 bearer。
type IssuedCredential struct {
	holder *issuedBearer
}

type issuedBearer struct {
	mu      sync.Mutex
	bearer  []byte
	leaseID string
}

var _ io.Closer = IssuedCredential{}

// TakeBearer 至多一次地返回 raw bearer，并尽力清零 holder 内的原始字节。
func (c IssuedCredential) TakeBearer() (string, error) {
	if c.holder == nil {
		return "", ErrCredentialUnavailable
	}
	c.holder.mu.Lock()
	defer c.holder.mu.Unlock()
	if len(c.holder.bearer) == 0 {
		return "", ErrCredentialUnavailable
	}
	bearer := string(c.holder.bearer)
	clear(c.holder.bearer)
	c.holder.bearer = nil
	return bearer, nil
}

// BoundLeaseID 返回签发时绑定的非秘密 lease 标识。
//
// 该标识与 holder 生命周期独立，Discard、Close 或 TakeBearer 不会清除；
// bearer 仍只能通过 TakeBearer 至多一次地取得，且不会由本方法暴露。
func (c IssuedCredential) BoundLeaseID() string {
	if c.holder == nil {
		return ""
	}
	c.holder.mu.Lock()
	defer c.holder.mu.Unlock()
	return c.holder.leaseID
}

// Discard 幂等清零尚未消费的 raw bearer。
//
// IssuedCredential 的值拷贝共享同一 holder，因此从任一拷贝调用 Discard 都会
// 使其它拷贝的 TakeBearer 立即失败。Discard 不影响 registry 中已签发的 digest
// 或其认证状态；它只销毁尚未交给 transport 的 bearer 材料。
func (c IssuedCredential) Discard() {
	if c.holder == nil {
		return
	}
	c.holder.mu.Lock()
	defer c.holder.mu.Unlock()
	clear(c.holder.bearer)
	c.holder.bearer = nil
}

// Close 实现 io.Closer，并将其语义收敛到 Discard。
//
// bearer wipe 没有可报告的失败，因此 Close 始终返回 nil；重复 Close、先
// TakeBearer 后 Close 以及并发 Close 都是安全且幂等的。
func (c IssuedCredential) Close() error {
	c.Discard()
	return nil
}

// String 返回固定脱敏文本。
func (IssuedCredential) String() string { return credentialRedaction }

// GoString 返回固定脱敏文本。
func (IssuedCredential) GoString() string { return credentialRedaction }

// Format 覆盖全部 fmt 动词，避免结构化格式输出 bearer 字节。
func (IssuedCredential) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, credentialRedaction)
}

// MarshalJSON 返回固定脱敏字符串。
func (IssuedCredential) MarshalJSON() ([]byte, error) {
	return json.Marshal(credentialRedaction)
}

// MarshalText 返回固定脱敏文本。
func (IssuedCredential) MarshalText() ([]byte, error) {
	return []byte(credentialRedaction), nil
}

type credentialState uint8

const (
	credentialStatePending credentialState = iota + 1
	credentialStateActive
	credentialStateRevoked
)

// credentialRecord 只保存不可逆摘要和 lease 生命周期事实，不保存 raw bearer 或身份快照。
type credentialRecord struct {
	digest     common.SessionCredentialDigest
	leaseID    string
	state      credentialState
	generation uint64
	expiresAt  time.Time
}

type activationSnapshot struct {
	record     *credentialRecord
	digest     common.SessionCredentialDigest
	generation uint64
	expiresAt  time.Time
}

type authenticationSnapshot struct {
	record     *credentialRecord
	identity   common.TaskSessionIdentity
	generation uint64
}

// Registry 是 standalone task credential 的唯一进程内 owner。
type Registry struct {
	mu        sync.Mutex
	entropyMu sync.Mutex

	verifier CurrentIdentityVerifier
	entropy  io.Reader
	now      func() time.Time

	closed           bool
	byLease          map[string]*credentialRecord
	byDigest         map[common.SessionCredentialDigest]*credentialRecord
	activeIdentities map[common.SessionCredentialDigest]common.TaskSessionIdentity
}

var _ common.TaskSessionAuthenticator = (*Registry)(nil)

// New 构造使用系统加密熵和 UTC 时钟的 credential registry。
func New(verifier CurrentIdentityVerifier) (*Registry, error) {
	return newRegistry(verifier, rand.Reader, func() time.Time { return time.Now().UTC() })
}

// newRegistry 仅为本包确定性测试注入熵源和时钟；生产入口固定使用 crypto/rand。
func newRegistry(
	verifier CurrentIdentityVerifier,
	entropy io.Reader,
	now func() time.Time,
) (*Registry, error) {
	registry := &Registry{
		verifier:         verifier,
		entropy:          entropy,
		now:              now,
		byLease:          make(map[string]*credentialRecord),
		byDigest:         make(map[common.SessionCredentialDigest]*credentialRecord),
		activeIdentities: make(map[common.SessionCredentialDigest]common.TaskSessionIdentity),
	}
	if err := registry.Validate(); err != nil {
		return nil, err
	}
	return registry, nil
}

// Validate 检查 common.TaskSessionAuthenticator 所需的 owner 依赖和内存索引。
func (r *Registry) Validate() error {
	if r == nil || currentIdentityVerifierIsNil(r.verifier) || readerIsNil(r.entropy) || r.now == nil {
		return ErrRegistryInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byLease == nil || r.byDigest == nil || r.activeIdentities == nil {
		return ErrRegistryInvalid
	}
	return nil
}

// IssuePending 为 lease 签发未激活的高熵 bearer；raw 值只进入返回的 holder。
func (r *Registry) IssuePending(
	ctx context.Context,
	leaseID string,
	expiresAt time.Time,
) (*IssuedCredential, error) {
	if err := r.validateIssueRequest(ctx, leaseID, expiresAt); err != nil {
		return nil, err
	}
	bearer, digest, err := r.generateBearer()
	if err != nil {
		return nil, err
	}
	if err := r.insertPending(ctx, leaseID, expiresAt, digest); err != nil {
		clear(bearer)
		return nil, err
	}
	return &IssuedCredential{holder: &issuedBearer{bearer: bearer, leaseID: leaseID}}, nil
}

// Activate 在锁外复验 current 身份，再以 generation CAS 将 pending lease 激活。
func (r *Registry) Activate(
	ctx context.Context,
	leaseID string,
	identity common.TaskSessionIdentity,
) error {
	if err := r.readyContext(ctx); err != nil || validateLeaseID(leaseID) != nil {
		return ErrCredentialUnavailable
	}
	snapshot, err := r.readPendingSnapshot(leaseID)
	if err != nil {
		return err
	}
	now, err := r.currentTime()
	if err != nil || !identityFitsCredential(identity, now, snapshot.expiresAt) {
		return ErrCredentialInputInvalid
	}
	if err := r.verifier.Verify(ctx, identity); err != nil {
		return ErrCredentialUnavailable
	}
	return r.commitActivation(ctx, leaseID, snapshot, identity)
}

// Authenticate 按 digest 读取 active 快照，锁外复验 current，再锁内复验代际与状态。
func (r *Registry) Authenticate(
	ctx context.Context,
	digest common.SessionCredentialDigest,
) (common.TaskSessionIdentity, error) {
	if err := r.readyContext(ctx); err != nil {
		return common.TaskSessionIdentity{}, ErrCredentialUnavailable
	}
	snapshot, err := r.readAuthenticationSnapshot(digest)
	if err != nil {
		return common.TaskSessionIdentity{}, err
	}
	if err := r.verifier.Verify(ctx, snapshot.identity); err != nil {
		return common.TaskSessionIdentity{}, ErrCredentialUnavailable
	}
	return r.commitAuthentication(ctx, digest, snapshot)
}

// Revoke 幂等撤销已知 lease，并立即清除 active 身份快照。
func (r *Registry) Revoke(ctx context.Context, leaseID string) error {
	if err := r.readyContext(ctx); err != nil || validateLeaseID(leaseID) != nil {
		return ErrCredentialUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.byLease[leaseID]
	if record == nil || record.leaseID != leaseID {
		return ErrCredentialUnavailable
	}
	r.revokeLocked(record)
	return nil
}

// Close 幂等关闭 owner，并在同一线性化点撤销全部 credential。
func (r *Registry) Close() error {
	if r == nil {
		return ErrRegistryInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	for _, record := range r.byLease {
		r.revokeLocked(record)
	}
	return nil
}

// validateIssueRequest 在读取熵源前校验 owner、context、lease 和 UTC expiry。
func (r *Registry) validateIssueRequest(ctx context.Context, leaseID string, expiresAt time.Time) error {
	if err := r.readyContext(ctx); err != nil {
		return err
	}
	now, err := r.currentTime()
	if err != nil {
		return ErrCredentialInputInvalid
	}
	if validateLeaseID(leaseID) != nil || validateExpiry(now, expiresAt) != nil {
		return ErrCredentialInputInvalid
	}
	return nil
}

// generateBearer 串行读取 32 字节加密熵，并只返回 bearer 与不可逆摘要。
func (r *Registry) generateBearer() ([]byte, common.SessionCredentialDigest, error) {
	raw := make([]byte, credentialEntropyBytes)
	r.entropyMu.Lock()
	_, err := io.ReadFull(r.entropy, raw)
	r.entropyMu.Unlock()
	if err != nil {
		clear(raw)
		return nil, common.SessionCredentialDigest{}, fmt.Errorf("%w: generate bearer", ErrRegistryInvalid)
	}
	bearer := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(bearer, raw)
	clear(raw)
	return bearer, common.SessionCredentialDigest(sha256.Sum256(bearer)), nil
}

// insertPending 在一个锁内线性化唯一 lease 与 digest 的 pending 记录。
func (r *Registry) insertPending(
	ctx context.Context,
	leaseID string,
	expiresAt time.Time,
	digest common.SessionCredentialDigest,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || ctx.Err() != nil {
		return ErrCredentialUnavailable
	}
	if _, exists := r.byLease[leaseID]; exists {
		return ErrCredentialUnavailable
	}
	if _, exists := r.byDigest[digest]; exists {
		return ErrCredentialUnavailable
	}
	record := &credentialRecord{
		digest: digest, leaseID: leaseID, state: credentialStatePending,
		generation: 1, expiresAt: expiresAt,
	}
	r.byLease[leaseID] = record
	r.byDigest[digest] = record
	return nil
}

// readPendingSnapshot 在锁内读取待激活记录的精确 CAS 前态。
func (r *Registry) readPendingSnapshot(leaseID string) (activationSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.byLease[leaseID]
	if record == nil || record.leaseID != leaseID || !r.pendingCurrentLocked(record) {
		return activationSnapshot{}, ErrCredentialUnavailable
	}
	return activationSnapshot{
		record: record, digest: record.digest, generation: record.generation, expiresAt: record.expiresAt,
	}, nil
}

// commitActivation 复验 pending 前态、当前时间和身份边界后执行唯一激活迁移。
func (r *Registry) commitActivation(
	ctx context.Context,
	leaseID string,
	snapshot activationSnapshot,
	identity common.TaskSessionIdentity,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.byLease[leaseID]
	if !r.activationSnapshotCurrentLocked(ctx, current, snapshot) {
		return ErrCredentialUnavailable
	}
	now, err := r.currentTime()
	if err != nil || !identityFitsCredential(identity, now, current.expiresAt) {
		r.revokeLocked(current)
		return ErrCredentialUnavailable
	}
	current.state = credentialStateActive
	current.generation++
	r.activeIdentities[snapshot.digest] = identity
	return nil
}

// activationSnapshotCurrentLocked 比较激活 CAS 的记录对象、摘要、代际和 pending 状态。
func (r *Registry) activationSnapshotCurrentLocked(
	ctx context.Context,
	current *credentialRecord,
	snapshot activationSnapshot,
) bool {
	if r.closed || ctx.Err() != nil || current != snapshot.record {
		return false
	}
	if current.generation != snapshot.generation || current.digest != snapshot.digest {
		return false
	}
	return r.pendingCurrentLocked(current)
}

// readAuthenticationSnapshot 在锁内读取 active 身份与代际快照。
func (r *Registry) readAuthenticationSnapshot(
	digest common.SessionCredentialDigest,
) (authenticationSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.byDigest[digest]
	identity, exists := r.activeIdentities[digest]
	if !exists || !r.activeCurrentLocked(record, identity) {
		return authenticationSnapshot{}, ErrCredentialUnavailable
	}
	return authenticationSnapshot{record: record, identity: identity, generation: record.generation}, nil
}

// commitAuthentication 在线性化返回点复验 digest、代际、状态和身份快照均未改变。
func (r *Registry) commitAuthentication(
	ctx context.Context,
	digest common.SessionCredentialDigest,
	snapshot authenticationSnapshot,
) (common.TaskSessionIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	identity, exists := r.activeIdentities[digest]
	if !r.authenticationSnapshotCurrentLocked(ctx, digest, identity, exists, snapshot) {
		return common.TaskSessionIdentity{}, ErrCredentialUnavailable
	}
	return identity, nil
}

// authenticationSnapshotCurrentLocked 比较认证 CAS 前后完整的 active snapshot。
func (r *Registry) authenticationSnapshotCurrentLocked(
	ctx context.Context,
	digest common.SessionCredentialDigest,
	identity common.TaskSessionIdentity,
	identityExists bool,
	snapshot authenticationSnapshot,
) bool {
	if r.closed || ctx.Err() != nil || !identityExists || r.byDigest[digest] != snapshot.record {
		return false
	}
	if snapshot.record.generation != snapshot.generation || !snapshot.identity.Equal(identity) {
		return false
	}
	return r.activeCurrentLocked(snapshot.record, identity)
}

// readyContext 检查 receiver、context 和构造期依赖均仍可用。
func (r *Registry) readyContext(ctx context.Context) error {
	if r == nil || contextIsNil(ctx) || ctx.Err() != nil || currentIdentityVerifierIsNil(r.verifier) ||
		readerIsNil(r.entropy) || r.now == nil {
		return ErrRegistryInvalid
	}
	return nil
}

// currentTime 返回非零 UTC 安全时钟观察值。
func (r *Registry) currentTime() (time.Time, error) {
	now := r.now()
	if now.IsZero() || now.Location() != time.UTC {
		return time.Time{}, ErrRegistryInvalid
	}
	return now, nil
}

// pendingCurrentLocked 在锁内复验 pending 记录，过期时立即转为 revoked。
func (r *Registry) pendingCurrentLocked(record *credentialRecord) bool {
	if r.closed || record == nil || record.state != credentialStatePending || !validCredentialState(record.state) {
		return false
	}
	now, err := r.currentTime()
	if err != nil || !record.expiresAt.After(now) {
		r.revokeLocked(record)
		return false
	}
	return true
}

// activeCurrentLocked 在锁内复验 active 记录和身份 expiry，失效时立即撤销。
func (r *Registry) activeCurrentLocked(
	record *credentialRecord,
	identity common.TaskSessionIdentity,
) bool {
	if r.closed || record == nil || record.state != credentialStateActive || !validCredentialState(record.state) {
		return false
	}
	now, err := r.currentTime()
	if err != nil || !record.expiresAt.After(now) || validateIdentityAt(identity, now) != nil ||
		identity.Principal.ExpiresAt.After(record.expiresAt) {
		r.revokeLocked(record)
		return false
	}
	return true
}

// revokeLocked 幂等撤销记录并删除其 active 身份快照。
func (r *Registry) revokeLocked(record *credentialRecord) {
	if record == nil {
		return
	}
	delete(r.activeIdentities, record.digest)
	if record.state == credentialStateRevoked {
		return
	}
	record.state = credentialStateRevoked
	record.generation++
}

// validateLeaseID 拒绝空、无界、非 canonical 或含控制字符的 lease 标识。
func validateLeaseID(leaseID string) error {
	if leaseID == "" || len(leaseID) > leaseIDTextLimit || !utf8.ValidString(leaseID) || strings.TrimSpace(leaseID) != leaseID {
		return ErrCredentialInputInvalid
	}
	for _, value := range leaseID {
		if unicode.IsControl(value) {
			return ErrCredentialInputInvalid
		}
	}
	return nil
}

// validateExpiry 要求 credential expiry 是严格未来的 UTC 时间。
func validateExpiry(now, expiresAt time.Time) error {
	if expiresAt.IsZero() || expiresAt.Location() != time.UTC || !expiresAt.After(now) {
		return ErrCredentialInputInvalid
	}
	return nil
}

// validateIdentityAt 使用同一安全时钟复验 principal 和 transport attribution。
func validateIdentityAt(identity common.TaskSessionIdentity, now time.Time) error {
	if err := identity.Principal.ValidateAt(now); err != nil {
		return err
	}
	return identity.Attribution.Validate()
}

// identityFitsCredential 要求身份有效且 principal expiry 不越过 credential expiry。
func identityFitsCredential(identity common.TaskSessionIdentity, now, credentialExpiry time.Time) bool {
	return validateIdentityAt(identity, now) == nil && !identity.Principal.ExpiresAt.After(credentialExpiry)
}

// allCredentialStates 返回状态消费者的唯一不可变登记。
func allCredentialStates() [3]credentialState {
	return [3]credentialState{
		credentialStatePending,
		credentialStateActive,
		credentialStateRevoked,
	}
}

// validCredentialState 只接受显式登记的 credential 生命周期状态。
func validCredentialState(state credentialState) bool {
	for _, registered := range allCredentialStates() {
		if state == registered {
			return true
		}
	}
	return false
}

// currentIdentityVerifierIsNil 拒绝 nil interface 和 typed-nil verifier。
func currentIdentityVerifierIsNil(verifier CurrentIdentityVerifier) bool {
	return interfaceIsNil(verifier)
}

// readerIsNil 拒绝 nil interface 和 typed-nil entropy reader。
func readerIsNil(reader io.Reader) bool {
	return interfaceIsNil(reader)
}

// contextIsNil 拒绝 nil interface 和 typed-nil context。
func contextIsNil(ctx context.Context) bool {
	return interfaceIsNil(ctx)
}

// interfaceIsNil 统一识别接口中承载的 nilable typed nil。
func interfaceIsNil(value any) bool {
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
