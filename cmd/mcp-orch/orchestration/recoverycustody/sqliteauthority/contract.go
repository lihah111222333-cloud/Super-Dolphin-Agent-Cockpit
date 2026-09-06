// Package sqliteauthority 实现独立外部单实例 RecoveryCustody SQLite authority。
package sqliteauthority

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

const (
	// AuthoritySchemaVersion 是稳定的 request/receipt wire revision；私有
	// SQLite schema 演进时仍保持 v1。
	AuthoritySchemaVersion int64 = 1
	// AuthorityDatabaseSchemaRevision 标识私有 authority SQLite 的精确 DDL
	// 与 manifest；revision 3 持久化 external-service mode 与 writer fence。
	AuthorityDatabaseSchemaRevision int64 = 3
	// AuthorityDatabaseSchemaDigestDomain 将 revision-3 DDL 指纹与已退役的
	// revision-1/2 authority schema 分离。
	AuthorityDatabaseSchemaDigestDomain = "orch.recovery-custody.sqlite-authority.schema/v3"
	ResolutionRequestDigestDomain       = "orch.recovery-custody.sqlite-authority.resolution/v1"
	ExpectedResolutionDigestDomain      = "orch.recovery-custody.sqlite-authority.expected-resolution/v1"
	FinalizationRequestDigestDomain     = "orch.recovery-custody.sqlite-authority.finalization/v1"
	StatePathDigestDomain               = "orch.recovery-custody.sqlite-authority.state-path/v1"
	resolutionStateActive               = "active_resolution"
	resolutionStateAnchored             = "anchored"
	resolutionStateTerminal             = "terminal"
)

// FinalizationQueryState 是恢复域外 finalization operation 的封闭查询状态。
type FinalizationQueryState string

const (
	FinalizationQueryAbsent    FinalizationQueryState = "absent"
	FinalizationQueryInflight  FinalizationQueryState = "inflight"
	FinalizationQueryFinalized FinalizationQueryState = "finalized"
)

var (
	ErrInvalidConfig        = errors.New("recovery custody SQLite authority configuration is invalid")
	ErrInvalidResolution    = errors.New("recovery custody SQLite authority resolution is invalid")
	ErrInvalidEvidence      = errors.New("recovery custody SQLite authority evidence is invalid")
	ErrLeaseRejected        = errors.New("recovery custody SQLite authority lease was rejected")
	ErrSchemaMismatch       = errors.New("recovery custody SQLite authority schema does not match")
	ErrAuthorityReadOnly    = errors.New("recovery custody SQLite authority is query-only")
	ErrAuthorityTransaction = errors.New("recovery custody SQLite authority transaction failed")
	ErrCommitOutcomeUnknown = errors.New("recovery custody SQLite authority commit outcome is unknown")
	ErrResolutionNotFound   = recoverycustody.ErrOperationNotFound
)

// Config 冻结独立 authority 文件、owner fence、writer epoch/deployment fence、
// security-time、head 与 retention 身份。
type Config struct {
	SchemaVersion int64
	Path          string
	// ExternalService selects the standalone RecoveryCustody service path
	// isolation contract. In this mode only the private authority path is
	// relevant; product/workspace paths remain required for generic callers.
	ExternalService            bool
	ProductSQLitePath          string
	WorkspaceRoot              string
	StoreID                    string
	AuthorityID                string
	AuthorityClusterID         string
	AuthorityProfile           string
	AuthorityGeneration        int64
	OwnerID                    string
	OwnerGeneration            int64
	OwnerFenceDigest           string
	AuthorityProfileGeneration int64
	AuthorityProfileDigest     string
	SecurityDomainID           string
	SecurityTimeLineageDigest  string
	SecurityTimeGeneration     int64
	SecurityTimeRevision       int64
	RetentionPolicyDigest      string
	HeadID                     string
	InitialHeadRootDigest      string
	AntiRollbackGeneration     int64
	AntiRollbackAnchorDigest   string
	AntiRollbackAttestationRef string
	WriterEpoch                int64
	DeploymentFence            string
}

// WriterFence 返回该配置要求的精确 DB-resident writer token。
func (c Config) WriterFence() WriterFence {
	return WriterFence{SchemaVersion: WriterFenceSchemaVersion, WriterEpoch: c.WriterEpoch, DeploymentFence: c.DeploymentFence}
}

