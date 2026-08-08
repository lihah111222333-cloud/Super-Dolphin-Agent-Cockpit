package gate

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

var ErrRemoteBaselineGenerationOneAlreadyInitialized = errors.New("remote baseline generation one is already initialized")

// InitializeRemoteBaselineGenerationOne 消费外部 ECI 回执并执行唯一首代 accepted INSERT。
func (store *DurationLedgerStore) InitializeRemoteBaselineGenerationOne(receiptJSON []byte) (RemoteBaselineStateRecord, error) {
	if store == nil {
		return RemoteBaselineStateRecord{}, errors.New("duration ledger store is nil")
	}
	receipt, err := cicontract.DecodeGenerationOneProvisionReceipt(receiptJSON)
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	if err := validateGenerationOneStateProjection(receipt); err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	database, err := openGenerationOneDatabase(store)
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	defer database.Close()
	return insertGenerationOneState(store, database, receipt)
}

// openGenerationOneDatabase 打开已经完成 schema migration 的 SQLite authority。
func openGenerationOneDatabase(store *DurationLedgerStore) (*sql.DB, error) {
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return nil, err
	}
	return database, nil
}

// insertGenerationOneState 在同一 SQLite 写事务中提交首代 singleton。
func insertGenerationOneState(store *DurationLedgerStore, database *sql.DB, receipt cicontract.GenerationOneProvisionReceipt) (RemoteBaselineStateRecord, error) {
	var record RemoteBaselineStateRecord
	err := withSQLiteWriteTransaction(database, "initialize remote baseline generation one", func(transaction *sql.Tx) error {
		return insertGenerationOneStateTransaction(store, transaction, receipt, &record)
	})
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	return record, nil
}

// insertGenerationOneStateTransaction 检查空表前置条件并执行唯一 INSERT。
func insertGenerationOneStateTransaction(store *DurationLedgerStore, transaction *sql.Tx, receipt cicontract.GenerationOneProvisionReceipt, record *RemoteBaselineStateRecord) error {
	if err := ensureGenerationOneStateTableEmpty(transaction); err != nil {
		return err
	}
	if err := ensureGenerationOneDurationLedgerMetadata(transaction, receipt.Generation); err != nil {
		return err
	}
	stateSHA256 := receipt.StateSHA256
	_, err := transaction.Exec(`INSERT INTO ci_remote_baseline_state(singleton,schema_version,generation,state_json,state_sha256,updated_at_unix_ms) VALUES(1,3,?,?,?,?)`,
		strconv.FormatUint(receipt.Generation, 10), string(receipt.StateJSON), stateSHA256, store.nowFunc().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("insert remote baseline generation one: %w", err)
	}
	*record = RemoteBaselineStateRecord{Generation: receipt.Generation, StateJSON: append([]byte(nil), receipt.StateJSON...), StateSHA256: stateSHA256}
	return nil
}

// ensureGenerationOneDurationLedgerMetadata 在首代 accepted 同一事务中补齐空账本元数据，并拒绝冲突 authority。
func ensureGenerationOneDurationLedgerMetadata(transaction *sql.Tx, generation uint64) error {
	currentGeneration, found, err := sqliteCurrentGeneration(transaction)
	if err != nil {
		return err
	}
	if found {
		if currentGeneration != generation {
			return errors.New("generation-one duration ledger metadata generation conflicts with the receipt")
		}
		var schemaVersion, ledgerVersion int
		if err := transaction.QueryRow(`SELECT schema_version, ledger_version FROM duration_ledger_meta WHERE singleton=1`).Scan(&schemaVersion, &ledgerVersion); err != nil {
			return fmt.Errorf("validate generation-one duration ledger metadata: %w", err)
		}
		if schemaVersion != 1 || ledgerVersion != durationLedgerVersion {
			return errors.New("generation-one duration ledger metadata schema is invalid")
		}
		return ensureGenerationOneAuthorityHistoryEmpty(transaction)
	}
	if err := ensureGenerationOneAuthorityHistoryEmpty(transaction); err != nil {
		return err
	}
	_, err = transaction.Exec(`INSERT INTO duration_ledger_meta(singleton,authority_id,schema_version,generation,ledger_version) VALUES(1,?,1,?,?)`,
		cicontract.SQLAuthorityID, strconv.FormatUint(generation, 10), durationLedgerVersion)
	if err != nil {
		return fmt.Errorf("initialize generation-one duration ledger metadata: %w", err)
	}
	return nil
}

