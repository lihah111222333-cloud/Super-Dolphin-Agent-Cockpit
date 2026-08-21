package heterociowner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const (
	// PostgreSQLOwnerProtocolSchemaVersion 冻结 owner 与 batch receipt 的远程 PostgreSQL-only 证据协议。
	PostgreSQLOwnerProtocolSchemaVersion = "heteroci-postgresql-local-authorities-protocol/v1"
	// PostgreSQLOwnerPlacementRemotePrimaryOnly 禁止本机库、standby read 与本地副本查询参与调度。
	PostgreSQLOwnerPlacementRemotePrimaryOnly = "remote_postgresql_primary_only"

	PostgreSQLOwnerSchema   = "heteroci_owner"
	PostgreSQLOwnerRole     = "heteroci_owner_writer"
	PostgreSQLReceiptSchema = "heteroci_receipt"
	PostgreSQLReceiptRole   = "heteroci_receipt_writer"

	PostgreSQLTransactionAuthorityIndependent = "independent"

	PostgreSQLOwnerFailoverManualFencedPromotion        = "manual_fenced_promotion"
	PostgreSQLOwnerFailoverQuorumWitnessFencedPromotion = "quorum_witness_fenced_promotion"
	PostgreSQLOwnerOldPrimaryFencingRequired            = "required_before_promotion"
	PostgreSQLOwnerPolicyForbidden                      = "forbidden"
	PostgreSQLOwnerReadPrimaryOnly                      = "primary_only"
	PostgreSQLOwnerSynchronousCommitRemoteApply         = "remote_apply"
	PostgreSQLOwnerIsolationSerializable                = "serializable"
	PostgreSQLOwnerScopeTransactionAdvisoryLock         = "scope_transaction_advisory_lock_before_authority_reads"
	PostgreSQLOwnerAmbiguousCommitExactReplay           = "remote_primary_same_operation_id_exact_replay"
	PostgreSQLReceiptAmbiguousCommitExactReplay         = "remote_primary_same_batch_material_command_exact_replay"
	PostgreSQLOwnerPromotionAcquireNewEpoch             = "acquire_new_owner_epoch_before_mutation"
	PostgreSQLNetworkFailureFailFast                    = "fail_fast"
	PostgreSQLOwnerRuntimeNotWired                      = "not_wired"

	maxPostgreSQLOwnerProtocolBytes = 16 * 1024
)

// ErrPostgreSQLOwnerProtocol 表示 PostgreSQL-only 本地非权威持久化协议无效。
var ErrPostgreSQLOwnerProtocol = errors.New("heteroci PostgreSQL local authority protocol is invalid")

// PostgreSQLAuthorityBinding 为 owner 或 receipt 冻结独立 schema、role 与事务 authority。
type PostgreSQLAuthorityBinding struct {
	Schema               string `json:"schema"`
	Role                 string `json:"role"`
	TransactionAuthority string `json:"transaction_authority"`
}

// PostgreSQLOwnerFailoverConfig 冻结两数据库节点的隔离与见证协议。
type PostgreSQLOwnerFailoverConfig struct {
	Mode                     string `json:"mode"`
	DatabaseNodes            int    `json:"database_nodes"`
	WitnessVoters            int    `json:"witness_voters"`
	OldPrimaryFencing        string `json:"old_primary_fencing"`
	AutomaticTwoNodeFailover string `json:"automatic_two_node_failover"`
}

// PostgreSQLOwnerTransactionConfig 冻结 primary-only、同步提交与事务串行化协议。
type PostgreSQLOwnerTransactionConfig struct {
	ReadPolicy                string `json:"read_policy"`
	SynchronousCommit         string `json:"synchronous_commit"`
	AsyncDowngrade            string `json:"async_downgrade"`
	IsolationLevel            string `json:"isolation_level"`
	ScopeSerialization        string `json:"scope_serialization"`
	CrossAuthorityTransaction string `json:"cross_authority_transaction"`
}

// PostgreSQLOwnerRecoveryConfig 冻结不确定提交与 promotion 后的新 epoch 语义。
type PostgreSQLOwnerRecoveryConfig struct {
	AmbiguousCommit        string `json:"owner_ambiguous_commit"`
	ReceiptAmbiguousCommit string `json:"receipt_ambiguous_commit"`
	PromotionEpoch         string `json:"promotion_epoch"`
}