// AuthorityScope 是 single-instance lease 必须逐字段绑定的完整 authority 范围。
type AuthorityScope struct {
	StoreID                    string
	StatePathDigest            string
	AuthorityID                string
	AuthorityClusterID         string
	AuthorityGeneration        int64
	OwnerID                    string
	OwnerGeneration            int64
	OwnerFenceDigest           string
	AuthorityProfile           string
	AuthorityProfileGeneration int64
	AuthorityProfileDigest     string
	SecurityDomainID           string
	RetentionPolicyDigest      string
}

// AuthorityLease 验证并线性化当前进程对完整 authority scope 的唯一写租约。
type AuthorityLease interface {
	Validate(context.Context, AuthorityScope) error
	WithValidatedScope(context.Context, AuthorityScope, func() error) error
}

// ResolutionRequest 描述 availability/abort 单赢家候选及外部 finalization 幂等键。
type ResolutionRequest struct {
	SchemaVersion           int64
	ResolutionID            string
	Handle                  recoverycustody.SubmissionHandle
	Kind                    string
	ResolutionRequestDigest string
	FinalizationOperationID string
}

// ActiveResolution 是启动扫描暴露的已持久化 active resolution 快照。
// Coordinator 只能使用该快照中的不可变句柄调用 ReconcileResolution；它不授予
// 创建新 resolution、推进 head 或生成 terminal receipt 的权限。
type ActiveResolution struct {
	ResolutionID string
	Request      ResolutionRequest
	Expected     ExpectedResolution
}

// ExpectedResolution 将候选绑定到 tx1 观察到的 head 与 anti-rollback 前驱。
type ExpectedResolution struct {
	ResolutionRequest
	HeadID                         string
	ExpectedHeadRevision           int64
	ExpectedHeadRootDigest         string
	ExpectedAntiRollbackGeneration int64
	ExpectedAnchorDigest           string
	SecurityTimeLineageDigest      string
	ExpectedSecurityTimeGeneration int64
	ExpectedSecurityTimeRevision   int64
	ExpectedResolutionDigest       string
}

// FinalizationRequest 把外部幂等 operation 绑定到 tx1 固化的完整 expected resolution。
type FinalizationRequest struct {
	ExpectedResolution
	FinalizationRequestDigest string
}

// FinalizedCheckpoint 是恢复域外 finalizer 返回的单调 anti-rollback checkpoint。
type FinalizedCheckpoint struct {
	HeadID                  string
	Generation              int64
	PredecessorGeneration   int64
	PredecessorAnchorDigest string
	AnchorDigest            string
	ResultHeadRootDigest    string
	AnchorRef               string
	FinalizationDigest      string
	FinalizationRef         string
	AnchoredAt              time.Time
}

// FinalizedEvidence 同时携带可验证 terminal receipt 与其 anti-rollback checkpoint。
type FinalizedEvidence struct {
	Receipt    recoverycustody.ExternalReceipt
	Checkpoint FinalizedCheckpoint
}

// FinalizationObservation 把线性化查询结果绑定到原 finalization 幂等身份和读取 fence。
type FinalizationObservation struct {
	SchemaVersion             int64
	State                     FinalizationQueryState
	FinalizationOperationID   string
	FinalizationRequestDigest string
	ObservationGeneration     int64
	ReadFenceDigest           string
	Evidence                  *FinalizedEvidence
}

// EvidenceFinalizer 在恢复域外查询或原子创建同一 finalization operation。
//
// Query 返回 absent 时必须是线性化的 definitive absent；Finalize 必须对相同 operation ID 与 request digest
// 执行原子 create-or-get，并在相同 operation ID 绑定不同 digest 时返回冲突。
type EvidenceFinalizer interface {
	Finalize(context.Context, FinalizationRequest) (FinalizedEvidence, error)
	Query(context.Context, FinalizationRequest) (FinalizationObservation, error)
}

// Validate 验证全部必填配置，不在这里创建或修改路径。
func (c Config) Validate() error {
	if c.SchemaVersion != AuthoritySchemaVersion {
		return fmt.Errorf("%w: schema version", ErrInvalidConfig)
	}
	if c.ExternalService && (c.ProductSQLitePath != "" || c.WorkspaceRoot != "") {
		return fmt.Errorf("%w: external service cannot carry product/workspace paths", ErrInvalidConfig)
	}
	if err := validateConfigPaths(c); err != nil {
		return err
	}
	if err := validateConfigTexts(c); err != nil {
		return err
	}
	if err := validateConfigDigests(c); err != nil {
		return err
	}
	return validatePositiveConfigValues(c)
}

