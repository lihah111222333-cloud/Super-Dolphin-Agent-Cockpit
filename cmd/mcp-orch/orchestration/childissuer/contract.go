// Package childissuer defines the parent-to-child task identity issuance
// candidate for the orchestration sidecar.
//
// The candidate only validates an identity returned by a future, independently
// authenticated issuer. It does not mint principals, generate bearer values,
// persist state, start a provider, or register a transport. Production wiring
// must remain disabled until the managed/native runtime and custody owners can
// provide the corresponding receipts.
package childissuer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const (
	identitySchemaVersion = 1
	issuerNonceDigestSize = 64
	maxRequestJSONBytes   = 1 << 20
	maxRequestJSONDepth   = 16
	maxRequestJSONMembers = 64
)

var (
	// ErrInvalidRequest means that the child reservation or binding is not a
	// complete, current, command-backed identity chain.
	ErrInvalidRequest = errors.New("child issuer request is invalid")
	// ErrParentUnavailable means that the authenticated parent task could not
	// be read as a current identity snapshot.
	ErrParentUnavailable = errors.New("child issuer parent task is unavailable")
	// ErrOperatorPrincipal means an operator transport was presented where a
	// task principal is required.
	ErrOperatorPrincipal = errors.New("operator principal cannot issue a child task")
	// ErrParentChildConflict means parent and child identity or scope binding
	// does not describe the requested delegation.
	ErrParentChildConflict = errors.New("parent and child task identities conflict")
	// ErrChildIdentityInvalid means the independent issuer returned an identity
	// that does not match the child reservation, binding, or issuer fence.
	ErrChildIdentityInvalid = errors.New("issued child task identity is invalid")
	// ErrIssuerUnavailable means the independent issuer is missing or failed.
	ErrIssuerUnavailable = errors.New("child identity issuer is unavailable")
	// ErrChildAlreadyIssued means this stable child identity already consumed an
	// issuance attempt. The map is only a defensive candidate guard, not a
	// cross-process authority.
	ErrChildAlreadyIssued = errors.New("child identity was already issued")
	// ErrIssuerReplay is retained as the compatibility name for a rejected
	// child-scoped replay.
	ErrIssuerReplay = ErrChildAlreadyIssued
)

// ParentResolver is the only parent identity read port consumed by this
// candidate. Implementations must return a resolver-verified current task.
// ParentResolver 是候选唯一的 parent 身份读取端口，必须返回 resolver 验证过的 current task。
type ParentResolver interface {
	RequireCurrent(context.Context) (taskprincipal.TaskPrincipal, error)
}

// IdentityIssuer is a future external authentication owner. It returns a
// complete child transport identity, never a raw bearer or provider secret.
// IdentityIssuer 是未来独立认证 owner，只返回完整 child transport identity，不返回 raw bearer。
type IdentityIssuer interface {
	IssueChildIdentity(context.Context, IssueRequest) (common.TaskSessionIdentity, error)
}

// Request identifies the child identity chain and independently issued nonce.
// Every field is supplied by an owner; no field is inferred from task names,
// paths, metadata, or the parent principal.
// Request 固定 child identity chain 与独立 nonce，所有字段都由 owner 显式提供。
type Request struct {
	ChildReservation  identity.Reservation    `json:"child_reservation"`
	ChildIncarnation  identity.Incarnation    `json:"child_incarnation"`
	ChildBinding      identity.CurrentBinding `json:"child_binding"`
	Issuer            common.IssuerBinding    `json:"issuer"`
	IssuerNonceDigest string                  `json:"issuer_nonce_digest"`
}

// IssueRequest is the delegate input after the parent resolver has verified
// the authenticated parent. It carries only identity/fence values.
// IssueRequest 是 parent resolver 验证后交给 delegate 的 identity/fence 输入。
type IssueRequest struct {
	Parent            taskprincipal.TaskPrincipal `json:"parent"`
	ChildReservation  identity.Reservation        `json:"child_reservation"`
	ChildIncarnation  identity.Incarnation        `json:"child_incarnation"`
	ChildBinding      identity.CurrentBinding     `json:"child_binding"`
	Issuer            common.IssuerBinding        `json:"issuer"`
	IssuerNonceDigest string                      `json:"issuer_nonce_digest"`
}