// ensureGenerationOneAuthorityHistoryEmpty 只允许 schema-only 空库建立首代元数据。
func ensureGenerationOneAuthorityHistoryEmpty(transaction *sql.Tx) error {
	for _, table := range generationOneAuthorityEmptyTableNames() {
		var found bool
		query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s LIMIT 1)", table)
		if err := transaction.QueryRow(query).Scan(&found); err != nil {
			return fmt.Errorf("check generation-one history table %s: %w", table, err)
		}
		if found {
			return fmt.Errorf("generation-one SQLite contains orphan history in %s", table)
		}
	}
	return nil
}

// generationOneAuthorityEmptyTableNames 从 cicontract 唯一 owner 派生七个历史根，
// 再追加首代 schema-only 所需为空的 supporting tables，拒绝漂移的第二根列表。
func generationOneAuthorityEmptyTableNames() []string {
	bindings := cicontract.RetentionRootBindings()
	tables := make([]string, 0, len(bindings)+len(cicontract.GenerationOneAuthoritySupportingTables()))
	for _, binding := range bindings {
		tables = append(tables, binding.Table)
	}
	return append(tables, cicontract.GenerationOneAuthoritySupportingTables()...)
}

// ensureGenerationOneStateTableEmpty 拒绝已有 accepted singleton 或损坏的 singleton 行。
func ensureGenerationOneStateTableEmpty(transaction *sql.Tx) error {
	var singleton int
	err := transaction.QueryRow(`SELECT singleton FROM ci_remote_baseline_state WHERE singleton=1`).Scan(&singleton)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check remote baseline generation one empty precondition: %w", err)
	}
	if singleton != 1 {
		return errors.New("remote baseline generation one singleton is invalid")
	}
	return ErrRemoteBaselineGenerationOneAlreadyInitialized
}

type generationOneStateProjection struct {
	SchemaVersion          uint32                        `json:"schema_version"`
	Generation             uint64                        `json:"generation"`
	ExecutionProvider      string                        `json:"execution_provider"`
	RegionID               string                        `json:"region_id"`
	MainCommit             string                        `json:"main_commit"`
	MainTree               string                        `json:"main_tree"`
	Platform               string                        `json:"platform"`
	PolicyDigest           string                        `json:"policy_digest"`
	ToolchainDigest        string                        `json:"toolchain_digest"`
	RuntimeImage           string                        `json:"runtime_image"`
	ImageCacheID           string                        `json:"image_cache_id"`
	ImageCacheSnapshotID   string                        `json:"image_cache_snapshot_id"`
	ImageCacheReady        bool                          `json:"image_cache_ready"`
	ImageDigest            string                        `json:"image_digest"`
	OCIProjectCache        *generationOneOCIProjectCache `json:"oci_project_cache"`
	GateBinarySHA256       string                        `json:"gate_binary_sha256"`
	RuntimeSeedSHA256      string                        `json:"runtime_seed_manifest_sha256"`
	BaselineManifestDigest string                        `json:"baseline_manifest_digest"`
	CreatedAt              time.Time                     `json:"created_at"`
	AcceptedAt             time.Time                     `json:"accepted_at"`
	RenewedAt              time.Time                     `json:"renewed_at"`
}

type generationOneOCIProjectCache struct {
	Image                 string `json:"image"`
	ContentManifestSHA256 string `json:"content_manifest_sha256"`
	MainTree              string `json:"main_tree"`
	ToolchainDigest       string `json:"toolchain_digest"`
	Platform              string `json:"platform"`
	CachePath             string `json:"cache_path"`
}

// validateGenerationOneStateProjection 校验 receipt 中状态投影的 schema、缓存和 identity。
func validateGenerationOneStateProjection(receipt cicontract.GenerationOneProvisionReceipt) error {
	state, err := decodeGenerationOneStateProjection(receipt.StateJSON)
	if err != nil {
		return fmt.Errorf("decode generation-one state projection: %w", err)
	}
	if err := validateGenerationOneStateCache(state, receipt); err != nil {
		return err
	}
	if err := validateGenerationOneStateIdentity(state, receipt); err != nil {
		return err
	}
	return validateGenerationOneStateMetadata(state)
}

// decodeGenerationOneStateProjection 严格解码完整 BaselineState 的已知字段。
func decodeGenerationOneStateProjection(data []byte) (generationOneStateProjection, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state generationOneStateProjection
	if err := decoder.Decode(&state); err != nil {
		return generationOneStateProjection{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return generationOneStateProjection{}, errors.New("generation-one state projection contains multiple JSON values")
		}
		return generationOneStateProjection{}, err
	}
	canonical, err := json.Marshal(state)
	if err != nil {
		return generationOneStateProjection{}, fmt.Errorf("marshal canonical generation-one state projection: %w", err)
	}
	if !bytes.Equal(canonical, data) {
		return generationOneStateProjection{}, errors.New("generation-one state projection is not canonical JSON")
	}
	return state, nil
}