// validateConfigPaths 验证 authority 路径以及通用调用方的产品库/工作区路径。
func validateConfigPaths(c Config) error {
	paths := []struct{ name, value string }{{"path", c.Path}}
	if !c.ExternalService {
		paths = append(paths,
			struct{ name, value string }{"product_sqlite_path", c.ProductSQLitePath},
			struct{ name, value string }{"workspace_root", c.WorkspaceRoot},
		)
	}
	for _, item := range paths {
		if item.name == "path" && filepath.Base(item.value) == kernelAuthorityLeaseSentinel {
			return fmt.Errorf("%w: authority database path conflicts with kernel lock sentinel", ErrInvalidConfig)
		}
		base := filepath.Base(item.value)
		if !filepath.IsAbs(item.value) || filepath.Clean(item.value) != item.value ||
			strings.TrimSpace(item.value) != item.value || strings.TrimSpace(base) != base {
			return fmt.Errorf("%w: %s must be an absolute canonical path", ErrInvalidConfig, item.name)
		}
	}
	return nil
}

// validateConfigTexts 验证配置中的有界规范文本字段。
func validateConfigTexts(c Config) error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"store_id", c.StoreID, 128}, {"authority_id", c.AuthorityID, 256},
		{"authority_cluster_id", c.AuthorityClusterID, 256}, {"authority_profile", c.AuthorityProfile, 128},
		{"owner_id", c.OwnerID, 256}, {"security_domain_id", c.SecurityDomainID, 256},
		{"head_id", c.HeadID, 256}, {"anti_rollback_attestation_ref", c.AntiRollbackAttestationRef, 512},
		{"deployment_fence", c.DeploymentFence, 512},
	} {
		if err := validateText(ErrInvalidConfig, item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return nil
}

// validateConfigDigests 验证配置中的全部 SHA-256 摘要字段。
func validateConfigDigests(c Config) error {
	for _, item := range []struct{ name, value string }{
		{"owner_fence_digest", c.OwnerFenceDigest}, {"authority_profile_digest", c.AuthorityProfileDigest},
		{"security_time_lineage_digest", c.SecurityTimeLineageDigest}, {"retention_policy_digest", c.RetentionPolicyDigest},
		{"initial_head_root_digest", c.InitialHeadRootDigest}, {"anti_rollback_anchor_digest", c.AntiRollbackAnchorDigest},
	} {
		if err := validateDigest(ErrInvalidConfig, item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

// validatePositiveConfigValues 验证配置中的 generation 与 revision 均严格为正数。
func validatePositiveConfigValues(c Config) error {
	for _, value := range []int64{c.AuthorityGeneration, c.OwnerGeneration, c.AuthorityProfileGeneration,
		c.SecurityTimeGeneration, c.SecurityTimeRevision, c.AntiRollbackGeneration, c.WriterEpoch} {
		if value <= 0 {
			return fmt.Errorf("%w: generation and revision values must be positive", ErrInvalidConfig)
		}
	}
	return nil
}

// Validate 验证 resolution 候选和 domain-separated digest。
func (r ResolutionRequest) Validate() error {
	if r.SchemaVersion != AuthoritySchemaVersion || r.Handle.Validate() != nil {
		return fmt.Errorf("%w: schema or handle", ErrInvalidResolution)
	}
	if r.Kind != recoverycustody.ReceiptAvailability && r.Kind != recoverycustody.ReceiptAbort {
		return fmt.Errorf("%w: kind", ErrInvalidResolution)
	}
	if err := validateResolutionTexts(r); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidResolution, "resolution_request_digest", r.ResolutionRequestDigest); err != nil {
		return err
	}
	return validateResolutionDerivedDigests(r)
}

// validateResolutionTexts 验证 resolution 与 finalization 的幂等身份文本。
func validateResolutionTexts(r ResolutionRequest) error {
	for _, item := range []struct{ name, value string }{
		{"resolution_id", r.ResolutionID}, {"finalization_operation_id", r.FinalizationOperationID},
	} {
		if err := validateText(ErrInvalidResolution, item.name, item.value, 128); err != nil {
			return err
		}
	}
	return nil
}

// validateResolutionDerivedDigests 重新计算并核对 resolution request 摘要。
func validateResolutionDerivedDigests(r ResolutionRequest) error {
	wantResolution, err := ComputeResolutionRequestDigest(r)
	if err != nil || wantResolution != r.ResolutionRequestDigest {
		return fmt.Errorf("%w: resolution request digest mismatch", ErrInvalidResolution)
	}
	return nil
}

// FinalizeResolutionRequestDigest 计算并写回不可自引用的 resolution request digest。
func FinalizeResolutionRequestDigest(request ResolutionRequest) (ResolutionRequest, error) {
	request.ResolutionRequestDigest = ""
	digest, err := ComputeResolutionRequestDigest(request)
	if err != nil {
		return ResolutionRequest{}, err
	}
	request.ResolutionRequestDigest = digest
	return request, nil
}

// ComputeResolutionRequestDigest 覆盖 ResolutionRequest 除摘要自身外的全部字段。
func ComputeResolutionRequestDigest(request ResolutionRequest) (string, error) {
	request.ResolutionRequestDigest = ""
	return digestValue(ResolutionRequestDigestDomain, request)
}

// BindExpectedResolutionDigest 计算并写回 tx1 完整 expected resolution 的不可自引用摘要。
func BindExpectedResolutionDigest(expected ExpectedResolution) (ExpectedResolution, error) {
	expected.ExpectedResolutionDigest = ""
	digest, err := ComputeExpectedResolutionDigest(expected)
	if err != nil {
		return ExpectedResolution{}, err
	}
	expected.ExpectedResolutionDigest = digest
	return expected, nil
}

// ComputeExpectedResolutionDigest 覆盖 ExpectedResolution 除摘要自身外的全部字段。
func ComputeExpectedResolutionDigest(expected ExpectedResolution) (string, error) {
	expected.ExpectedResolutionDigest = ""
	return digestValue(ExpectedResolutionDigestDomain, expected)
}

// BindFinalizationRequest 计算并写回 operation 与 expected digest 的最终外部幂等摘要。
func BindFinalizationRequest(expected ExpectedResolution) (FinalizationRequest, error) {
	if err := expected.Validate(); err != nil {
		return FinalizationRequest{}, err
	}
	request := FinalizationRequest{ExpectedResolution: expected}
	digest, err := ComputeFinalizationRequestDigest(expected.FinalizationOperationID, expected.ExpectedResolutionDigest)
	if err != nil {
		return FinalizationRequest{}, err
	}
	request.FinalizationRequestDigest = digest
	return request, nil
}

// ComputeFinalizationRequestDigest 把外部幂等 operation 绑定到已冻结 expected resolution。
func ComputeFinalizationRequestDigest(operationID, expectedResolutionDigest string) (string, error) {
	if err := validateText(ErrInvalidResolution, "finalization_operation_id", operationID, 128); err != nil {
		return "", err
	}
	if err := validateDigest(ErrInvalidResolution, "expected_resolution_digest", expectedResolutionDigest); err != nil {
		return "", err
	}
	return digestValue(FinalizationRequestDigestDomain, struct {
		FinalizationOperationID  string `json:"finalization_operation_id"`
		ExpectedResolutionDigest string `json:"expected_resolution_digest"`
	}{FinalizationOperationID: operationID, ExpectedResolutionDigest: expectedResolutionDigest})
}

// Validate 验证 tx1 产生的 expected head 与 anti-rollback 前驱。
func (r ExpectedResolution) Validate() error {
	if err := r.ResolutionRequest.Validate(); err != nil {
		return err
	}
	if err := validateExpectedResolutionValues(r); err != nil {
		return err
	}
	return validateExpectedResolutionDigest(r)
}

// validateExpectedResolutionValues 验证 tx1 head、anti-rollback 与 security-time 标量。
func validateExpectedResolutionValues(r ExpectedResolution) error {
	if err := validateText(ErrInvalidResolution, "head_id", r.HeadID, 256); err != nil {
		return err
	}
	if r.ExpectedHeadRevision < 0 || r.ExpectedAntiRollbackGeneration <= 0 ||
		r.ExpectedSecurityTimeGeneration <= 0 || r.ExpectedSecurityTimeRevision <= 0 {
		return fmt.Errorf("%w: expected revision or generation", ErrInvalidResolution)
	}
	if err := validateDigest(ErrInvalidResolution, "expected_head_root_digest", r.ExpectedHeadRootDigest); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidResolution, "expected_anchor_digest", r.ExpectedAnchorDigest); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidResolution, "security_time_lineage_digest", r.SecurityTimeLineageDigest); err != nil {
		return err
	}
	return nil
}

// validateExpectedResolutionDigest 重新计算并核对完整 expected resolution 摘要。
func validateExpectedResolutionDigest(r ExpectedResolution) error {
	if err := validateDigest(ErrInvalidResolution, "expected_resolution_digest", r.ExpectedResolutionDigest); err != nil {
		return err
	}
	want, err := ComputeExpectedResolutionDigest(r)
	if err != nil || want != r.ExpectedResolutionDigest {
		return fmt.Errorf("%w: expected resolution digest mismatch", ErrInvalidResolution)
	}
	return nil
}

// Validate 验证 finalization request 精确绑定完整 expected resolution。
func (r FinalizationRequest) Validate() error {
	if err := r.ExpectedResolution.Validate(); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidResolution, "finalization_request_digest", r.FinalizationRequestDigest); err != nil {
		return err
	}
	want, err := ComputeFinalizationRequestDigest(r.FinalizationOperationID, r.ExpectedResolutionDigest)
	if err != nil || want != r.FinalizationRequestDigest {
		return fmt.Errorf("%w: finalization request digest mismatch", ErrInvalidResolution)
	}
	return nil
}