// Candidate is the non-authorizing result of a successful parent-to-child
// identity check. The session identity is intentionally private so default
// JSON/log formatting cannot expose its auth context.
// Candidate 是 parent→child 身份检查成功后的 non-authorizing 结果，session identity 私有保存。
type Candidate struct {
	ParentAgentID      string                `json:"parent_agent_id"`
	ParentPrincipalRef string                `json:"parent_principal_ref"`
	ParentScopeRef     string                `json:"parent_scope_ref"`
	ParentProfile      string                `json:"parent_profile"`
	ParentAuthDigest   string                `json:"parent_auth_context_digest"`
	ChildAgentID       string                `json:"child_agent_id"`
	ChildReservationID string                `json:"child_reservation_id"`
	ChildIncarnationID string                `json:"child_incarnation_id"`
	ChildIdentityKind  string                `json:"child_identity_kind"`
	ChildSourceKind    string                `json:"child_source_kind"`
	ChildExternalRef   string                `json:"child_external_identity_ref"`
	ChildExternalEpoch string                `json:"child_external_epoch"`
	ChildScopeRef      string                `json:"child_scope_ref"`
	ParentFence        common.ExecutionFence `json:"parent_fence"`
	ChildFence         common.ExecutionFence `json:"child_fence"`
	Issuer             common.IssuerBinding  `json:"issuer"`
	IssuerNonceDigest  string                `json:"issuer_nonce_digest"`
	identity           common.TaskSessionIdentity
	parentPrincipalRef string
	parentAuthContext  string
}

// CandidateIssuer validates a delegated child identity and rejects a second
// issuance for the same parent/child/issuer fence. Claiming a child is a
// one-shot attempt: an issuer error or malformed response consumes it, because
// the candidate cannot prove that an external effect did not happen. It is
// deliberately not an authenticator and has no production Fx/module constructor.
// CandidateIssuer 校验 delegated child identity，并拒绝同一稳定 child 的第二次尝试。
type CandidateIssuer struct {
	parent       ParentResolver
	issuer       IdentityIssuer
	mu           sync.Mutex
	usedChildren map[childIdentityKey]struct{}
}

// childIdentityKey 把 child reservation/incarnation/binding fence 固定为一项；
// parent fence 与 issuer 在每次尝试中重验，但不能绕过同 child 防重放。
type childIdentityKey struct {
	childReservationID string
	childIncarnationID string
	childFence         common.ExecutionFence
}

// NewCandidateIssuer 构造显式候选 owner；nil 与 typed-nil 端口必须直接拒绝。
func NewCandidateIssuer(parent ParentResolver, issuer IdentityIssuer) (*CandidateIssuer, error) {
	if interfaceIsNil(parent) || interfaceIsNil(issuer) {
		return nil, ErrIssuerUnavailable
	}
	return &CandidateIssuer{
		parent: parent, issuer: issuer,
		usedChildren: make(map[childIdentityKey]struct{}),
	}, nil
}

// Issue 读取 resolver 已验证的父 task，检查 child 链，并为 nonce 接受一次独立签发结果。
func (i *CandidateIssuer) Issue(ctx context.Context, request Request) (Candidate, error) {
	if err := i.validateIssueInputs(ctx, request); err != nil {
		return Candidate{}, err
	}
	parent, err := i.loadParent(ctx)
	if err != nil {
		return Candidate{}, err
	}
	if err := validateParent(parent); err != nil {
		return Candidate{}, err
	}
	if err := validateParentChildChain(parent, request); err != nil {
		return Candidate{}, err
	}
	if err := i.claimIssue(request); err != nil {
		return Candidate{}, err
	}
	issued, err := i.issueChildIdentity(ctx, parent, request)
	if err != nil {
		return Candidate{}, err
	}
	currentParent, err := i.loadParent(ctx)
	if err != nil {
		return Candidate{}, err
	}
	if err := validateParentStable(parent, currentParent); err != nil {
		return Candidate{}, err
	}
	if err := validateIssuedIdentity(currentParent, request, issued); err != nil {
		return Candidate{}, err
	}
	return buildCandidate(currentParent, request, issued)
}

// validateIssueInputs 在任何父读取或委托调用前检查 owner、context 和 child 请求。
func (i *CandidateIssuer) validateIssueInputs(ctx context.Context, request Request) error {
	if i == nil || interfaceIsNil(i.parent) || interfaceIsNil(i.issuer) {
		return ErrIssuerUnavailable
	}
	if err := validateContext(ctx); err != nil {
		return err
	}
	return request.Validate()
}

// loadParent 只通过 resolver 读取父 task，并折叠底层错误细节。
func (i *CandidateIssuer) loadParent(ctx context.Context) (taskprincipal.TaskPrincipal, error) {
	if err := validateContext(ctx); err != nil {
		return taskprincipal.TaskPrincipal{}, err
	}
	parent, err := i.parent.RequireCurrent(ctx)
	if err != nil {
		return taskprincipal.TaskPrincipal{}, classifyUnavailable(err, ErrParentUnavailable)
	}
	return parent, nil
}

// issueChildIdentity 将完整 parent/child fence 交给独立 issuer，并折叠私密错误。
func (i *CandidateIssuer) issueChildIdentity(
	ctx context.Context,
	parent taskprincipal.TaskPrincipal,
	request Request,
) (common.TaskSessionIdentity, error) {
	if err := validateContext(ctx); err != nil {
		return common.TaskSessionIdentity{}, err
	}
	issued, err := i.issuer.IssueChildIdentity(valueIsolatedContext{Context: ctx}, IssueRequest{
		Parent: parent, ChildReservation: request.ChildReservation,
		ChildIncarnation: request.ChildIncarnation, ChildBinding: request.ChildBinding,
		Issuer: request.Issuer, IssuerNonceDigest: request.IssuerNonceDigest,
	})
	if err != nil {
		return common.TaskSessionIdentity{}, classifyUnavailable(err, ErrIssuerUnavailable)
	}
	return issued, nil
}

