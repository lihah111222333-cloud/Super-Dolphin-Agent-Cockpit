package sqliteauthority

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

type rowScanner interface {
	Scan(...any) error
}

type authorityHead struct {
	HeadID                     string
	Revision                   int64
	RootDigest                 string
	SecurityTimeLineageDigest  string
	SecurityTimeGeneration     int64
	SecurityTimeRevision       int64
	AntiRollbackGeneration     int64
	AntiRollbackAnchorDigest   string
	AntiRollbackAttestationRef string
	ActiveResolutionID         sql.NullString
}

type storedResolution struct {
	Expected ExpectedResolution
	State    string
}

type storedSubmissionRow struct {
	Request       recoverycustody.SubmissionRequest
	HandleDigest  string
	RequestJSON   string
	HandleJSON    string
	Status        string
	ReceiptDigest sql.NullString
}

type storedResolutionRow struct {
	Stored                    storedResolution
	OperationID               string
	FinalizationRequestDigest string
	RequestJSON               string
}

type terminalReceiptRow struct {
	Receipt                           recoverycustody.ExternalReceipt
	ResolutionID                      string
	AuthorityTime                     string
	RetentionUntil                    string
	ReceiptJSON                       string
	ReceiptCheckpointGeneration       int64
	SubmissionStatus                  string
	SubmissionReceiptDigest           sql.NullString
	Resolution                        storedResolutionRow
	ResolutionCheckpointGeneration    sql.NullInt64
	ResolutionCheckpointAnchor        sql.NullString
	ResolutionResultHeadRoot          sql.NullString
	ResolutionFinalizationDigest      sql.NullString
	CheckpointGeneration              int64
	CheckpointPredecessorGeneration   int64
	CheckpointPredecessorAnchorDigest string
	CheckpointAnchorDigest            string
	CheckpointResultHeadRootDigest    string
	CheckpointFinalizationDigest      string
}

// submissionInsertArgs 将提交请求与句柄编码为 canonical JSON，并按持久化列顺序生成参数。
func submissionInsertArgs(request recoverycustody.SubmissionRequest, handle recoverycustody.SubmissionHandle, now string) ([]any, error) {
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode authority submission: %w", err)
	}
	handleJSON, err := json.Marshal(handle)
	if err != nil {
		return nil, fmt.Errorf("encode authority handle: %w", err)
	}
	return []any{request.OperationID, request.SchemaVersion, request.SecurityDomainID, request.AuthorityProfile,
		request.CommandID, request.CommandVersion, request.CommandProfileDigest, request.AuthorizationDigest,
		request.IdentityFenceDigest, request.ContentSnapshotDigest, request.ResourceSnapshotDigest, request.BatchDigest,
		request.IdempotencyKeyDigest, request.RequestDigest, handle.HandleDigest, string(requestJSON), string(handleJSON), now, now}, nil
}

// scanSubmission 扫描提交记录并校验请求、句柄及终态回执占位之间的身份一致性。
func scanSubmission(row rowScanner) (recoverycustody.SubmissionRequest, recoverycustody.Outcome, error) {
	var stored storedSubmissionRow
	if err := row.Scan(submissionScanTargets(&stored)...); err != nil {
		return recoverycustody.SubmissionRequest{}, recoverycustody.Outcome{}, err
	}
	request, handle := stored.Request, submissionHandle(stored)
	var encodedRequest recoverycustody.SubmissionRequest
	if err := strictJSON(stored.RequestJSON, &encodedRequest); err != nil || encodedRequest != request {
		return recoverycustody.SubmissionRequest{}, recoverycustody.Outcome{}, ErrSchemaMismatch
	}
	var encodedHandle recoverycustody.SubmissionHandle
	if err := strictJSON(stored.HandleJSON, &encodedHandle); err != nil || encodedHandle != handle {
		return recoverycustody.SubmissionRequest{}, recoverycustody.Outcome{}, ErrSchemaMismatch
	}
	if err := request.Validate(); err != nil {
		return recoverycustody.SubmissionRequest{}, recoverycustody.Outcome{}, ErrSchemaMismatch
	}
	if err := recoverycustody.MatchSubmission(handle, request); err != nil {
		return recoverycustody.SubmissionRequest{}, recoverycustody.Outcome{}, ErrSchemaMismatch
	}
	if !submissionStateMatches(stored.Status, stored.ReceiptDigest) {
		return recoverycustody.SubmissionRequest{}, recoverycustody.Outcome{}, ErrSchemaMismatch
	}
	outcome := recoverycustody.Outcome{Handle: handle, Status: stored.Status}
	return request, outcome, nil
}