// PostgreSQLOwnerBoundaryConfig 冻结未接线阶段禁止的本机库、fallback 与 remote ledger 路径。
type PostgreSQLOwnerBoundaryConfig struct {
	OwnerSQLite          string `json:"owner_sqlite"`
	ReceiptSQLite        string `json:"receipt_sqlite"`
	LocalReplicaRead     string `json:"local_replica_read"`
	LocalReplicaMutation string `json:"local_replica_mutation"`
	SQLiteFallback       string `json:"sqlite_fallback"`
	DualWrite            string `json:"dual_write"`
	NetworkFailure       string `json:"network_failure"`
	RemoteDurationLedger string `json:"remote_duration_ledger"`
	RuntimeWiring        string `json:"runtime_wiring"`
}

// PostgreSQLOwnerProtocolConfig 是 owner 与 receipt 的 strict、未接线协议证据。
// 它不含 endpoint、credential、本机路径、driver、DDL 或 runtime connector。
type PostgreSQLOwnerProtocolConfig struct {
	SchemaVersion string                           `json:"schema_version"`
	Authority     string                           `json:"authority"`
	Status        string                           `json:"status"`
	Placement     string                           `json:"placement"`
	Owner         PostgreSQLAuthorityBinding       `json:"owner"`
	Receipt       PostgreSQLAuthorityBinding       `json:"receipt"`
	Failover      PostgreSQLOwnerFailoverConfig    `json:"failover"`
	Transaction   PostgreSQLOwnerTransactionConfig `json:"transaction"`
	Recovery      PostgreSQLOwnerRecoveryConfig    `json:"recovery"`
	Boundary      PostgreSQLOwnerBoundaryConfig    `json:"boundary"`
}