// valueIsolatedContext 保留取消/截止时间，但隐藏 parent transport context values。
type valueIsolatedContext struct {
	context.Context
}

// Value 阻断 delegate 从 context 读取 parent principal 或客户端 metadata。
func (valueIsolatedContext) Value(any) any { return nil }

// buildCandidate 组装并复验不含 raw bearer 的候选结果。
func buildCandidate(
	parent taskprincipal.TaskPrincipal,
	request Request,
	issued common.TaskSessionIdentity,
) (Candidate, error) {
	candidate := Candidate{
		ParentAgentID: parent.Attribution.TaskID, ParentPrincipalRef: parent.Principal.PrincipalRef,
		ParentScopeRef: parent.Principal.ScopeRef, ParentProfile: parent.Principal.Profile,
		ParentAuthDigest: digestText(parent.Principal.AuthContext), ChildAgentID: request.ChildBinding.AgentID,
		ChildReservationID: request.ChildReservation.ReservationID, ChildIncarnationID: request.ChildIncarnation.IncarnationID,
		ChildIdentityKind: request.ChildReservation.IdentityKind, ChildSourceKind: request.ChildReservation.SourceKind,
		ChildExternalRef: request.ChildIncarnation.ExternalIdentityRef, ChildExternalEpoch: request.ChildIncarnation.ExternalEpoch,
		ChildScopeRef: request.ChildReservation.ScopeRef,
		ParentFence:   parent.Attribution.Source, ChildFence: executionFence(request.ChildBinding),
		Issuer: request.Issuer, IssuerNonceDigest: request.IssuerNonceDigest, identity: issued,
		parentPrincipalRef: parent.Principal.PrincipalRef, parentAuthContext: parent.Principal.AuthContext,
	}
	if err := candidate.Validate(); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

// SessionIdentity 返回给未来 credential activation owner 的已验证 child identity，不返回 bearer。
func (c Candidate) SessionIdentity() common.TaskSessionIdentity { return c.identity }

// Validate 重新检查候选公开 fence tuple 与私有 identity，阻断手工篡改。
func (c Candidate) Validate() error {
	if err := validateCandidateReferences(c); err != nil {
		return ErrChildIdentityInvalid
	}
	if err := validateCandidateMetadata(c); err != nil {
		return ErrChildIdentityInvalid
	}
	if err := validateCandidateFences(c); err != nil {
		return ErrChildIdentityInvalid
	}
	if err := validateCandidateIdentity(c); err != nil {
		return ErrChildIdentityInvalid
	}
	return nil
}

// validateCandidateReferences 检查候选中的 parent/child 标识互不相同且逐字绑定 fence。
func validateCandidateReferences(c Candidate) error {
	if !validText(c.ParentAgentID, 128, false) || !validText(c.ChildAgentID, 128, false) || c.ParentAgentID == c.ChildAgentID {
		return ErrChildIdentityInvalid
	}
	if !validText(c.ChildReservationID, 128, false) || !validText(c.ChildIncarnationID, 128, false) {
		return ErrChildIdentityInvalid
	}
	if c.ParentFence.AgentID != c.ParentAgentID || c.ChildFence.AgentID != c.ChildAgentID {
		return ErrChildIdentityInvalid
	}
	if c.ChildReservationID != c.ChildFence.ReservationID || c.ChildIncarnationID != c.ChildFence.IncarnationID {
		return ErrChildIdentityInvalid
	}
	return nil
}

// validateCandidateMetadata 检查候选保留的非秘密 parent/child 关联字段。
func validateCandidateMetadata(c Candidate) error {
	for _, field := range []struct {
		value string
		limit int
	}{
		{c.ParentPrincipalRef, 512}, {c.ParentScopeRef, 512}, {c.ParentProfile, 512},
		{c.ChildIdentityKind, 64}, {c.ChildSourceKind, 64}, {c.ChildExternalRef, 512},
		{c.ChildExternalEpoch, 128}, {c.ChildScopeRef, 512},
	} {
		if !validText(field.value, field.limit, false) {
			return ErrChildIdentityInvalid
		}
	}
	if !validDigest(c.ParentAuthDigest) || c.ParentAuthDigest != digestText(c.parentAuthContext) {
		return ErrChildIdentityInvalid
	}
	return nil
}

// validateCandidateFences 检查 parent/child execution fence、issuer 与 nonce digest 的形状。
func validateCandidateFences(c Candidate) error {
	if err := validateNonceDigest(c.IssuerNonceDigest); err != nil {
		return err
	}
	if err := validateFence(c.ParentFence); err != nil {
		return err
	}
	if err := validateFence(c.ChildFence); err != nil {
		return err
	}
	return validateIssuer(c.Issuer)
}

// validateCandidateIdentity 检查候选内部 identity 不重用 parent 连接或身份。
func validateCandidateIdentity(c Candidate) error {
	if !validText(c.parentPrincipalRef, 512, false) || !validText(c.parentAuthContext, 512, false) {
		return ErrChildIdentityInvalid
	}
	if err := c.identity.Validate(); err != nil || isOperatorPrincipal(c.identity.Principal) {
		return ErrChildIdentityInvalid
	}
	if c.identity.Principal.PrincipalRef == c.parentPrincipalRef || c.identity.Principal.AuthContext == c.parentAuthContext {
		return ErrChildIdentityInvalid
	}
	if c.identity.Attribution.TaskID != c.ChildAgentID || c.identity.Attribution.Source != c.ChildFence || c.identity.Attribution.Issuer != c.Issuer {
		return ErrChildIdentityInvalid
	}
	return nil
}

// MarshalJSON 只序列化候选公开字段，隐藏私有 session identity。
func (c Candidate) MarshalJSON() ([]byte, error) {
	type publicCandidate struct {
		ParentAgentID      string                `json:"parent_agent_id"`
		ParentPrincipalRef string                `json:"parent_principal_ref"`
		ParentScopeRef     string                `json:"parent_scope_ref"`
		ParentProfile      string                `json:"parent_profile"`
		ParentAuthDigest   string                `json:"parent_auth_context_digest"`
		ChildAgentID       string                `json:"child_agent_id"`
		ChildReservationID string                `json:"child_reservation_id"`
		ChildIncarnationID string                `json:"child_incarnation_id"`
		ChildIdentityKind  string                `json:"child_identity_kind"`
		ChildSourceKind    string                `json:"child_source_kind"`
		ChildExternalRef   string                `json:"child_external_identity_ref"`
		ChildExternalEpoch string                `json:"child_external_epoch"`
		ChildScopeRef      string                `json:"child_scope_ref"`
		ParentFence        common.ExecutionFence `json:"parent_fence"`
		ChildFence         common.ExecutionFence `json:"child_fence"`
		Issuer             common.IssuerBinding  `json:"issuer"`
		IssuerNonceDigest  string                `json:"issuer_nonce_digest"`
	}
	return json.Marshal(publicCandidate{
		ParentAgentID: c.ParentAgentID, ParentPrincipalRef: c.ParentPrincipalRef, ParentScopeRef: c.ParentScopeRef,
		ParentProfile: c.ParentProfile, ParentAuthDigest: c.ParentAuthDigest, ChildAgentID: c.ChildAgentID,
		ChildReservationID: c.ChildReservationID, ChildIncarnationID: c.ChildIncarnationID,
		ChildIdentityKind: c.ChildIdentityKind, ChildSourceKind: c.ChildSourceKind,
		ChildExternalRef: c.ChildExternalRef, ChildExternalEpoch: c.ChildExternalEpoch,
		ChildScopeRef: c.ChildScopeRef,
		ParentFence:   c.ParentFence, ChildFence: c.ChildFence, Issuer: c.Issuer,
		IssuerNonceDigest: c.IssuerNonceDigest,
	})
}

// MarshalJSON 序列化 delegate request 时隐藏 parent principal auth context，只保留 fence 引用。
func (r IssueRequest) MarshalJSON() ([]byte, error) {
	type publicIssueRequest struct {
		ParentAgentID      string                  `json:"parent_agent_id"`
		ParentPrincipalRef string                  `json:"parent_principal_ref"`
		ParentScopeRef     string                  `json:"parent_scope_ref"`
		ParentProfile      string                  `json:"parent_profile"`
		ParentAuthDigest   string                  `json:"parent_auth_context_digest"`
		ParentFence        common.ExecutionFence   `json:"parent_fence"`
		ChildReservation   identity.Reservation    `json:"child_reservation"`
		ChildIncarnation   identity.Incarnation    `json:"child_incarnation"`
		ChildBinding       identity.CurrentBinding `json:"child_binding"`
		Issuer             common.IssuerBinding    `json:"issuer"`
		IssuerNonceDigest  string                  `json:"issuer_nonce_digest"`
	}
	return json.Marshal(publicIssueRequest{
		ParentAgentID: r.Parent.Attribution.TaskID, ParentPrincipalRef: r.Parent.Principal.PrincipalRef,
		ParentScopeRef: r.Parent.Principal.ScopeRef, ParentProfile: r.Parent.Principal.Profile,
		ParentAuthDigest: digestText(r.Parent.Principal.AuthContext), ParentFence: r.Parent.Attribution.Source,
		ChildReservation: r.ChildReservation, ChildIncarnation: r.ChildIncarnation, ChildBinding: r.ChildBinding,
		Issuer: r.Issuer, IssuerNonceDigest: r.IssuerNonceDigest,
	})
}

// String 返回不含 principal auth 数据的 delegate request 摘要。
func (r IssueRequest) String() string {
	return fmt.Sprintf("child identity issue parent=%s child=%s issuer=%s", r.Parent.Attribution.TaskID, r.ChildBinding.AgentID, r.Issuer.Kind)
}

// GoString 为 %#v 格式化保留脱敏 request 摘要。
func (r IssueRequest) GoString() string { return r.String() }

// String 返回不含 auth context 或 bearer 的候选 identity 摘要。
func (c Candidate) String() string {
	return fmt.Sprintf("child identity candidate parent=%s child=%s issuer=%s", c.ParentAgentID, c.ChildAgentID, c.Issuer.Kind)
}

// GoString 为 %#v 格式化保留同样的候选脱敏摘要。
func (c Candidate) GoString() string { return c.String() }

// Validate 在任何 delegate 调用前检查 child request 的字段和关系。
func (r Request) Validate() error {
	if err := validateNonceDigest(r.IssuerNonceDigest); err != nil {
		return fmt.Errorf("%w: issuer nonce digest", ErrInvalidRequest)
	}
	if err := validateChildReservation(r.ChildReservation); err != nil {
		return err
	}
	if err := validateChildIncarnation(r.ChildIncarnation); err != nil {
		return err
	}
	if err := validateChildBinding(r.ChildBinding, r.Issuer); err != nil {
		return err
	}
	return validateChildRelations(r)
}

// DecodeRequest 严格解码候选请求，拒绝 duplicate、unknown、null、尾随和非 canonical JSON。
func DecodeRequest(payload []byte) (Request, error) {
	if err := validateRequestJSONShape(payload); err != nil {
		return Request{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("%w: strict JSON decode", ErrInvalidRequest)
	}
	if err := requireRequestJSONEOF(decoder); err != nil {
		return Request{}, err
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	canonical, err := json.Marshal(request)
	if err != nil || !bytes.Equal(payload, canonical) {
		return Request{}, fmt.Errorf("%w: JSON is not canonical", ErrInvalidRequest)
	}
	return request, nil
}

// validateRequestJSONShape 先限制大小，再递归检查 object、array token 和键唯一性。
func validateRequestJSONShape(payload []byte) error {
	if len(payload) == 0 || len(payload) > maxRequestJSONBytes {
		return fmt.Errorf("%w: JSON payload exceeds bound", ErrInvalidRequest)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return fmt.Errorf("%w: top-level JSON object is required", ErrInvalidRequest)
	}
	if err := scanRequestJSONObject(decoder, 0); err != nil {
		return err
	}
	return requireRequestJSONTokenEOF(decoder)
}

// scanRequestJSONObject 递归检查对象成员数、重复键和嵌套深度。
func scanRequestJSONObject(decoder *json.Decoder, depth int) error {
	if depth > maxRequestJSONDepth {
		return fmt.Errorf("%w: JSON nesting exceeds bound", ErrInvalidRequest)
	}
	seen := make(map[string]struct{})
	for members := 0; decoder.More(); members++ {
		if members >= maxRequestJSONMembers {
			return fmt.Errorf("%w: JSON object has too many members", ErrInvalidRequest)
		}
		key, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%w: malformed JSON object key", ErrInvalidRequest)
		}
		name, ok := key.(string)
		if !ok {
			return fmt.Errorf("%w: JSON object key is not text", ErrInvalidRequest)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("%w: duplicate JSON object field", ErrInvalidRequest)
		}
		seen[name] = struct{}{}
		if err := scanRequestJSONValue(decoder, depth); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return fmt.Errorf("%w: malformed JSON object", ErrInvalidRequest)
	}
	return nil
}

// scanRequestJSONValue 拒绝 null 并递归检查 object、array 分支。
func scanRequestJSONValue(decoder *json.Decoder, depth int) error {
	value, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON value", ErrInvalidRequest)
	}
	if value == nil {
		return fmt.Errorf("%w: null JSON value is forbidden", ErrInvalidRequest)
	}
	delimiter, ok := value.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanRequestJSONObject(decoder, depth+1)
	case '[':
		return scanRequestJSONArray(decoder, depth+1)
	default:
		return fmt.Errorf("%w: unexpected JSON delimiter", ErrInvalidRequest)
	}
}