// submissionScanTargets 固定 submission 标量、JSON 与终态占位的读取顺序。
func submissionScanTargets(stored *storedSubmissionRow) []any {
	r := &stored.Request
	return []any{&r.SchemaVersion, &r.OperationID, &r.SecurityDomainID, &r.AuthorityProfile, &r.CommandID,
		&r.CommandVersion, &r.CommandProfileDigest, &r.AuthorizationDigest, &r.IdentityFenceDigest,
		&r.ContentSnapshotDigest, &r.ResourceSnapshotDigest, &r.BatchDigest, &r.IdempotencyKeyDigest,
		&r.RequestDigest, &stored.HandleDigest, &stored.RequestJSON, &stored.HandleJSON, &stored.Status,
		&stored.ReceiptDigest}
}

// submissionHandle 只从持久化标量列重建查询句柄，不把 JSON 当作事实源。
func submissionHandle(stored storedSubmissionRow) recoverycustody.SubmissionHandle {
	r := stored.Request
	return recoverycustody.SubmissionHandle{SchemaVersion: r.SchemaVersion, OperationID: r.OperationID,
		SecurityDomainID: r.SecurityDomainID, AuthorityProfile: r.AuthorityProfile, CommandID: r.CommandID,
		CommandVersion: r.CommandVersion, RequestDigest: r.RequestDigest, BatchDigest: r.BatchDigest,
		HandleDigest: stored.HandleDigest}
}

// submissionStateMatches 要求 pending 没有回执摘要，终态必须持有回执摘要。
func submissionStateMatches(status string, receiptDigest sql.NullString) bool {
	if status == recoverycustody.StatusPending {
		return !receiptDigest.Valid
	}
	return (status == recoverycustody.StatusAvailable || status == recoverycustody.StatusAborted) && receiptDigest.Valid
}

// scanAuthorityHead 按权威头查询列顺序扫描当前头部及其防回滚身份。
func scanAuthorityHead(row rowScanner) (authorityHead, error) {
	var head authorityHead
	err := row.Scan(&head.HeadID, &head.Revision, &head.RootDigest, &head.SecurityTimeLineageDigest,
		&head.SecurityTimeGeneration, &head.SecurityTimeRevision, &head.AntiRollbackGeneration,
		&head.AntiRollbackAnchorDigest, &head.AntiRollbackAttestationRef, &head.ActiveResolutionID)
	return head, err
}

// resolutionInsertArgs 将预期解决请求编码为 canonical JSON，并按持久化列顺序生成参数。
func resolutionInsertArgs(expected ExpectedResolution, now string) ([]any, error) {
	finalization, err := BindFinalizationRequest(expected)
	if err != nil {
		return nil, err
	}
	requestJSON, err := json.Marshal(expected.ResolutionRequest)
	if err != nil {
		return nil, fmt.Errorf("encode authority resolution: %w", err)
	}
	return []any{expected.ResolutionID, expected.Handle.OperationID, expected.SchemaVersion, expected.Kind,
		expected.ResolutionRequestDigest, expected.FinalizationOperationID, expected.ExpectedResolutionDigest,
		finalization.FinalizationRequestDigest, string(requestJSON), expected.HeadID, expected.ExpectedHeadRevision, expected.ExpectedHeadRootDigest,
		expected.ExpectedAntiRollbackGeneration, expected.ExpectedAnchorDigest, expected.SecurityTimeLineageDigest,
		expected.ExpectedSecurityTimeGeneration, expected.ExpectedSecurityTimeRevision, now, now}, nil
}

// scanStoredResolution 扫描解决记录并校验其 canonical 请求及预期身份。
func scanStoredResolution(row rowScanner) (storedResolution, error) {
	var scanned storedResolutionRow
	if err := row.Scan(resolutionScanTargets(&scanned)...); err != nil {
		return storedResolution{}, err
	}
	if err := validateStoredResolutionRow(scanned); err != nil {
		return storedResolution{}, ErrSchemaMismatch
	}
	return scanned.Stored, nil
}

