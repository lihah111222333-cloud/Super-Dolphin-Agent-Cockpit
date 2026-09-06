package managedlaunch

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidMapping 表示 managedlaunch 到 managedruntime 的候选映射缺少显式字段或破坏固定绑定。
var ErrInvalidMapping = errors.New("managed lifecycle mapping is invalid")

// PrepareProjection 是 managedruntime.PrepareRequest 的 transport-neutral 投影。
//
// 它刻意不包含 provider 原文、credential、进程参数或 fence token；调用方必须显式
// 提供这些启动面摘要。该类型留在本包以保持 mcp-orch sidecar 不反向导入 sibling command。
type PrepareProjection struct {
	SchemaVersion              int64  `json:"schema_version"`
	OperationID                string `json:"operation_id"`
	BootstrapOperationID       string `json:"bootstrap_operation_id"`
	BootstrapOperationDigest   string `json:"bootstrap_operation_digest"`
	SourceBindingDigest        string `json:"source_binding_digest"`
	ProviderDigest             string `json:"provider_digest"`
	ExecutableDigest           string `json:"executable_digest"`
	WorkingDirectoryDigest     string `json:"working_directory_digest"`
	ArgumentVectorDigest       string `json:"argument_vector_digest"`
	EnvironmentDigest          string `json:"environment_digest"`
	ExpectedRuntimeWriterEpoch string `json:"expected_runtime_writer_epoch"`
	RequestDigest              string `json:"request_digest"`
}

// CommitProjection 是 managedruntime.CommitRequest 的 transport-neutral 投影。
type CommitProjection struct {
	SchemaVersion                 int64  `json:"schema_version"`
	OperationID                   string `json:"operation_id"`
	PreparedOperationID           string `json:"prepared_operation_id"`
	SourceBindingDigest           string `json:"source_binding_digest"`
	PreparedRequestDigest         string `json:"prepared_request_digest"`
	ExpectedPreparedReceiptRef    string `json:"expected_prepared_receipt_ref"`
	ExpectedPreparedReceiptDigest string `json:"expected_prepared_receipt_digest"`
	ActiveBindingDigest           string `json:"active_binding_digest"`
	CredentialDigest              string `json:"credential_digest"`
	RequestDigest                 string `json:"request_digest"`
}

// AbortProjection 是 managedruntime.AbortRequest 的 transport-neutral 投影。
type AbortProjection struct {
	SchemaVersion                 int64  `json:"schema_version"`
	OperationID                   string `json:"operation_id"`
	PreparedOperationID           string `json:"prepared_operation_id"`
	SourceBindingDigest           string `json:"source_binding_digest"`
	PreparedRequestDigest         string `json:"prepared_request_digest"`
	ExpectedPreparedReceiptRef    string `json:"expected_prepared_receipt_ref"`
	ExpectedPreparedReceiptDigest string `json:"expected_prepared_receipt_digest"`
	CleanupEvidenceRef            string `json:"cleanup_evidence_ref"`
	CleanupEvidenceDigest         string `json:"cleanup_evidence_digest"`
	RequestDigest                 string `json:"request_digest"`
}