// DecodePostgreSQLOwnerProtocolConfig 严格解码 canonical PostgreSQL-only 协议证据。
func DecodePostgreSQLOwnerProtocolConfig(data []byte) (PostgreSQLOwnerProtocolConfig, error) {
	if len(data) == 0 || len(data) > maxPostgreSQLOwnerProtocolBytes {
		return PostgreSQLOwnerProtocolConfig{}, fmt.Errorf("%w: document size is invalid", ErrPostgreSQLOwnerProtocol)
	}
	if err := rejectPostgreSQLProtocolDuplicateFields(data); err != nil {
		return PostgreSQLOwnerProtocolConfig{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config PostgreSQLOwnerProtocolConfig
	if err := decoder.Decode(&config); err != nil {
		return PostgreSQLOwnerProtocolConfig{}, fmt.Errorf("%w: decode: %w", ErrPostgreSQLOwnerProtocol, err)
	}
	if err := ensurePostgreSQLProtocolEOF(decoder); err != nil {
		return PostgreSQLOwnerProtocolConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return PostgreSQLOwnerProtocolConfig{}, err
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return PostgreSQLOwnerProtocolConfig{}, fmt.Errorf("%w: encode canonical form: %w", ErrPostgreSQLOwnerProtocol, err)
	}
	if !bytes.Equal(canonical, data) {
		return PostgreSQLOwnerProtocolConfig{}, fmt.Errorf("%w: document is not canonical", ErrPostgreSQLOwnerProtocol)
	}
	return config, nil
}

// Validate 校验 PostgreSQL-only owner 与 receipt 协议的完整不变量。
func (config PostgreSQLOwnerProtocolConfig) Validate() error {
	if err := validatePostgreSQLProtocolHeader(config); err != nil {
		return err
	}
	if err := validatePostgreSQLAuthorityBindings(config.Owner, config.Receipt); err != nil {
		return err
	}
	if err := validatePostgreSQLFailover(config.Failover); err != nil {
		return err
	}
	if err := validatePostgreSQLTransaction(config.Transaction); err != nil {
		return err
	}
	if err := validatePostgreSQLRecovery(config.Recovery); err != nil {
		return err
	}
	return validatePostgreSQLBoundary(config.Boundary)
}

// validatePostgreSQLProtocolHeader 校验协议身份、非权威状态与唯一远程 primary placement。
func validatePostgreSQLProtocolHeader(config PostgreSQLOwnerProtocolConfig) error {
	for _, requirement := range []struct {
		field string
		got   string
		want  string
	}{
		{field: "schema_version", got: config.SchemaVersion, want: PostgreSQLOwnerProtocolSchemaVersion},
		{field: "authority", got: config.Authority, want: heteroci.AuthorityLocalNonAuthoritative},
		{field: "status", got: config.Status, want: heteroci.ResultStatusNotVerified},
		{field: "placement", got: config.Placement, want: PostgreSQLOwnerPlacementRemotePrimaryOnly},
	} {
		if requirement.got != requirement.want {
			return postgreSQLProtocolFieldError(requirement.field)
		}
	}
	return nil
}

// validatePostgreSQLAuthorityBindings 拒绝 owner 与 receipt 共用 schema、role 或事务 authority。
func validatePostgreSQLAuthorityBindings(owner, receipt PostgreSQLAuthorityBinding) error {
	if owner.Schema != PostgreSQLOwnerSchema || owner.Role != PostgreSQLOwnerRole || owner.TransactionAuthority != PostgreSQLTransactionAuthorityIndependent {
		return postgreSQLProtocolFieldError("owner")
	}
	if receipt.Schema != PostgreSQLReceiptSchema || receipt.Role != PostgreSQLReceiptRole || receipt.TransactionAuthority != PostgreSQLTransactionAuthorityIndependent {
		return postgreSQLProtocolFieldError("receipt")
	}
	if owner.Schema == receipt.Schema || owner.Role == receipt.Role {
		return postgreSQLProtocolFieldError("authority separation")
	}
	return nil
}

// validatePostgreSQLFailover 拒绝无 fencing 的 promotion 与两节点自动选主。
func validatePostgreSQLFailover(config PostgreSQLOwnerFailoverConfig) error {
	if config.DatabaseNodes != 2 || config.OldPrimaryFencing != PostgreSQLOwnerOldPrimaryFencingRequired || config.AutomaticTwoNodeFailover != PostgreSQLOwnerPolicyForbidden {
		return postgreSQLProtocolFieldError("failover")
	}
	switch config.Mode {
	case PostgreSQLOwnerFailoverManualFencedPromotion:
		if config.WitnessVoters != 0 {
			return postgreSQLProtocolFieldError("failover.witness_voters")
		}
	case PostgreSQLOwnerFailoverQuorumWitnessFencedPromotion:
		if config.WitnessVoters != 1 {
			return postgreSQLProtocolFieldError("failover.witness_voters")
		}
	default:
		return postgreSQLProtocolFieldError("failover.mode")
	}
	return nil
}

// validatePostgreSQLTransaction 校验 primary-only、remote_apply 与 scope 事务锁。
func validatePostgreSQLTransaction(config PostgreSQLOwnerTransactionConfig) error {
	for _, requirement := range []struct {
		field string
		got   string
		want  string
	}{
		{field: "transaction.read_policy", got: config.ReadPolicy, want: PostgreSQLOwnerReadPrimaryOnly},
		{field: "transaction.synchronous_commit", got: config.SynchronousCommit, want: PostgreSQLOwnerSynchronousCommitRemoteApply},
		{field: "transaction.async_downgrade", got: config.AsyncDowngrade, want: PostgreSQLOwnerPolicyForbidden},
		{field: "transaction.isolation_level", got: config.IsolationLevel, want: PostgreSQLOwnerIsolationSerializable},
		{field: "transaction.scope_serialization", got: config.ScopeSerialization, want: PostgreSQLOwnerScopeTransactionAdvisoryLock},
		{field: "transaction.cross_authority_transaction", got: config.CrossAuthorityTransaction, want: PostgreSQLOwnerPolicyForbidden},
	} {
		if requirement.got != requirement.want {
			return postgreSQLProtocolFieldError(requirement.field)
		}
	}
	return nil
}

// validatePostgreSQLRecovery 校验不确定提交只回远程 primary 精确查账。
func validatePostgreSQLRecovery(config PostgreSQLOwnerRecoveryConfig) error {
	if config.AmbiguousCommit != PostgreSQLOwnerAmbiguousCommitExactReplay {
		return postgreSQLProtocolFieldError("recovery.owner_ambiguous_commit")
	}
	if config.ReceiptAmbiguousCommit != PostgreSQLReceiptAmbiguousCommitExactReplay {
		return postgreSQLProtocolFieldError("recovery.receipt_ambiguous_commit")
	}
	if config.PromotionEpoch != PostgreSQLOwnerPromotionAcquireNewEpoch {
		return postgreSQLProtocolFieldError("recovery.promotion_epoch")
	}
	return nil
}

// validatePostgreSQLBoundary 拒绝本机库、本地副本、fallback、双写与 remote ledger 接入。
func validatePostgreSQLBoundary(config PostgreSQLOwnerBoundaryConfig) error {
	for _, requirement := range []struct {
		field string
		got   string
		want  string
	}{
		{field: "boundary.owner_sqlite", got: config.OwnerSQLite, want: PostgreSQLOwnerPolicyForbidden},
		{field: "boundary.receipt_sqlite", got: config.ReceiptSQLite, want: PostgreSQLOwnerPolicyForbidden},
		{field: "boundary.local_replica_read", got: config.LocalReplicaRead, want: PostgreSQLOwnerPolicyForbidden},
		{field: "boundary.local_replica_mutation", got: config.LocalReplicaMutation, want: PostgreSQLOwnerPolicyForbidden},
		{field: "boundary.sqlite_fallback", got: config.SQLiteFallback, want: PostgreSQLOwnerPolicyForbidden},
		{field: "boundary.dual_write", got: config.DualWrite, want: PostgreSQLOwnerPolicyForbidden},
		{field: "boundary.network_failure", got: config.NetworkFailure, want: PostgreSQLNetworkFailureFailFast},
		{field: "boundary.remote_duration_ledger", got: config.RemoteDurationLedger, want: PostgreSQLOwnerPolicyForbidden},
		{field: "boundary.runtime_wiring", got: config.RuntimeWiring, want: PostgreSQLOwnerRuntimeNotWired},
	} {
		if requirement.got != requirement.want {
			return postgreSQLProtocolFieldError(requirement.field)
		}
	}
	return nil
}

// postgreSQLProtocolFieldError 返回不暴露配置内容的稳定字段错误。
func postgreSQLProtocolFieldError(field string) error {
	return fmt.Errorf("%w: field %s is invalid", ErrPostgreSQLOwnerProtocol, field)
}

// ensurePostgreSQLProtocolEOF 拒绝 canonical 协议后的任意尾随 JSON。
func ensurePostgreSQLProtocolEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON", ErrPostgreSQLOwnerProtocol)
	}
	return nil
}