// resolutionScanTargets 固定 ResolutionRequest 标量、JSON 与 expected tuple 的读取顺序。
func resolutionScanTargets(scanned *storedResolutionRow) []any {
	e := &scanned.Stored.Expected
	r := &e.ResolutionRequest
	h := &r.Handle
	return []any{&r.SchemaVersion, &r.ResolutionID, &scanned.OperationID, &h.SchemaVersion, &h.OperationID,
		&h.SecurityDomainID, &h.AuthorityProfile, &h.CommandID, &h.CommandVersion, &h.RequestDigest, &h.BatchDigest,
		&h.HandleDigest, &r.Kind, &r.ResolutionRequestDigest, &r.FinalizationOperationID,
		&e.ExpectedResolutionDigest, &scanned.FinalizationRequestDigest, &scanned.RequestJSON, &e.HeadID, &e.ExpectedHeadRevision,
		&e.ExpectedHeadRootDigest, &e.ExpectedAntiRollbackGeneration, &e.ExpectedAnchorDigest,
		&e.SecurityTimeLineageDigest, &e.ExpectedSecurityTimeGeneration, &e.ExpectedSecurityTimeRevision,
		&scanned.Stored.State}
}

// validateStoredResolutionRow 双向核对 resolution 标量、canonical JSON 与 expected tuple。
func validateStoredResolutionRow(scanned storedResolutionRow) error {
	var encoded ResolutionRequest
	if err := strictJSON(scanned.RequestJSON, &encoded); err != nil {
		return err
	}
	if encoded != scanned.Stored.Expected.ResolutionRequest || scanned.OperationID != encoded.Handle.OperationID {
		return ErrSchemaMismatch
	}
	if err := scanned.Stored.Expected.Validate(); err != nil {
		return err
	}
	finalization, err := BindFinalizationRequest(scanned.Stored.Expected)
	if err != nil || finalization.FinalizationRequestDigest != scanned.FinalizationRequestDigest {
		return ErrSchemaMismatch
	}
	if !validResolutionState(scanned.Stored.State) {
		return ErrSchemaMismatch
	}
	return nil
}

// validResolutionState 拒绝 schema 约束之外的解析状态。
func validResolutionState(state string) bool {
	return state == resolutionStateActive || state == resolutionStateAnchored || state == resolutionStateTerminal
}

// terminalReceiptInsertArgs 按终态回执列顺序生成参数，并保留回执与检查点的联合身份。
func terminalReceiptInsertArgs(expected ExpectedResolution, evidence FinalizedEvidence, receiptJSON, now string) []any {
	r := evidence.Receipt
	return []any{r.OperationID, expected.ResolutionID, r.SchemaVersion, r.Kind, r.ReceiptID, r.AuthorityID,
		r.AuthorityClusterID, r.AuthorityProfile, r.AuthorityGeneration, r.OwnerID, r.OwnerGeneration,
		r.AuthorityProfileGeneration, r.AuthorityProfileDigest, r.SecurityDomainID, r.HeadID, r.HeadRevision,
		r.HeadRootDigest, r.RequestDigest, r.BatchDigest, r.SecurityTimeLineageDigest, r.SecurityTimeGeneration,
		r.SecurityTimeRevision, r.AntiRollbackGeneration, r.AntiRollbackAnchorDigest, r.QuorumAttestationDigest,
		r.QuorumAttestationRef, r.SignatureDigest, r.SignatureRef, canonicalTime(r.AuthorityTime),
		r.RetentionPolicyDigest, canonicalTime(r.RetentionUntil), r.GCNotBeforeAnchorGeneration, r.ReceiptDigest,
		receiptJSON, evidence.Checkpoint.Generation, now}
}

// validateEvidenceIdentity 校验终态证据与配置、预期解决请求共享同一权威身份。
func validateEvidenceIdentity(config Config, request FinalizationRequest, evidence FinalizedEvidence) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := evidence.Validate(request.ExpectedResolution); err != nil {
		return err
	}
	r := evidence.Receipt
	if !receiptAuthorityIdentityMatches(config, r) {
		return fmt.Errorf("%w: receipt authority identity", ErrInvalidEvidence)
	}
	if evidence.Checkpoint.FinalizationDigest != request.FinalizationRequestDigest {
		return fmt.Errorf("%w: finalization digest", ErrInvalidEvidence)
	}
	return nil
}

// receiptAuthorityIdentityMatches 汇总权威 owner 与安全策略两个独立身份分组的比较结果。
func receiptAuthorityIdentityMatches(config Config, receipt recoverycustody.ExternalReceipt) bool {
	return receiptOwnerIdentityMatches(config, receipt) && receiptSecurityIdentityMatches(config, receipt)
}

