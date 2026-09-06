package unifiedv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

var (
	ErrCommandSubmissionInspectorDisabled     = errors.New("command_submission inspector candidate is disabled")
	ErrCommandSubmissionInspectorUnavailable  = errors.New("command_submission inspector projection is unavailable")
	ErrCommandSubmissionInspectorInvalid      = errors.New("command_submission inspector request is invalid")
	ErrCommandSubmissionInspectorMismatch     = errors.New("command_submission inspector identity mismatch")
	ErrCommandSubmissionInspectorForeignFence = errors.New("command_submission inspector foreign fence")
)

// CommandSubmissionInspectInput is the complete immutable identity supplied by
// the caller. Every field is compared with both durable rows in one transaction.
type CommandSubmissionInspectInput struct {
	CommandID            string `json:"command_id"`
	CommandVersion       int64  `json:"command_version"`
	SubmissionID         string `json:"submission_id"`
	SubjectSelector      string `json:"subject_selector"`
	CallerPrincipalRef   string `json:"caller_principal_ref"`
	ScopeRef             string `json:"scope_ref"`
	SourceFenceRef       string `json:"source_fence_ref"`
	TargetFenceRef       string `json:"target_fence_ref"`
	IdempotencyNamespace string `json:"idempotency_namespace"`
	IdempotencyKeyDigest string `json:"idempotency_key_digest"`
	RequestDigest        string `json:"request_digest"`
}

// CommandSubmissionIdentity is the identity echoed by every projection.
type CommandSubmissionIdentity struct {
	CommandID            string `json:"command_id"`
	CommandVersion       int64  `json:"command_version"`
	SubmissionID         string `json:"submission_id"`
	SubjectSelector      string `json:"subject_selector"`
	CallerPrincipalRef   string `json:"caller_principal_ref"`
	ScopeRef             string `json:"scope_ref"`
	SourceFenceRef       string `json:"source_fence_ref"`
	TargetFenceRef       string `json:"target_fence_ref"`
	IdempotencyNamespace string `json:"idempotency_namespace"`
	IdempotencyKeyDigest string `json:"idempotency_key_digest"`
	RequestDigest        string `json:"request_digest"`
}

// CommandSubmissionFence is a command admission fence, not an execution head.
type CommandSubmissionFence struct {
	Ref             string `json:"ref"`
	Digest          string `json:"digest"`
	Cursor          string `json:"cursor"`
	CommandID       string `json:"command_id"`
	CommandVersion  int64  `json:"command_version"`
	SubjectSelector string `json:"subject_selector"`
}

// CommandSubmissionAcceptedProjection is admission-only metadata. It never
// proves provider dispatch, execution, event delivery, or terminal completion.
type CommandSubmissionAcceptedProjection struct {
	CommandFence          CommandSubmissionFence `json:"command_fence"`
	NonTerminal           bool                   `json:"non_terminal"`
	AcceptReceiptRef      string                 `json:"accept_receipt_ref"`
	AcceptReceiptDigest   string                 `json:"accept_receipt_digest"`
	RecoveryReceiptRef    string                 `json:"recovery_receipt_ref"`
	RecoveryReceiptDigest string                 `json:"recovery_receipt_digest"`
}

// CommandSubmissionRejectedProjection is the authoritative admission rejection.
type CommandSubmissionRejectedProjection struct {
	CommandFence          CommandSubmissionFence `json:"command_fence"`
	RejectReceiptRef      string                 `json:"reject_receipt_ref"`
	RejectReceiptDigest   string                 `json:"reject_receipt_digest"`
	RecoveryReceiptRef    string                 `json:"recovery_receipt_ref"`
	RecoveryReceiptDigest string                 `json:"recovery_receipt_digest"`
}

// CommandSubmissionInspectionStatus is the only externally visible outcome
// axis; accepted means admission accepted, never execution completed.
type CommandSubmissionInspectionStatus string