// rejectPostgreSQLProtocolDuplicateFields 在结构解码前拒绝任意层级的重复字段。
func rejectPostgreSQLProtocolDuplicateFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkPostgreSQLProtocolJSON(decoder); err != nil {
		return err
	}
	return ensurePostgreSQLProtocolEOF(decoder)
}

// walkPostgreSQLProtocolJSON 递归读取一个 JSON 值并分派容器校验。
func walkPostgreSQLProtocolJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON", ErrPostgreSQLOwnerProtocol)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return walkPostgreSQLProtocolObject(decoder)
	case '[':
		return walkPostgreSQLProtocolArray(decoder)
	default:
		return fmt.Errorf("%w: JSON delimiter is invalid", ErrPostgreSQLOwnerProtocol)
	}
}

// walkPostgreSQLProtocolObject 校验对象键唯一并递归读取全部字段值。
func walkPostgreSQLProtocolObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%w: malformed object", ErrPostgreSQLOwnerProtocol)
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("%w: object key is invalid", ErrPostgreSQLOwnerProtocol)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate field", ErrPostgreSQLOwnerProtocol)
		}
		seen[key] = struct{}{}
		if err := walkPostgreSQLProtocolJSON(decoder); err != nil {
			return err
		}
	}
	return closePostgreSQLProtocolContainer(decoder)
}

// walkPostgreSQLProtocolArray 递归读取数组全部值。
func walkPostgreSQLProtocolArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := walkPostgreSQLProtocolJSON(decoder); err != nil {
			return err
		}
	}
	return closePostgreSQLProtocolContainer(decoder)
}

// closePostgreSQLProtocolContainer 消费并校验当前容器的结束 token。
func closePostgreSQLProtocolContainer(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("%w: unterminated JSON", ErrPostgreSQLOwnerProtocol)
	}
	return nil
}