// receiptOwnerIdentityMatches 校验回执由配置声明的权威集群、代际与 owner 生成。
func receiptOwnerIdentityMatches(config Config, receipt recoverycustody.ExternalReceipt) bool {
	return receipt.AuthorityID == config.AuthorityID &&
		receipt.AuthorityClusterID == config.AuthorityClusterID &&
		receipt.AuthorityGeneration == config.AuthorityGeneration &&
		receipt.OwnerID == config.OwnerID &&
		receipt.OwnerGeneration == config.OwnerGeneration
}

// receiptSecurityIdentityMatches 校验回执的权威配置、安全时间谱系及留存策略身份。
func receiptSecurityIdentityMatches(config Config, receipt recoverycustody.ExternalReceipt) bool {
	return receipt.AuthorityProfileGeneration == config.AuthorityProfileGeneration &&
		receipt.AuthorityProfileDigest == config.AuthorityProfileDigest &&
		receipt.SecurityTimeLineageDigest == config.SecurityTimeLineageDigest &&
		receipt.RetentionPolicyDigest == config.RetentionPolicyDigest
}

// loadTerminalOutcome 读取并严格解码终态回执，再通过验证器将其应用到待决结果。
func loadTerminalOutcome(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, operationID string, pending recoverycustody.Outcome, config Config, verifier recoverycustody.ReceiptVerifier) (recoverycustody.Outcome, error) {
	receipt, err := scanTerminalReceipt(queryer.QueryRowContext(ctx, selectTerminalReceiptSQL, operationID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return recoverycustody.Outcome{}, ErrSchemaMismatch
		}
		if errors.Is(err, ErrSchemaMismatch) {
			return recoverycustody.Outcome{}, ErrSchemaMismatch
		}
		return recoverycustody.Outcome{}, safeAuthorityError("read terminal receipt", err)
	}
	if !receiptAuthorityIdentityMatches(config, receipt) {
		return recoverycustody.Outcome{}, fmt.Errorf("%w: persisted receipt authority identity", ErrSchemaMismatch)
	}
	return recoverycustody.ApplyVerifiedReceipt(ctx, pending, receipt, verifier)
}

// scanTerminalReceipt 扫描终态历史行，并校验 submission、resolution、checkpoint 与回执闭环。
func scanTerminalReceipt(row rowScanner) (recoverycustody.ExternalReceipt, error) {
	var scanned terminalReceiptRow
	if err := row.Scan(terminalReceiptScanTargets(&scanned)...); err != nil {
		return recoverycustody.ExternalReceipt{}, err
	}
	if err := decodeTerminalReceipt(&scanned); err != nil {
		return recoverycustody.ExternalReceipt{}, ErrSchemaMismatch
	}
	if err := validateStoredResolutionRow(scanned.Resolution); err != nil {
		return recoverycustody.ExternalReceipt{}, ErrSchemaMismatch
	}
	if !terminalRelationsMatch(scanned) {
		return recoverycustody.ExternalReceipt{}, ErrSchemaMismatch
	}
	return scanned.Receipt, nil
}

// terminalReceiptScanTargets 固定终态回执及其历史关联行的读取顺序。
func terminalReceiptScanTargets(scanned *terminalReceiptRow) []any {
	targets := terminalReceiptScalarTargets(scanned)
	targets = append(targets, &scanned.SubmissionStatus, &scanned.SubmissionReceiptDigest)
	targets = append(targets, resolutionScanTargets(&scanned.Resolution)...)
	return append(targets, &scanned.ResolutionCheckpointGeneration, &scanned.ResolutionCheckpointAnchor,
		&scanned.ResolutionResultHeadRoot, &scanned.ResolutionFinalizationDigest, &scanned.CheckpointGeneration,
		&scanned.CheckpointPredecessorGeneration, &scanned.CheckpointPredecessorAnchorDigest,
		&scanned.CheckpointAnchorDigest, &scanned.CheckpointResultHeadRootDigest,
		&scanned.CheckpointFinalizationDigest)
}