// Validate 验证 checkpoint 严格推进一代并绑定 canonical UTC evidence。
func (c FinalizedCheckpoint) Validate(expected ExpectedResolution) error {
	if !checkpointPredecessorMatches(c, expected) {
		return fmt.Errorf("%w: checkpoint predecessor", ErrInvalidEvidence)
	}
	if err := validateCheckpointTexts(c); err != nil {
		return err
	}
	if err := validateCheckpointDigests(c); err != nil {
		return err
	}
	if c.AnchoredAt.IsZero() || c.AnchoredAt.Location() != time.UTC {
		return fmt.Errorf("%w: anchored_at must be canonical UTC", ErrInvalidEvidence)
	}
	if c.ResultHeadRootDigest == expected.ExpectedHeadRootDigest {
		return fmt.Errorf("%w: checkpoint did not advance head root", ErrInvalidEvidence)
	}
	return nil
}

// checkpointPredecessorMatches 核对 checkpoint 只从 expected 前驱严格推进一代。
func checkpointPredecessorMatches(c FinalizedCheckpoint, expected ExpectedResolution) bool {
	return c.HeadID == expected.HeadID && c.PredecessorGeneration == expected.ExpectedAntiRollbackGeneration &&
		c.Generation == c.PredecessorGeneration+1 && c.PredecessorAnchorDigest == expected.ExpectedAnchorDigest
}