// validateGenerationOneStateCache 校验 generation、Ready cache 和 runtime digest 绑定。
func validateGenerationOneStateCache(state generationOneStateProjection, receipt cicontract.GenerationOneProvisionReceipt) error {
	if state.SchemaVersion != cicontract.BaselineStateSchemaVersion || state.Generation != 1 || state.ExecutionProvider != receipt.ExecutionProvider || state.RegionID != receipt.RegionID || !state.ImageCacheReady || state.ImageCacheID != receipt.ImageCacheID || state.ImageCacheSnapshotID != receipt.ImageCacheSnapshotID || state.RuntimeImage != receipt.RuntimeImage || state.ImageDigest != imageDigestFromReference(state.RuntimeImage) {
		return errors.New("generation-one state projection does not match the provision receipt")
	}
	return nil
}

// validateGenerationOneStateIdentity 校验源码与工件 identity 的完整绑定。
func validateGenerationOneStateIdentity(state generationOneStateProjection, receipt cicontract.GenerationOneProvisionReceipt) error {
	if err := validateGenerationOneStateSourceIdentity(state, receipt); err != nil {
		return err
	}
	return validateGenerationOneStateArtifactIdentity(state, receipt)
}

// validateGenerationOneStateSourceIdentity 校验提交、树、平台、策略和工具链摘要。
func validateGenerationOneStateSourceIdentity(state generationOneStateProjection, receipt cicontract.GenerationOneProvisionReceipt) error {
	if state.MainCommit != receipt.MainCommit || state.MainTree != receipt.MainTree || state.Platform != receipt.Platform || state.PolicyDigest != receipt.PolicyDigest || state.ToolchainDigest != receipt.ToolchainDigest {
		return errors.New("generation-one state projection identity does not match the provision receipt")
	}
	return nil
}

// validateGenerationOneStateArtifactIdentity 校验 Gate、seed 与 baseline manifest 摘要。
func validateGenerationOneStateArtifactIdentity(state generationOneStateProjection, receipt cicontract.GenerationOneProvisionReceipt) error {
	if state.GateBinarySHA256 != receipt.GateBinarySHA256 || state.RuntimeSeedSHA256 != receipt.RuntimeSeedSHA256 || state.BaselineManifestDigest != receipt.BaselineManifestDigest {
		return errors.New("generation-one state projection artifact identity does not match the provision receipt")
	}
	return nil
}

// validateGenerationOneStateMetadata 校验 OCI project cache 和首代时间戳的完整投影。
func validateGenerationOneStateMetadata(state generationOneStateProjection) error {
	if err := validateGenerationOneOCIProjectCache(state); err != nil {
		return err
	}
	return validateGenerationOneStateTimes(state)
}

// validateGenerationOneOCIProjectCache 校验嵌套 OCI project cache 的完整 identity。
func validateGenerationOneOCIProjectCache(state generationOneStateProjection) error {
	cache := state.OCIProjectCache
	if cache == nil || cache.Image != state.RuntimeImage || cache.MainTree != state.MainTree || cache.ToolchainDigest != state.ToolchainDigest || cache.Platform != cicontract.TargetPlatform || cache.CachePath != "/opt/super-dolphin/cache/go-build" || !validGenerationOneStateDigest(cache.ContentManifestSHA256) {
		return errors.New("generation-one OCI project cache projection is invalid")
	}
	return nil
}

// validateGenerationOneStateTimes 校验 accepted state 三个 UTC 时间戳的单调关系。
func validateGenerationOneStateTimes(state generationOneStateProjection) error {
	if state.CreatedAt.IsZero() || state.AcceptedAt.IsZero() || state.RenewedAt.IsZero() || state.CreatedAt.Location() != time.UTC || state.AcceptedAt.Location() != time.UTC || state.RenewedAt.Location() != time.UTC || state.AcceptedAt.Before(state.CreatedAt) || state.RenewedAt.Before(state.AcceptedAt) {
		return errors.New("generation-one state timestamps are invalid")
	}
	return nil
}

// validGenerationOneStateDigest 校验状态嵌套摘要的 sha256 形状。
func validGenerationOneStateDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

// imageDigestFromReference 提取 immutable OCI 引用中的 sha256 digest。
func imageDigestFromReference(reference string) string {
	_, digest, ok := strings.Cut(reference, "@")
	if !ok {
		return ""
	}
	return digest
}