// terminalReceiptScalarTargets 覆盖 ExternalReceipt 的全部标量列与 canonical JSON。
func terminalReceiptScalarTargets(scanned *terminalReceiptRow) []any {
	r := &scanned.Receipt
	return []any{&r.OperationID, &scanned.ResolutionID, &r.SchemaVersion, &r.Kind, &r.ReceiptID, &r.AuthorityID,
		&r.AuthorityClusterID, &r.AuthorityProfile, &r.AuthorityGeneration, &r.OwnerID, &r.OwnerGeneration,
		&r.AuthorityProfileGeneration, &r.AuthorityProfileDigest, &r.SecurityDomainID, &r.HeadID, &r.HeadRevision,
		&r.HeadRootDigest, &r.RequestDigest, &r.BatchDigest, &r.SecurityTimeLineageDigest, &r.SecurityTimeGeneration,
		&r.SecurityTimeRevision, &r.AntiRollbackGeneration, &r.AntiRollbackAnchorDigest,
		&r.QuorumAttestationDigest, &r.QuorumAttestationRef, &r.SignatureDigest, &r.SignatureRef,
		&scanned.AuthorityTime, &r.RetentionPolicyDigest, &scanned.RetentionUntil,
		&r.GCNotBeforeAnchorGeneration, &r.ReceiptDigest, &scanned.ReceiptJSON,
		&scanned.ReceiptCheckpointGeneration}
}

// decodeTerminalReceipt 将时间标量规范化，并要求全部标量精确等于 canonical JSON。
func decodeTerminalReceipt(scanned *terminalReceiptRow) error {
	authorityTime, err := parseCanonicalTime(scanned.AuthorityTime)
	if err != nil {
		return err
	}
	retentionUntil, err := parseCanonicalTime(scanned.RetentionUntil)
	if err != nil {
		return err
	}
	scanned.Receipt.AuthorityTime = authorityTime
	scanned.Receipt.RetentionUntil = retentionUntil
	var encoded recoverycustody.ExternalReceipt
	if err := strictJSON(scanned.ReceiptJSON, &encoded); err != nil {
		return err
	}
	if encoded != scanned.Receipt {
		return ErrSchemaMismatch
	}
	return scanned.Receipt.Validate()
}

// terminalRelationsMatch 校验终态行只绑定自身历史 resolution/checkpoint，不依赖当前 head。
func terminalRelationsMatch(scanned terminalReceiptRow) bool {
	return terminalSubmissionMatches(scanned) && terminalResolutionMatches(scanned) && terminalCheckpointMatches(scanned)
}

// terminalSubmissionMatches 校验 submission 的终态状态与唯一 receipt 摘要。
func terminalSubmissionMatches(scanned terminalReceiptRow) bool {
	wantStatus := recoverycustody.StatusAborted
	if scanned.Receipt.Kind == recoverycustody.ReceiptAvailability {
		wantStatus = recoverycustody.StatusAvailable
	}
	return scanned.SubmissionStatus == wantStatus && scanned.SubmissionReceiptDigest.Valid &&
		scanned.SubmissionReceiptDigest.String == scanned.Receipt.ReceiptDigest
}

// terminalResolutionMatches 校验终态 resolution 的候选、operation 与 receipt 身份。
func terminalResolutionMatches(scanned terminalReceiptRow) bool {
	e := scanned.Resolution.Stored.Expected
	r := scanned.Receipt
	return terminalResolutionIdentityMatches(scanned, e, r) && terminalResolutionAdvanceMatches(e, r)
}

// terminalResolutionIdentityMatches 校验 resolution、handle 与 receipt 的稳定身份字段。
func terminalResolutionIdentityMatches(
	scanned terminalReceiptRow,
	expected ExpectedResolution,
	receipt recoverycustody.ExternalReceipt,
) bool {
	handle := expected.Handle
	return scanned.Resolution.Stored.State == resolutionStateTerminal && scanned.ResolutionID == expected.ResolutionID &&
		expected.Kind == receipt.Kind && handle.OperationID == receipt.OperationID &&
		handle.SecurityDomainID == receipt.SecurityDomainID && handle.AuthorityProfile == receipt.AuthorityProfile &&
		handle.RequestDigest == receipt.RequestDigest && handle.BatchDigest == receipt.BatchDigest
}

// terminalResolutionAdvanceMatches 校验 receipt 相对 expected head 与安全时间严格推进。
func terminalResolutionAdvanceMatches(expected ExpectedResolution, receipt recoverycustody.ExternalReceipt) bool {
	return expected.HeadID == receipt.HeadID && receipt.HeadRevision == expected.ExpectedHeadRevision+1 &&
		expected.SecurityTimeLineageDigest == receipt.SecurityTimeLineageDigest && securityTimeAdvances(expected, receipt)
}