// scanRequestJSONArray 递归限制数组成员数和深度。
func scanRequestJSONArray(decoder *json.Decoder, depth int) error {
	if depth > maxRequestJSONDepth {
		return fmt.Errorf("%w: JSON nesting exceeds bound", ErrInvalidRequest)
	}
	for members := 0; decoder.More(); members++ {
		if members >= maxRequestJSONMembers {
			return fmt.Errorf("%w: JSON array has too many members", ErrInvalidRequest)
		}
		if err := scanRequestJSONValue(decoder, depth); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return fmt.Errorf("%w: malformed JSON array", ErrInvalidRequest)
	}
	return nil
}

// requireRequestJSONEOF 拒绝合法对象后的第二个 JSON value。
func requireRequestJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON value", ErrInvalidRequest)
	}
	return nil
}

// requireRequestJSONTokenEOF 拒绝合法对象后的额外 token。
func requireRequestJSONTokenEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON token", ErrInvalidRequest)
	}
	return nil
}

// validateChildRelations 确认 reservation、incarnation 与 current binding 是同一代。
func validateChildRelations(r Request) error {
	if !sameStrings(r.ChildReservation.AgentID, r.ChildIncarnation.AgentID, r.ChildBinding.AgentID) {
		return fmt.Errorf("%w: child agent relation", ErrInvalidRequest)
	}
	if !sameStrings(r.ChildReservation.ReservationID, r.ChildIncarnation.ReservationID, r.ChildBinding.ReservationID) {
		return fmt.Errorf("%w: child reservation relation", ErrInvalidRequest)
	}
	if !sameStrings(r.ChildReservation.IdentityKind, r.ChildIncarnation.IdentityKind, r.ChildBinding.IdentityKind) {
		return fmt.Errorf("%w: child identity kind relation", ErrInvalidRequest)
	}
	if !sameStrings(r.ChildReservation.SourceFenceRef, r.ChildIncarnation.SourceFenceRef, r.ChildBinding.SourceFenceRef) {
		return fmt.Errorf("%w: child source fence relation", ErrInvalidRequest)
	}
	if !sameInt64s(r.ChildIncarnation.IncarnationGeneration, r.ChildBinding.IncarnationGeneration) {
		return fmt.Errorf("%w: child incarnation generation relation", ErrInvalidRequest)
	}
	if !sameInt64s(r.ChildReservation.ReservationGeneration, r.ChildBinding.ReservationGeneration) {
		return fmt.Errorf("%w: child generation relation", ErrInvalidRequest)
	}
	if r.ChildIncarnation.IncarnationID != r.ChildBinding.IncarnationID {
		return fmt.Errorf("%w: child incarnation relation", ErrInvalidRequest)
	}
	if r.ChildIncarnation.ExternalIdentityRef != r.ChildBinding.ExternalIdentityRef {
		return fmt.Errorf("%w: child external identity relation", ErrInvalidRequest)
	}
	return nil
}