const (
	CommandSubmissionInspectionIndeterminate CommandSubmissionInspectionStatus = "indeterminate"
	CommandSubmissionInspectionAccepted      CommandSubmissionInspectionStatus = "accepted"
	CommandSubmissionInspectionRejected      CommandSubmissionInspectionStatus = "rejected"
)

// CommandSubmissionInspection is a read-only acceptance-resolution projection.
// ExecutionTerminal is always false: accepted metadata is not execution state.
type CommandSubmissionInspection struct {
	Status              CommandSubmissionInspectionStatus    `json:"status"`
	IndeterminateReason string                               `json:"indeterminate_reason,omitempty"`
	Identity            CommandSubmissionIdentity            `json:"identity"`
	Revision            int64                                `json:"revision"`
	AdmissionOnly       bool                                 `json:"admission_only"`
	ExecutionTerminal   bool                                 `json:"execution_terminal"`
	Accepted            *CommandSubmissionAcceptedProjection `json:"accepted,omitempty"`
	Rejected            *CommandSubmissionRejectedProjection `json:"rejected,omitempty"`
}

// CommandSubmissionInspectorReader is the narrow durable read seam. Implementations
// must use the supplied transaction for both submission and intent reads.
type CommandSubmissionInspectorReader interface {
	GetTx(context.Context, storedb.DBTX, orchestrationcommand.DurableSubmissionKey) (orchestrationcommand.DurableSubmission, error)
	GetIntentTx(context.Context, storedb.DBTX, string) (orchestrationcommand.Record, error)
}

// CommandSubmissionInspector is an unregistered, side-effect-free candidate.
type CommandSubmissionInspector struct {
	reader CommandSubmissionInspectorReader
}

// NewCommandSubmissionInspector 构造不依赖 Fx、注册表、provider、room 或执行组件的候选检查器。
func NewCommandSubmissionInspector(reader CommandSubmissionInspectorReader) (*CommandSubmissionInspector, error) {
	if isNil(reader) {
		return nil, ErrCommandSubmissionInspectorDisabled
	}
	return &CommandSubmissionInspector{reader: reader}, nil
}

// NewCommandSubmissionInspectorCandidate 返回候选检查器构造函数的显式别名。
func NewCommandSubmissionInspectorCandidate(reader CommandSubmissionInspectorReader) (*CommandSubmissionInspector, error) {
	return NewCommandSubmissionInspector(reader)
}