// securityTimeAdvances 校验 receipt 的安全时间严格推进 expected tuple。
func securityTimeAdvances(expected ExpectedResolution, receipt recoverycustody.ExternalReceipt) bool {
	return receipt.SecurityTimeGeneration > expected.ExpectedSecurityTimeGeneration ||
		(receipt.SecurityTimeGeneration == expected.ExpectedSecurityTimeGeneration &&
			receipt.SecurityTimeRevision > expected.ExpectedSecurityTimeRevision)
}

// terminalCheckpointMatches 校验 resolution、checkpoint 标量与 receipt 的锚点闭环。
func terminalCheckpointMatches(scanned terminalReceiptRow) bool {
	e := scanned.Resolution.Stored.Expected
	r := scanned.Receipt
	return terminalCheckpointPresent(scanned) && terminalCheckpointGenerationMatches(scanned, e, r) &&
		terminalCheckpointContentMatches(scanned, e, r)
}

// terminalCheckpointPresent 要求 resolution 已固化全部 checkpoint 终态列。
func terminalCheckpointPresent(scanned terminalReceiptRow) bool {
	return scanned.ResolutionCheckpointGeneration.Valid && scanned.ResolutionCheckpointAnchor.Valid &&
		scanned.ResolutionResultHeadRoot.Valid && scanned.ResolutionFinalizationDigest.Valid
}

// terminalCheckpointGenerationMatches 校验 receipt、resolution 与 checkpoint 的代际链。
func terminalCheckpointGenerationMatches(
	scanned terminalReceiptRow,
	expected ExpectedResolution,
	receipt recoverycustody.ExternalReceipt,
) bool {
	return scanned.ReceiptCheckpointGeneration == scanned.CheckpointGeneration &&
		scanned.CheckpointGeneration == receipt.AntiRollbackGeneration &&
		scanned.CheckpointGeneration == expected.ExpectedAntiRollbackGeneration+1 &&
		scanned.CheckpointPredecessorGeneration == expected.ExpectedAntiRollbackGeneration &&
		scanned.CheckpointPredecessorAnchorDigest == expected.ExpectedAnchorDigest &&
		scanned.ResolutionCheckpointGeneration.Int64 == scanned.CheckpointGeneration
}

// terminalCheckpointContentMatches 校验锚点、结果 root 与 finalization 摘要闭环。
func terminalCheckpointContentMatches(
	scanned terminalReceiptRow,
	expected ExpectedResolution,
	receipt recoverycustody.ExternalReceipt,
) bool {
	return scanned.ResolutionCheckpointAnchor.String == scanned.CheckpointAnchorDigest &&
		scanned.CheckpointAnchorDigest == receipt.AntiRollbackAnchorDigest &&
		scanned.ResolutionResultHeadRoot.String == scanned.CheckpointResultHeadRootDigest &&
		scanned.CheckpointResultHeadRootDigest == receipt.HeadRootDigest &&
		scanned.ResolutionFinalizationDigest.String == scanned.CheckpointFinalizationDigest &&
		finalizationRequestMatches(expected, scanned.CheckpointFinalizationDigest)
}

// finalizationRequestMatches 核对终态 checkpoint 使用完整 expected resolution 派生的最终摘要。
func finalizationRequestMatches(expected ExpectedResolution, digest string) bool {
	request, err := BindFinalizationRequest(expected)
	return err == nil && request.FinalizationRequestDigest == digest
}

// parseCanonicalTime 只接受 canonical UTC RFC3339Nano 字符串。
func parseCanonicalTime(encoded string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339Nano, encoded)
	if err != nil || canonicalTime(value) != encoded {
		return time.Time{}, errors.New("authority time is not canonical UTC RFC3339Nano")
	}
	return value, nil
}

// strictJSON 严格解码单个 JSON 值，并要求其字节内容等于重新编码后的 canonical 表示。
func strictJSON(encoded string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("authority JSON has a trailing value")
	}
	canonical, err := json.Marshal(destination)
	if err != nil || string(canonical) != encoded {
		return errors.New("authority JSON is not exact canonical encoding")
	}
	return nil
}

// canonicalTime 将时间统一为 UTC RFC3339Nano 表示，供持久化身份计算复用。
func canonicalTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