// QueryProjection 是 managedruntime.QueryResult 的 transport-neutral 投影。
type QueryProjection struct {
	SchemaVersion         int64  `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	OperationDigest       string `json:"operation_digest"`
	SourceBindingDigest   string `json:"source_binding_digest"`
	State                 string `json:"state"`
	PreparedReceiptRef    string `json:"prepared_receipt_ref"`
	PreparedReceiptDigest string `json:"prepared_receipt_digest"`
	ClosedReceiptRef      string `json:"closed_receipt_ref"`
	ClosedReceiptDigest   string `json:"closed_receipt_digest"`
	UncertaintyReceiptRef string `json:"uncertainty_receipt_ref"`
	UncertaintyDigest     string `json:"uncertainty_digest"`
	ResultDigest          string `json:"result_digest"`
}

// ValidatePrepareProjection 固定 bootstrap→prepare 的单向 operation/digest 映射。
// 启动面摘要和 writer epoch 必须由已验证 owner 显式提供，缺失时直接失败。
func ValidatePrepareProjection(source BootstrapRequest, target PrepareProjection) error {
	if err := source.Validate(); err != nil {
		return fmt.Errorf("%w: source bootstrap: %v", ErrInvalidMapping, err)
	}
	if err := validatePrepareProjectionBinding(source, target); err != nil {
		return err
	}
	return validatePrepareProjectionFields(target)
}

// validatePrepareProjectionBinding 验证 prepare 投影逐字段消费 bootstrap 的 canonical identity。
func validatePrepareProjectionBinding(source BootstrapRequest, target PrepareProjection) error {
	if target.SchemaVersion != SchemaVersion || target.OperationID != source.AttemptOperationID ||
		target.BootstrapOperationID != source.CommandOperationID || target.BootstrapOperationDigest != source.CommandOperationDigest ||
		target.ProviderDigest != source.ProviderDigest || target.WorkingDirectoryDigest != source.CWDDigest ||
		target.SourceBindingDigest != source.BootstrapRequestDigest {
		return fmt.Errorf("%w: prepare operation or digest mapping mismatch", ErrInvalidMapping)
	}
	return nil
}

// validatePrepareProjectionFields 验证 prepare 投影所有启动面摘要和 writer epoch 均显式存在。
func validatePrepareProjectionFields(target PrepareProjection) error {
	for _, field := range []mappingDigest{
		{"source_binding_digest", target.SourceBindingDigest}, {"provider_digest", target.ProviderDigest}, {"executable_digest", target.ExecutableDigest},
		{"working_directory_digest", target.WorkingDirectoryDigest}, {"argument_vector_digest", target.ArgumentVectorDigest},
		{"environment_digest", target.EnvironmentDigest}, {"request_digest", target.RequestDigest},
	} {
		if err := requireMappingDigest(field); err != nil {
			return err
		}
	}
	return requireMappingText("expected_runtime_writer_epoch", target.ExpectedRuntimeWriterEpoch, 128)
}

// ValidateCommitProjection 固定 commit operation、prepared receipt、binding 和 credential digest 映射。
func ValidateCommitProjection(source CommitRequest, prepared BootstrapResult, target CommitProjection) error {
	if err := source.Validate(); err != nil {
		return fmt.Errorf("%w: source commit: %v", ErrInvalidMapping, err)
	}
	if err := prepared.Validate(); err != nil {
		return fmt.Errorf("%w: source prepared result: %v", ErrInvalidMapping, err)
	}
	if source.Operation != prepared.Operation || source.PreparedResultDigest != prepared.ResultDigest {
		return fmt.Errorf("%w: commit prepared receipt binding mismatch", ErrInvalidMapping)
	}
	if err := validateCommitProjectionBinding(source, prepared, target); err != nil {
		return err
	}
	return validateCommitProjectionFields(target)
}

// validateCommitProjectionBinding 校验 commit 投影的身份字段和 prepared request 摘要逐字映射。
func validateCommitProjectionBinding(source CommitRequest, prepared BootstrapResult, target CommitProjection) error {
	if target.SchemaVersion != SchemaVersion || target.OperationID != source.Operation.AttemptOperationID ||
		target.PreparedOperationID != source.Operation.AttemptOperationID ||
		target.SourceBindingDigest != source.CommitOperationDigest ||
		target.PreparedRequestDigest != source.PreparedRequestDigest {
		return fmt.Errorf("%w: commit operation mapping mismatch", ErrInvalidMapping)
	}
	if target.ExpectedPreparedReceiptRef != prepared.ActiveProcessReceiptRef || target.ExpectedPreparedReceiptDigest != prepared.ActiveProcessReceiptDigest {
		return fmt.Errorf("%w: commit prepared receipt mapping mismatch", ErrInvalidMapping)
	}
	if target.ActiveBindingDigest != source.ActiveCurrentBindingDigest || target.CredentialDigest != source.ActivatedCredentialDigest {
		return fmt.Errorf("%w: commit authorization mapping mismatch", ErrInvalidMapping)
	}
	return nil
}

// ValidateAbortProjection 固定 abort operation、prepared receipt、cleanup evidence digest 映射。
func ValidateAbortProjection(source AbortRequest, prepared BootstrapResult, target AbortProjection) error {
	if err := source.Validate(); err != nil {
		return fmt.Errorf("%w: source abort: %v", ErrInvalidMapping, err)
	}
	if err := prepared.Validate(); err != nil {
		return fmt.Errorf("%w: source prepared result: %v", ErrInvalidMapping, err)
	}
	if source.Operation != prepared.Operation || source.PreparedResultDigest != prepared.ResultDigest {
		return fmt.Errorf("%w: abort prepared receipt binding mismatch", ErrInvalidMapping)
	}
	if err := validateAbortProjectionBinding(source, prepared, target); err != nil {
		return err
	}
	return validateAbortProjectionFields(target)
}

// validateAbortProjectionBinding 校验 abort 投影的操作、prepared request、receipt 和 cleanup 绑定。
func validateAbortProjectionBinding(source AbortRequest, prepared BootstrapResult, target AbortProjection) error {
	if target.SchemaVersion != SchemaVersion || target.OperationID != source.Operation.AttemptOperationID ||
		target.PreparedOperationID != source.Operation.AttemptOperationID ||
		target.SourceBindingDigest != source.AbortOperationDigest ||
		target.PreparedRequestDigest != source.PreparedRequestDigest {
		return fmt.Errorf("%w: abort operation mapping mismatch", ErrInvalidMapping)
	}
	if target.ExpectedPreparedReceiptRef != prepared.ActiveProcessReceiptRef || target.ExpectedPreparedReceiptDigest != prepared.ActiveProcessReceiptDigest {
		return fmt.Errorf("%w: abort prepared receipt mapping mismatch", ErrInvalidMapping)
	}
	if target.CleanupEvidenceRef != source.CleanupEvidenceRef || target.CleanupEvidenceDigest != source.CleanupEvidenceDigest {
		return fmt.Errorf("%w: abort cleanup mapping mismatch", ErrInvalidMapping)
	}
	return nil
}

// ValidateQueryProjection 固定 query operation/digest 映射，并拒绝 unknown 到 mutation 的隐式转换。
func ValidateQueryProjection(source QueryOutcome, target QueryProjection) error {
	if err := source.Validate(); err != nil {
		return fmt.Errorf("%w: source query: %v", ErrInvalidMapping, err)
	}
	if target.SchemaVersion != SchemaVersion || target.OperationID != source.Operation.AttemptOperationID ||
		target.SourceBindingDigest != source.Operation.BootstrapRequestDigest {
		return fmt.Errorf("%w: query operation mapping mismatch", ErrInvalidMapping)
	}
	if err := requireMappingDigest(mappingDigest{"result_digest", target.ResultDigest}); err != nil {
		return err
	}
	if err := validateQueryBranch(source, target); err != nil {
		return err
	}
	return validateQueryProjectionFields(target)
}

// validateQueryBranch 按 source status 分派到唯一 query 分支，禁止隐式状态转换。
func validateQueryBranch(source QueryOutcome, target QueryProjection) error {
	switch source.Status {
	case StatusPrepared:
		return validatePreparedQueryBranch(source, target)
	case StatusCommitted, StatusAborted:
		return validateClosedQueryBranch(source.Status, target)
	case StatusOutcomeUnknown:
		return validateUnknownQueryBranch(source, target)
	default:
		return fmt.Errorf("%w: unsupported query status", ErrInvalidMapping)
	}
}

// validatePreparedQueryBranch 验证 prepared receipt ref/digest 原样映射。
func validatePreparedQueryBranch(source QueryOutcome, target QueryProjection) error {
	if target.State != "prepared" || source.Prepared == nil || target.PreparedReceiptRef != source.Prepared.ActiveProcessReceiptRef ||
		target.PreparedReceiptDigest != source.Prepared.ActiveProcessReceiptDigest {
		return fmt.Errorf("%w: prepared query branch mismatch", ErrInvalidMapping)
	}
	return validateQueryBranchPairs(target, true, false, false)
}

// validateClosedQueryBranch 要求 committed/aborted 的两组 receipt 都由调用方显式提供。
func validateClosedQueryBranch(status string, target QueryProjection) error {
	if target.State != strings.ToLower(status) || !completeMappingPair(target.PreparedReceiptRef, target.PreparedReceiptDigest) ||
		!completeMappingPair(target.ClosedReceiptRef, target.ClosedReceiptDigest) {
		return fmt.Errorf("%w: %s query requires explicit prepared and closed receipts", ErrInvalidMapping, status)
	}
	return validateQueryBranchPairs(target, true, true, false)
}

// validateUnknownQueryBranch 保持 outcome_unknown 为 query-only 分支并绑定 evidence。
func validateUnknownQueryBranch(source QueryOutcome, target QueryProjection) error {
	if target.State != "outcome_unknown" || source.OutcomeUnknown == nil ||
		target.UncertaintyReceiptRef != source.OutcomeUnknown.EvidenceRef || target.UncertaintyDigest != source.OutcomeUnknown.EvidenceDigest {
		return fmt.Errorf("%w: unknown query branch must remain explicit", ErrInvalidMapping)
	}
	return validateQueryBranchPairs(target, false, false, true)
}

// validateQueryBranchPairs 拒绝当前 state 之外的 receipt pair，保持 runtime query 的 one-hot presence。
func validateQueryBranchPairs(target QueryProjection, prepared, closed, unknown bool) error {
	pairs := []struct {
		name, ref, digest string
		allowed           bool
	}{
		{"prepared_receipt", target.PreparedReceiptRef, target.PreparedReceiptDigest, prepared},
		{"closed_receipt", target.ClosedReceiptRef, target.ClosedReceiptDigest, closed},
		{"uncertainty", target.UncertaintyReceiptRef, target.UncertaintyDigest, unknown},
	}
	for _, pair := range pairs {
		if !pair.allowed && (pair.ref != "" || pair.digest != "") {
			return fmt.Errorf("%w: %s receipt pair is not allowed for query state", ErrInvalidMapping, pair.name)
		}
	}
	return nil
}

type mappingDigest struct {
	name  string
	value string
}

func requireMappingDigest(field mappingDigest) error {
	if len(field.value) != 64 || strings.ToLower(field.value) != field.value {
		return fmt.Errorf("%w: %s must be an explicit lowercase sha256 digest", ErrInvalidMapping, field.name)
	}
	for _, char := range field.value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return fmt.Errorf("%w: %s must be an explicit lowercase sha256 digest", ErrInvalidMapping, field.name)
		}
	}
	return nil
}

func requireMappingText(name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be explicit bounded text", ErrInvalidMapping, name)
	}
	return nil
}

// validateCommitProjectionFields 验证 commit 投影的 receipt、binding 和 credential 摘要。
func validateCommitProjectionFields(target CommitProjection) error {
	for _, field := range []mappingDigest{{"source_binding_digest", target.SourceBindingDigest}, {"prepared_request_digest", target.PreparedRequestDigest}, {"expected_prepared_receipt_digest", target.ExpectedPreparedReceiptDigest}, {"active_binding_digest", target.ActiveBindingDigest}, {"credential_digest", target.CredentialDigest}, {"request_digest", target.RequestDigest}} {
		if err := requireMappingDigest(field); err != nil {
			return err
		}
	}
	for _, field := range []struct{ name, value string }{{"operation_id", target.OperationID}, {"prepared_operation_id", target.PreparedOperationID}, {"expected_prepared_receipt_ref", target.ExpectedPreparedReceiptRef}} {
		if err := requireMappingText(field.name, field.value, 512); err != nil {
			return err
		}
	}
	return nil
}

// validateAbortProjectionFields 验证 abort 投影的 receipt 与 cleanup evidence。
func validateAbortProjectionFields(target AbortProjection) error {
	for _, field := range []mappingDigest{{"source_binding_digest", target.SourceBindingDigest}, {"prepared_request_digest", target.PreparedRequestDigest}, {"expected_prepared_receipt_digest", target.ExpectedPreparedReceiptDigest}, {"cleanup_evidence_digest", target.CleanupEvidenceDigest}, {"request_digest", target.RequestDigest}} {
		if err := requireMappingDigest(field); err != nil {
			return err
		}
	}
	for _, field := range []struct{ name, value string }{{"operation_id", target.OperationID}, {"prepared_operation_id", target.PreparedOperationID}, {"expected_prepared_receipt_ref", target.ExpectedPreparedReceiptRef}} {
		if err := requireMappingText(field.name, field.value, 512); err != nil {
			return err
		}
	}
	return requireMappingText("cleanup_evidence_ref", target.CleanupEvidenceRef, 512)
}

// validateQueryProjectionFields 验证 query 投影中的引用/摘要成对且均为有界值。
func validateQueryProjectionFields(target QueryProjection) error {
	if err := requireMappingText("operation_id", target.OperationID, 128); err != nil {
		return err
	}
	if err := requireMappingDigest(mappingDigest{"operation_digest", target.OperationDigest}); err != nil {
		return err
	}
	if err := requireMappingDigest(mappingDigest{"source_binding_digest", target.SourceBindingDigest}); err != nil {
		return err
	}
	for _, pair := range []struct{ ref, digest, name string }{{target.PreparedReceiptRef, target.PreparedReceiptDigest, "prepared_receipt"}, {target.ClosedReceiptRef, target.ClosedReceiptDigest, "closed_receipt"}, {target.UncertaintyReceiptRef, target.UncertaintyDigest, "uncertainty"}} {
		if (pair.ref == "") != (pair.digest == "") {
			return fmt.Errorf("%w: %s ref and digest must be supplied together", ErrInvalidMapping, pair.name)
		}
		if pair.ref != "" {
			if err := requireMappingText(pair.name+"_ref", pair.ref, 512); err != nil {
				return err
			}
		}
		if pair.digest != "" {
			if err := requireMappingDigest(mappingDigest{pair.name + "_digest", pair.digest}); err != nil {
				return err
			}
		}
	}
	return nil
}

func completeMappingPair(ref, digest string) bool { return ref != "" && digest != "" }