// Inspect 在调用方提供的事务 DBTX 上读取提交与意图，并返回只读检查投影。
func (i *CommandSubmissionInspector) Inspect(ctx context.Context, dbtx storedb.DBTX, input CommandSubmissionInspectInput) (CommandSubmissionInspection, error) {
	if i == nil || isNil(i.reader) {
		return CommandSubmissionInspection{}, ErrCommandSubmissionInspectorDisabled
	}
	if ctx == nil {
		return CommandSubmissionInspection{}, fmt.Errorf("%w: nil context", ErrCommandSubmissionInspectorInvalid)
	}
	if err := validateCommandSubmissionInspectInput(input); err != nil {
		return CommandSubmissionInspection{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return CommandSubmissionInspection{}, fmt.Errorf("%w: %v", ErrCommandSubmissionInspectorUnavailable, err)
	}
	key := orchestrationcommand.DurableSubmissionKey{CommandID: input.CommandID, SubmissionID: input.SubmissionID}
	submission, err := i.reader.GetTx(ctx, dbtx, key)
	if err != nil {
		return CommandSubmissionInspection{}, fmt.Errorf("%w: submission: %v", ErrCommandSubmissionInspectorUnavailable, err)
	}
	intent, err := i.reader.GetIntentTx(ctx, dbtx, input.CommandID)
	if err != nil {
		return CommandSubmissionInspection{}, fmt.Errorf("%w: intent: %v", ErrCommandSubmissionInspectorUnavailable, err)
	}
	if err := matchInspectIdentity(input, submission, intent); err != nil {
		return CommandSubmissionInspection{}, err
	}
	if err := validateSubmissionResolutionProjection(submission); err != nil {
		return CommandSubmissionInspection{}, err
	}
	return projectCommandSubmission(submission), nil
}

// InspectTx 为只读调用方提供明确表达事务语义的检查入口。
func (i *CommandSubmissionInspector) InspectTx(ctx context.Context, dbtx storedb.DBTX, input CommandSubmissionInspectInput) (CommandSubmissionInspection, error) {
	return i.Inspect(ctx, dbtx, input)
}

// HandleTx 严格解码 JSON 后委托 Inspect；它不是工具处理器，不注册到工具表。
func (i *CommandSubmissionInspector) HandleTx(ctx context.Context, dbtx storedb.DBTX, raw json.RawMessage) (CommandSubmissionInspection, error) {
	input, err := decodeCommandSubmissionInspectInput(raw)
	if err != nil {
		return CommandSubmissionInspection{}, err
	}
	return i.Inspect(ctx, dbtx, input)
}

// DecodeCommandSubmissionInspectInput 向测试和离线客户端暴露严格线协议解码器，但不注册公开工具。
func DecodeCommandSubmissionInspectInput(raw json.RawMessage) (CommandSubmissionInspectInput, error) {
	return decodeCommandSubmissionInspectInput(raw)
}

// decodeCommandSubmissionInspectInput 校验输入形态、拒绝未知字段并执行身份字段规范化检查。
func decodeCommandSubmissionInspectInput(raw json.RawMessage) (CommandSubmissionInspectInput, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return CommandSubmissionInspectInput{}, fmt.Errorf("%w: input must be a non-null object", ErrCommandSubmissionInspectorInvalid)
	}
	if err := scanWaitJSON(trimmed); err != nil {
		return CommandSubmissionInspectInput{}, fmt.Errorf("%w: %v", ErrCommandSubmissionInspectorInvalid, err)
	}
	var input CommandSubmissionInspectInput
	if err := shared.DecodeInput(trimmed, &input); err != nil {
		return CommandSubmissionInspectInput{}, fmt.Errorf("%w: %v", ErrCommandSubmissionInspectorInvalid, err)
	}
	if err := validateCommandSubmissionInspectInput(input); err != nil {
		return CommandSubmissionInspectInput{}, err
	}
	return input, nil
}

// validateCommandSubmissionInspectInput 检查字符串规范、版本号和摘要格式，缺失或异常立即失败。
func validateCommandSubmissionInspectInput(input CommandSubmissionInspectInput) error {
	for _, field := range []struct {
		name, value string
		max         int
	}{{"command_id", input.CommandID, 128}, {"submission_id", input.SubmissionID, 128}, {"subject_selector", input.SubjectSelector, 512}, {"caller_principal_ref", input.CallerPrincipalRef, 512}, {"scope_ref", input.ScopeRef, 512}, {"source_fence_ref", input.SourceFenceRef, 512}, {"target_fence_ref", input.TargetFenceRef, 512}, {"idempotency_namespace", input.IdempotencyNamespace, 256}} {
		if field.value == "" || strings.TrimSpace(field.value) != field.value || len(field.value) > field.max || !utf8.ValidString(field.value) || strings.ContainsRune(field.value, 0) {
			return fmt.Errorf("%w: %s is not canonical", ErrCommandSubmissionInspectorInvalid, field.name)
		}
	}
	if input.CommandVersion != 1 || !isSHA256Digest(input.IdempotencyKeyDigest) || !isSHA256Digest(input.RequestDigest) {
		return fmt.Errorf("%w: command version or digest is invalid", ErrCommandSubmissionInspectorInvalid)
	}
	return nil
}

// matchInspectIdentity 按照外部围栏、输入回显和提交/意图一致性顺序校验身份。
func matchInspectIdentity(input CommandSubmissionInspectInput, submission orchestrationcommand.DurableSubmission, intent orchestrationcommand.Record) error {
	r := submission.Request
	i := intent.Intent
	if !matchInspectFenceRefs(input, r, i) {
		return ErrCommandSubmissionInspectorForeignFence
	}
	if !matchInspectInputIdentity(input, r, i, intent.CommandVersion) || !matchInspectRequestIdentity(input, r) || !matchInspectIntentIdentity(r, i, intent.CommandVersion) {
		return ErrCommandSubmissionInspectorMismatch
	}
	return nil
}