// sameStrings 判断一组关系字段是否逐字相等。
func sameStrings(values ...string) bool {
	if len(values) < 2 {
		return false
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return false
		}
	}
	return true
}

// sameInt64s 判断一组代际字段是否逐值相等。
func sameInt64s(values ...int64) bool {
	if len(values) < 2 {
		return false
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return false
		}
	}
	return true
}

// validateParent 检查 resolver 返回的 parent 是否仍是 active task identity。
func validateParent(parent taskprincipal.TaskPrincipal) error {
	if err := parent.Principal.Validate(); err != nil {
		return ErrParentUnavailable
	}
	if err := parent.Attribution.Validate(); err != nil || parent.Binding.SchemaVersion != identitySchemaVersion || parent.Binding.State != "active" ||
		parent.Attribution.TaskID != parent.Binding.AgentID || parent.Attribution.Source != executionFence(parent.Binding) {
		return ErrParentUnavailable
	}
	if !validText(parent.Binding.ExternalIdentityRef, 512, false) || !validText(parent.Binding.ActiveProcessReceiptRef, 512, false) {
		return ErrParentUnavailable
	}
	if isOperatorPrincipal(parent.Principal) {
		return ErrOperatorPrincipal
	}
	return nil
}

// validateParentStable 在 delegate 返回后重读 parent，拒绝期间发生的代际或连接变化。
func validateParentStable(before, after taskprincipal.TaskPrincipal) error {
	if err := validateParent(after); err != nil {
		return err
	}
	if before.Principal.PrincipalRef != after.Principal.PrincipalRef || before.Principal.ScopeRef != after.Principal.ScopeRef ||
		before.Principal.Profile != after.Principal.Profile || before.Principal.AuthContext != after.Principal.AuthContext ||
		!before.Principal.ExpiresAt.Equal(after.Principal.ExpiresAt) || before.Attribution != after.Attribution || before.Binding != after.Binding {
		return ErrParentChildConflict
	}
	return nil
}