// validateCheckpointTexts 验证 checkpoint 的有界规范文本字段。
func validateCheckpointTexts(c FinalizedCheckpoint) error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"head_id", c.HeadID, 256}, {"anchor_ref", c.AnchorRef, 512}, {"finalization_ref", c.FinalizationRef, 512},
	} {
		if err := validateText(ErrInvalidEvidence, item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return nil
}

// validateCheckpointDigests 验证 checkpoint 的全部 SHA-256 摘要字段。
func validateCheckpointDigests(c FinalizedCheckpoint) error {
	for _, item := range []struct{ name, value string }{
		{"predecessor_anchor_digest", c.PredecessorAnchorDigest}, {"anchor_digest", c.AnchorDigest},
		{"result_head_root_digest", c.ResultHeadRootDigest}, {"finalization_digest", c.FinalizationDigest},
	} {
		if err := validateDigest(ErrInvalidEvidence, item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

// Validate 验证 evidence、receipt 与 expected resolution/head/checkpoint 的闭环绑定。
func (e FinalizedEvidence) Validate(expected ExpectedResolution) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if err := e.Checkpoint.Validate(expected); err != nil {
		return err
	}
	if err := e.Receipt.Validate(); err != nil {
		return fmt.Errorf("%w: receipt", ErrInvalidEvidence)
	}
	if !receiptStringsMatchExpected(e, expected) || !receiptNumbersMatchExpected(e, expected) {
		return fmt.Errorf("%w: receipt does not advance expected resolution", ErrInvalidEvidence)
	}
	return nil
}

// Validate 严格验证查询 observation 与原 finalization request 的身份、fence 和 evidence 状态互斥。
func (o FinalizationObservation) Validate(finalization FinalizationRequest) error {
	if err := finalization.Validate(); err != nil {
		return err
	}
	if err := o.validateEnvelope(finalization); err != nil {
		return err
	}
	return o.validateStateEvidence(finalization)
}

// validateEnvelope 验证 observation 的版本、原请求身份、generation 与读取 fence。
func (o FinalizationObservation) validateEnvelope(finalization FinalizationRequest) error {
	if o.SchemaVersion != AuthoritySchemaVersion || o.ObservationGeneration <= 0 {
		return fmt.Errorf("%w: observation schema or generation", ErrInvalidEvidence)
	}
	if o.FinalizationOperationID != finalization.FinalizationOperationID ||
		o.FinalizationRequestDigest != finalization.FinalizationRequestDigest {
		return fmt.Errorf("%w: observation finalization identity", ErrInvalidEvidence)
	}
	if err := validateDigest(ErrInvalidEvidence, "read_fence_digest", o.ReadFenceDigest); err != nil {
		return err
	}
	return nil
}

// validateStateEvidence 验证封闭状态与 evidence 指针严格互斥，并核对终态 evidence。
func (o FinalizationObservation) validateStateEvidence(finalization FinalizationRequest) error {
	switch o.State {
	case FinalizationQueryAbsent, FinalizationQueryInflight:
		if o.Evidence != nil {
			return fmt.Errorf("%w: non-finalized observation carries evidence", ErrInvalidEvidence)
		}
	case FinalizationQueryFinalized:
		if o.Evidence == nil {
			return fmt.Errorf("%w: finalized observation lacks evidence", ErrInvalidEvidence)
		}
		if err := o.Evidence.Validate(finalization.ExpectedResolution); err != nil {
			return err
		}
		if o.Evidence.Checkpoint.FinalizationDigest != finalization.FinalizationRequestDigest {
			return fmt.Errorf("%w: observation evidence finalization digest", ErrInvalidEvidence)
		}
	default:
		return fmt.Errorf("%w: observation state", ErrInvalidEvidence)
	}
	return nil
}

// receiptStringsMatchExpected 核对 receipt 的文本字段与 expected resolution 和 checkpoint 绑定。
func receiptStringsMatchExpected(e FinalizedEvidence, expected ExpectedResolution) bool {
	r := e.Receipt
	pairs := [][2]string{{r.Kind, expected.Kind}, {r.OperationID, expected.Handle.OperationID},
		{r.SecurityDomainID, expected.Handle.SecurityDomainID}, {r.AuthorityProfile, expected.Handle.AuthorityProfile},
		{r.RequestDigest, expected.Handle.RequestDigest}, {r.BatchDigest, expected.Handle.BatchDigest},
		{r.HeadID, expected.HeadID}, {r.HeadRootDigest, e.Checkpoint.ResultHeadRootDigest},
		{r.SecurityTimeLineageDigest, expected.SecurityTimeLineageDigest},
		{r.AntiRollbackAnchorDigest, e.Checkpoint.AnchorDigest}}
	for _, pair := range pairs {
		if pair[0] != pair[1] {
			return false
		}
	}
	return true
}

// receiptNumbersMatchExpected 核对 receipt 的 head、anti-rollback 与 security-time 单调推进。
func receiptNumbersMatchExpected(e FinalizedEvidence, expected ExpectedResolution) bool {
	r := e.Receipt
	if r.HeadRevision != expected.ExpectedHeadRevision+1 || r.AntiRollbackGeneration != e.Checkpoint.Generation {
		return false
	}
	return r.SecurityTimeGeneration > expected.ExpectedSecurityTimeGeneration ||
		(r.SecurityTimeGeneration == expected.ExpectedSecurityTimeGeneration &&
			r.SecurityTimeRevision > expected.ExpectedSecurityTimeRevision)
}

// validateText 校验持久合同文本的UTF-8、空白、NUL和长度边界。
func validateText(base error, name, value string, limit int) error {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", base, name)
	}
	return nil
}

// validateDigest 验证摘要为固定长度的小写十六进制 SHA-256。
func validateDigest(base error, name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be a lowercase SHA-256 digest", base, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s must be hexadecimal", base, name)
	}
	return nil
}

// digestValue 对带显式 domain 的 JSON payload 计算 SHA-256 摘要。
func digestValue(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", fmt.Errorf("encode SQLite authority digest payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// dependencyIsNil 识别 nil 接口及其包裹的可空依赖值。
func dependencyIsNil(value any) bool {
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