// matchInspectFenceRefs 确认输入和两个持久化投影使用同一来源与目标围栏。
func matchInspectFenceRefs(input CommandSubmissionInspectInput, request orchestrationcommand.DurableSubmissionRequest, intent orchestrationcommand.Intent) bool {
	return input.SourceFenceRef == request.SourceFenceRef &&
		input.TargetFenceRef == request.TargetFenceRef &&
		input.SourceFenceRef == intent.SourceFenceRef &&
		input.TargetFenceRef == intent.TargetFenceRef
}

// matchInspectInputIdentity 校验输入中的命令标识与意图版本回显。
func matchInspectInputIdentity(input CommandSubmissionInspectInput, request orchestrationcommand.DurableSubmissionRequest, intent orchestrationcommand.Intent, intentVersion int64) bool {
	return input.CommandID == intent.CommandID && input.CommandVersion == intentVersion && input.CommandID == request.CommandID && input.CommandVersion == request.CommandVersion
}

// matchInspectRequestIdentity 校验输入与持久化提交请求的完整身份字段。
func matchInspectRequestIdentity(input CommandSubmissionInspectInput, request orchestrationcommand.DurableSubmissionRequest) bool {
	return input.SubmissionID == request.SubmissionID &&
		input.SubjectSelector == request.SubjectSelector &&
		input.CallerPrincipalRef == request.CallerPrincipalRef &&
		input.ScopeRef == request.ScopeRef &&
		input.IdempotencyNamespace == request.IdempotencyNamespace &&
		input.IdempotencyKeyDigest == request.IdempotencyKeyDigest &&
		input.RequestDigest == request.RequestDigest
}

// matchInspectIntentIdentity 校验提交请求与持久化意图的共享身份字段。
func matchInspectIntentIdentity(request orchestrationcommand.DurableSubmissionRequest, intent orchestrationcommand.Intent, intentVersion int64) bool {
	return request.CommandID == intent.CommandID &&
		request.CommandVersion == intentVersion &&
		request.CallerPrincipalRef == intent.CallerPrincipalRef &&
		request.ScopeRef == intent.ScopeRef &&
		request.SourceFenceRef == intent.SourceFenceRef &&
		request.TargetFenceRef == intent.TargetFenceRef &&
		request.IdempotencyNamespace == intent.IdempotencyNamespace &&
		request.IdempotencyKeyDigest == intent.IdempotencyKeyDigest &&
		request.RequestDigest == intent.RequestDigest
}

// projectCommandSubmission 将持久化接受决议转换为明确标注 admission-only 的只读投影。
func projectCommandSubmission(submission orchestrationcommand.DurableSubmission) CommandSubmissionInspection {
	identity := CommandSubmissionIdentity{CommandID: submission.Request.CommandID, CommandVersion: submission.Request.CommandVersion, SubmissionID: submission.Request.SubmissionID, SubjectSelector: submission.Request.SubjectSelector, CallerPrincipalRef: submission.Request.CallerPrincipalRef, ScopeRef: submission.Request.ScopeRef, SourceFenceRef: submission.Request.SourceFenceRef, TargetFenceRef: submission.Request.TargetFenceRef, IdempotencyNamespace: submission.Request.IdempotencyNamespace, IdempotencyKeyDigest: submission.Request.IdempotencyKeyDigest, RequestDigest: submission.Request.RequestDigest}
	out := CommandSubmissionInspection{Status: CommandSubmissionInspectionIndeterminate, Identity: identity, Revision: submission.Revision, ExecutionTerminal: false}
	switch submission.Resolution {
	case orchestrationcommand.AcceptAccepted:
		out.Status, out.AdmissionOnly = CommandSubmissionInspectionAccepted, true
		out.Accepted = &CommandSubmissionAcceptedProjection{CommandFence: submissionFence(submission.AcceptedCommandFence), NonTerminal: true, AcceptReceiptRef: submission.AcceptReceiptRef, AcceptReceiptDigest: submission.AcceptReceiptDigest, RecoveryReceiptRef: submission.RecoveryReceiptRef, RecoveryReceiptDigest: submission.RecoveryReceiptDigest}
	case orchestrationcommand.AcceptAborted:
		out.Status, out.AdmissionOnly = CommandSubmissionInspectionRejected, true
		out.Rejected = &CommandSubmissionRejectedProjection{CommandFence: submissionFence(submission.RejectedCommandFence), RejectReceiptRef: submission.RejectReceiptRef, RejectReceiptDigest: submission.RejectReceiptDigest, RecoveryReceiptRef: submission.RecoveryReceiptRef, RecoveryReceiptDigest: submission.RecoveryReceiptDigest}
	case orchestrationcommand.AcceptPending:
		out.IndeterminateReason = "accept_resolution_pending"
	default:
		out.IndeterminateReason = "accept_resolution_unknown"
	}
	return out
}