// validateParentChildChain 确认 child reservation 由当前 parent 在同一 scope 发起。
func validateParentChildChain(parent taskprincipal.TaskPrincipal, request Request) error {
	if request.Issuer == parent.Attribution.Issuer {
		return ErrParentChildConflict
	}
	reservation := request.ChildReservation
	if reservation.ParentAgentID == "" || reservation.ParentAgentID != parent.Attribution.TaskID {
		return ErrParentChildConflict
	}
	if reservation.AgentID == parent.Attribution.TaskID || reservation.CallerPrincipalRef != parent.Principal.PrincipalRef {
		return ErrParentChildConflict
	}
	if reservation.ScopeRef != parent.Principal.ScopeRef {
		return ErrParentChildConflict
	}
	return nil
}

// validateIssuedIdentity 检查独立 issuer 的 child identity 与目标 fence 完全相等。
func validateIssuedIdentity(parent taskprincipal.TaskPrincipal, request Request, issued common.TaskSessionIdentity) error {
	if err := issued.Validate(); err != nil {
		return ErrChildIdentityInvalid
	}
	if isOperatorPrincipal(issued.Principal) || issued.Principal.PrincipalRef == parent.Principal.PrincipalRef ||
		issued.Principal.AuthContext == parent.Principal.AuthContext || issued.Principal.ScopeRef != request.ChildReservation.ScopeRef {
		return ErrParentChildConflict
	}
	expected, err := common.NewCallerAttribution(request.ChildBinding.AgentID, executionFence(request.ChildBinding), request.Issuer)
	if err != nil || issued.Attribution != expected {
		return ErrChildIdentityInvalid
	}
	return nil
}

// validateChildReservation 检查 child reservation 是已激活、命令驱动的 managed/native 身份。
func validateChildReservation(reservation identity.Reservation) error {
	commandID := ""
	if reservation.CommandID != nil {
		commandID = *reservation.CommandID
	}
	request := identity.ReservationRequest{
		ReservationID: reservation.ReservationID, CommandID: commandID, AgentID: reservation.AgentID,
		IdentityKind: reservation.IdentityKind, SourceKind: reservation.SourceKind,
		CallerPrincipalRef: reservation.CallerPrincipalRef, ScopeRef: reservation.ScopeRef,
		ParentAgentID: reservation.ParentAgentID, IdempotencyNamespace: reservation.IdempotencyNamespace,
		IdempotencyKeyDigest: reservation.IdempotencyKeyDigest, RequestDigest: reservation.RequestDigest,
		SourceFenceRef: reservation.SourceFenceRef, TargetFenceRef: reservation.TargetFenceRef,
		ExpectedBindingVersion: reservation.ExpectedBindingVersion, ReservationGeneration: reservation.ReservationGeneration,
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("%w: child reservation", ErrInvalidRequest)
	}
	if err := validateChildReservationState(reservation); err != nil {
		return fmt.Errorf("%w: child reservation", ErrInvalidRequest)
	}
	return nil
}

// validateChildReservationState 固定可签发 child 的 reservation 状态与来源判别。
func validateChildReservationState(reservation identity.Reservation) error {
	if reservation.SchemaVersion != identitySchemaVersion || reservation.State != "activated" || reservation.ParentAgentID == "" {
		return ErrInvalidRequest
	}
	if reservation.IdentityKind != "managed_agent" && reservation.IdentityKind != "native_subagent" {
		return ErrInvalidRequest
	}
	// Native children need an independent zero-effect/adoption proof; none is
	// carried by this candidate, so command-backed native issuance is denied.
	if reservation.IdentityKind == "native_subagent" {
		return ErrInvalidRequest
	}
	if reservation.SourceKind != "first_launch" && reservation.SourceKind != "recover" && reservation.SourceKind != "replace" {
		return ErrInvalidRequest
	}
	return nil
}

// validateChildIncarnation 检查 child incarnation 的不可变身份和代际字段。
func validateChildIncarnation(incarnation identity.Incarnation) error {
	request := identity.IncarnationRequest{
		IncarnationID: incarnation.IncarnationID, ReservationID: incarnation.ReservationID,
		AgentID: incarnation.AgentID, IdentityKind: incarnation.IdentityKind,
		ExternalIdentityRef: incarnation.ExternalIdentityRef, ExternalEpoch: incarnation.ExternalEpoch,
		SourceFenceRef: incarnation.SourceFenceRef, IdentityDigest: incarnation.IdentityDigest,
		IncarnationGeneration: incarnation.IncarnationGeneration,
	}
	if err := request.Validate(); err != nil || incarnation.SchemaVersion != identitySchemaVersion {
		return fmt.Errorf("%w: child incarnation", ErrInvalidRequest)
	}
	return nil
}

// validateChildBinding 检查 active current binding、issuer 和完整执行围栏。
func validateChildBinding(binding identity.CurrentBinding, issuer common.IssuerBinding) error {
	if err := validateChildBindingMetadata(binding); err != nil {
		return fmt.Errorf("%w: child binding", ErrInvalidRequest)
	}
	if err := validateChildBindingKind(binding.IdentityKind); err != nil {
		return fmt.Errorf("%w: child binding identity kind", ErrInvalidRequest)
	}
	if err := validateIssuer(issuer); err != nil {
		return fmt.Errorf("%w: child issuer binding", ErrInvalidRequest)
	}
	if _, err := common.NewCallerAttribution(binding.AgentID, executionFence(binding), issuer); err != nil {
		return fmt.Errorf("%w: child execution fence", ErrInvalidRequest)
	}
	return nil
}