// validateSubmissionResolutionProjection 校验接受决议及其围栏投影的形状和归属。
func validateSubmissionResolutionProjection(submission orchestrationcommand.DurableSubmission) error {
	switch submission.Resolution {
	case orchestrationcommand.AcceptPending:
		return validatePendingResolutionProjection(submission)
	case orchestrationcommand.AcceptAccepted:
		return validateAcceptedResolutionProjection(submission)
	case orchestrationcommand.AcceptAborted:
		return validateRejectedResolutionProjection(submission)
	}
	return nil
}

// validatePendingResolutionProjection 拒绝带有任何终态围栏的待处理投影。
func validatePendingResolutionProjection(submission orchestrationcommand.DurableSubmission) error {
	if submission.ResolutionKind != orchestrationcommand.ResolutionPending || submission.AcceptedCommandFence != (orchestrationcommand.CommandFence{}) || submission.RejectedCommandFence != (orchestrationcommand.CommandFence{}) {
		return fmt.Errorf("%w: pending resolution projection is malformed", ErrCommandSubmissionInspectorUnavailable)
	}
	return nil
}

// validateAcceptedResolutionProjection 确认接受投影仅携带匹配请求的接受围栏。
func validateAcceptedResolutionProjection(submission orchestrationcommand.DurableSubmission) error {
	if submission.ResolutionKind != orchestrationcommand.ResolutionAccepted || !fenceMatchesRequest(submission.AcceptedCommandFence, submission.Request) || submission.RejectedCommandFence != (orchestrationcommand.CommandFence{}) {
		return ErrCommandSubmissionInspectorForeignFence
	}
	return nil
}

// validateRejectedResolutionProjection 确认拒绝投影仅携带匹配请求的拒绝围栏。
func validateRejectedResolutionProjection(submission orchestrationcommand.DurableSubmission) error {
	if submission.ResolutionKind != orchestrationcommand.ResolutionRejected || !fenceMatchesRequest(submission.RejectedCommandFence, submission.Request) || submission.AcceptedCommandFence != (orchestrationcommand.CommandFence{}) {
		return ErrCommandSubmissionInspectorForeignFence
	}
	return nil
}

// fenceMatchesRequest 判断命令围栏是否属于给定提交请求。
func fenceMatchesRequest(fence orchestrationcommand.CommandFence, request orchestrationcommand.DurableSubmissionRequest) bool {
	return fence.Ref != "" && fence.CommandID == request.CommandID && fence.CommandVersion == request.CommandVersion && fence.SubjectSelector == request.SubjectSelector
}

func submissionFence(f orchestrationcommand.CommandFence) CommandSubmissionFence {
	return CommandSubmissionFence{Ref: f.Ref, Digest: f.Digest, Cursor: f.Cursor, CommandID: f.CommandID, CommandVersion: f.CommandVersion, SubjectSelector: f.SubjectSelector}
}

// isSHA256Digest 判断摘要是否为小写十六进制编码的 SHA-256 值。
func isSHA256Digest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}