// validateChildBindingMetadata 检查 current binding 的完整非空字段和版本。
func validateChildBindingMetadata(binding identity.CurrentBinding) error {
	if binding.SchemaVersion != identitySchemaVersion || binding.State != "active" || binding.BindingVersion <= 0 ||
		binding.ReservationGeneration <= 0 || binding.IncarnationGeneration <= 0 {
		return ErrInvalidRequest
	}
	for _, field := range []struct {
		value string
		limit int
	}{
		{binding.AgentID, 128}, {binding.ReservationID, 128}, {binding.IncarnationID, 128},
		{binding.IdentityKind, 64}, {binding.ExternalIdentityRef, 512}, {binding.SourceFenceRef, 512},
		{binding.ActiveProcessReceiptRef, 512},
	} {
		if !validText(field.value, field.limit, false) {
			return ErrInvalidRequest
		}
	}
	if !validDigest(binding.BindingDigest) {
		return ErrInvalidRequest
	}
	return nil
}

// validateChildBindingKind 限制 child current binding 只能是 managed/native 类型。
func validateChildBindingKind(kind string) error {
	if kind != "managed_agent" && kind != "native_subagent" {
		return ErrInvalidRequest
	}
	return nil
}

// validateIssuer 限制 child issuer 为已登记的 managed runtime 或 provider bridge。
func validateIssuer(issuer common.IssuerBinding) error {
	if issuer.Kind != common.CallerAttributionIssuerManagedRuntime && issuer.Kind != common.CallerAttributionIssuerProviderBridge {
		return ErrInvalidRequest
	}
	if !validText(issuer.BindingRef, 512, false) || issuer.Generation <= 0 || !validDigest(issuer.Digest) {
		return ErrInvalidRequest
	}
	return nil
}

// claimIssue 在 delegate 产生任何可能副作用前线性化稳定 child 的一次性尝试。
func (i *CandidateIssuer) claimIssue(request Request) error {
	child := childIdentityKey{
		childReservationID: request.ChildReservation.ReservationID, childIncarnationID: request.ChildIncarnation.IncarnationID,
		childFence: executionFence(request.ChildBinding),
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, exists := i.usedChildren[child]; exists {
		return ErrChildAlreadyIssued
	}
	i.usedChildren[child] = struct{}{}
	return nil
}

// executionFence 从 current binding 投影完整执行围栏。
func executionFence(binding identity.CurrentBinding) common.ExecutionFence {
	return common.ExecutionFence{
		AgentID: binding.AgentID, ReservationID: binding.ReservationID,
		ReservationGeneration: binding.ReservationGeneration, IncarnationID: binding.IncarnationID,
		IncarnationGeneration: binding.IncarnationGeneration, BindingVersion: binding.BindingVersion,
		BindingDigest: binding.BindingDigest, SourceFenceRef: binding.SourceFenceRef,
	}
}

// validateFence 检查公开 execution fence 的必要标识和摘要。
func validateFence(fence common.ExecutionFence) error {
	for _, field := range []struct {
		value string
		limit int
	}{
		{fence.AgentID, 128}, {fence.ReservationID, 128}, {fence.IncarnationID, 128}, {fence.SourceFenceRef, 512},
	} {
		if !validText(field.value, field.limit, false) {
			return ErrInvalidRequest
		}
	}
	if fence.ReservationGeneration <= 0 || fence.IncarnationGeneration <= 0 || fence.BindingVersion <= 0 || !validDigest(fence.BindingDigest) {
		return ErrInvalidRequest
	}
	return nil
}

// validateNonceDigest 检查 issuer nonce 是小写 SHA-256 文本。
func validateNonceDigest(value string) error {
	if len(value) != issuerNonceDigestSize || strings.ToLower(value) != value {
		return ErrInvalidRequest
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ErrInvalidRequest
	}
	return nil
}

// validDigest 判断值是否为小写 SHA-256 十六进制摘要。
func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// digestText 为仅可读的非秘密上下文生成小写 SHA-256 摘要。
func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// validText 判断标识文本是否有界、UTF-8 合法且没有控制字符。
func validText(value string, limit int, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	if len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsRune(value, 0) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

// isOperatorPrincipal 识别不得进入 task issuer 的 operator profile/ref。
func isOperatorPrincipal(principal common.CallerPrincipal) bool {
	profile := strings.ToLower(principal.Profile)
	ref := strings.ToLower(principal.PrincipalRef)
	return profile == "operator" || profile == "standalone_http_local_v1" || strings.HasPrefix(profile, "operator/") || strings.HasPrefix(ref, "operator/")
}

// validateContext 检查上下文非空且尚未取消。
func validateContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// classifyUnavailable 将 delegate/parent 的底层错误折叠到稳定哨兵。
func classifyUnavailable(err, fallback error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fallback
}

// interfaceIsNil 同时识别 nil interface 和承载 nil 指针的 typed nil。
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
