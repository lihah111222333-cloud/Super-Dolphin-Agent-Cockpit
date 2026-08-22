package heterociexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

const (
	canonicalReceiptRowSchema     = "heteroci-batch-receipt/v1"
	canonicalReceiptClaimSchema   = "heteroci-batch-claim/v1"
	canonicalReceiptStateClaimed  = "claimed"
	canonicalReceiptStateTerminal = "terminal"
)

var (
	// ErrCanonicalReceiptStore 表示 provider-neutral canonical receipt 无效。
	ErrCanonicalReceiptStore = errors.New("heteroci canonical receipt is invalid")
	// ErrCanonicalReceiptConflict 表示 canonical receipt identity 已漂移。
	ErrCanonicalReceiptConflict = errors.New("heteroci canonical receipt conflicts with persisted identity")
)

type canonicalReceiptClaim struct {
	SchemaVersion  string                       `json:"schema_version"`
	BatchID        heterociworkflow.BatchID     `json:"batch_id"`
	RunID          string                       `json:"run_id"`
	Executor       heterociworkflow.ExecutorKey `json:"executor"`
	MaterialDigest string                       `json:"material_digest"`
	CommandDigest  string                       `json:"command_digest"`
	UnitIDs        []string                     `json:"unit_ids"`
}

type canonicalReceiptRow struct {
	SchemaVersion string
	CommandDigest string
	State         string
	ClaimJSON     []byte
	ClaimDigest   string
	ResultJSON    []byte
	ResultDigest  sql.NullString
}

type canonicalReceiptQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// prepareCanonicalReceiptClaim 生成与底层存储无关的 canonical batch claim。
func prepareCanonicalReceiptClaim(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (canonicalReceiptClaim, []byte, string, error) {
	if err := validateCanonicalReceiptContext(ctx); err != nil {
		return canonicalReceiptClaim{}, nil, "", err
	}
	if _, err := validateExecutionBatch(batch); err != nil {
		return canonicalReceiptClaim{}, nil, "", errors.Join(ErrCanonicalReceiptStore, err)
	}
	if !validCanonicalReceiptDigest(commandDigest) {
		return canonicalReceiptClaim{}, nil, "", fmt.Errorf("%w: command digest is invalid", ErrCanonicalReceiptStore)
	}
	claim := canonicalReceiptClaim{SchemaVersion: canonicalReceiptClaimSchema, BatchID: batch.BatchID, RunID: batch.RunID, Executor: batch.Key, MaterialDigest: batch.MaterialDigest, CommandDigest: commandDigest, UnitIDs: make([]string, len(batch.Units))}
	for index, unit := range batch.Units {
		claim.UnitIDs[index] = unit.UnitID
	}
	encoded, err := json.Marshal(claim)
	if err != nil {
		return canonicalReceiptClaim{}, nil, "", fmt.Errorf("%w: encode claim: %w", ErrCanonicalReceiptStore, err)
	}
	return claim, encoded, digestCanonicalReceiptBytes(encoded), nil
}

// prepareCanonicalBatchResult 生成 canonical batch terminal bytes 与 digest。
func prepareCanonicalBatchResult(ctx context.Context, result heterociworkflow.BatchResult) ([]byte, string, error) {
	if err := validateCanonicalReceiptContext(ctx); err != nil {
		return nil, "", err
	}
	if err := validateCanonicalBatchResultShape(result); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, "", fmt.Errorf("%w: encode terminal receipt: %w", ErrCanonicalReceiptStore, err)
	}
	return encoded, digestCanonicalReceiptBytes(encoded), nil
}

func validateCanonicalReceiptContext(ctx context.Context) error {
	if ctx == nil {
		return ErrCanonicalReceiptStore
	}
	return ctx.Err()
}

// validateCanonicalReceiptClaimRow 对 persisted claim 执行完整字段和 canonical bytes 校验。
func validateCanonicalReceiptClaimRow(row canonicalReceiptRow, want canonicalReceiptClaim, wantJSON []byte, wantDigest string) error {
	if row.SchemaVersion != canonicalReceiptRowSchema {
		return ErrCanonicalReceiptStore
	}
	if row.CommandDigest != want.CommandDigest {
		return ErrCanonicalReceiptConflict
	}
	if row.ClaimDigest != wantDigest || !bytes.Equal(row.ClaimJSON, wantJSON) {
		return ErrCanonicalReceiptStore
	}
	persisted, err := decodeCanonicalReceiptClaim(row.ClaimJSON, row.ClaimDigest)
	if err != nil {
		return err
	}
	if !sameCanonicalReceiptClaim(persisted, want) {
		return ErrCanonicalReceiptStore
	}
	return validateCanonicalReceiptRowState(row)
}

func validateCanonicalReceiptRowState(row canonicalReceiptRow) error {
	switch row.State {
	case canonicalReceiptStateClaimed:
		if row.ResultJSON != nil || row.ResultDigest.Valid {
			return ErrCanonicalReceiptStore
		}
	case canonicalReceiptStateTerminal:
		if len(row.ResultJSON) == 0 || !row.ResultDigest.Valid {
			return ErrCanonicalReceiptStore
		}
	default:
		return ErrCanonicalReceiptStore
	}
	return nil
}

func decodeCanonicalReceiptClaim(raw []byte, digest string) (canonicalReceiptClaim, error) {
	if len(raw) == 0 || !validCanonicalReceiptDigest(digest) || digestCanonicalReceiptBytes(raw) != digest {
		return canonicalReceiptClaim{}, ErrCanonicalReceiptStore
	}
	var claim canonicalReceiptClaim
	if err := decodeStrictCanonicalReceiptJSON(raw, &claim); err != nil {
		return canonicalReceiptClaim{}, fmt.Errorf("%w: decode claim: %w", ErrCanonicalReceiptStore, err)
	}
	if err := validateCanonicalReceiptClaimShape(claim); err != nil {
		return canonicalReceiptClaim{}, err
	}
	return claim, nil
}

func decodeCanonicalBatchResult(raw []byte, digest sql.NullString) (heterociworkflow.BatchResult, error) {
	if len(raw) == 0 || !digest.Valid || !validCanonicalReceiptDigest(digest.String) || digestCanonicalReceiptBytes(raw) != digest.String {
		return heterociworkflow.BatchResult{}, ErrCanonicalReceiptStore
	}
	var result heterociworkflow.BatchResult
	if err := decodeStrictCanonicalReceiptJSON(raw, &result); err != nil {
		return heterociworkflow.BatchResult{}, fmt.Errorf("%w: decode terminal receipt: %w", ErrCanonicalReceiptStore, err)
	}
	if err := validateCanonicalBatchResultShape(result); err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	return result, nil
}

func decodeStrictCanonicalReceiptJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON is invalid")
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, canonical) {
		return fmt.Errorf("JSON is not canonical")
	}
	return nil
}

func validateCanonicalReceiptClaimShape(claim canonicalReceiptClaim) error {
	if !validCanonicalClaimIdentity(claim) {
		return ErrCanonicalReceiptStore
	}
	if err := claim.Executor.Validate(); err != nil {
		return errors.Join(ErrCanonicalReceiptStore, err)
	}
	if len(claim.UnitIDs) == 0 {
		return ErrCanonicalReceiptStore
	}
	return validateCanonicalClaimUnits(claim.UnitIDs)
}

func validCanonicalClaimIdentity(claim canonicalReceiptClaim) bool {
	return claim.SchemaVersion == canonicalReceiptClaimSchema && validCommandBatchID(claim.BatchID) && validCanonicalReceiptIdentity(claim.RunID) && validCanonicalReceiptDigest(claim.MaterialDigest) && validCanonicalReceiptDigest(claim.CommandDigest)
}

func validateCanonicalClaimUnits(unitIDs []string) error {
	seen := make(map[string]struct{}, len(unitIDs))
	for _, unitID := range unitIDs {
		if !validCanonicalReceiptIdentity(unitID) {
			return ErrCanonicalReceiptStore
		}
		if _, found := seen[unitID]; found {
			return ErrCanonicalReceiptStore
		}
		seen[unitID] = struct{}{}
	}
	return nil
}

func validCanonicalResultIdentity(result heterociworkflow.BatchResult) bool {
	return validCommandBatchID(result.BatchID) && validCanonicalReceiptIdentity(result.RunID) && validCanonicalReceiptDigest(result.MaterialDigest) && validCanonicalReceiptDigest(result.CommandDigest)
}

func validateCanonicalResultOutcomes(outcomes []heterociworkflow.BatchOutcome) error {
	if len(outcomes) == 0 {
		return ErrCanonicalReceiptStore
	}
	seen := make(map[string]struct{}, len(outcomes))
	for _, outcome := range outcomes {
		if !validCanonicalReceiptIdentity(outcome.UnitID) || (outcome.State != heteroci.LeaseStateSucceeded && outcome.State != heteroci.LeaseStateFailed) {
			return ErrCanonicalReceiptStore
		}
		if _, found := seen[outcome.UnitID]; found {
			return ErrCanonicalReceiptStore
		}
		seen[outcome.UnitID] = struct{}{}
	}
	return nil
}

func validateCanonicalBatchResultShape(result heterociworkflow.BatchResult) error {
	if !validCanonicalResultIdentity(result) {
		return ErrCanonicalReceiptStore
	}
	if err := result.Executor.Validate(); err != nil {
		return errors.Join(ErrCanonicalReceiptStore, err)
	}
	if err := validateReceiptHeader(result); err != nil {
		return errors.Join(ErrCanonicalReceiptStore, err)
	}
	return validateCanonicalResultOutcomes(result.Outcomes)
}

func validateCanonicalResultAgainstClaim(result heterociworkflow.BatchResult, claim canonicalReceiptClaim) error {
	if result.BatchID != claim.BatchID || result.RunID != claim.RunID || result.Executor != claim.Executor || result.MaterialDigest != claim.MaterialDigest || result.CommandDigest != claim.CommandDigest {
		return ErrCanonicalReceiptConflict
	}
	if len(result.Outcomes) != len(claim.UnitIDs) {
		return ErrCanonicalReceiptConflict
	}
	for index, outcome := range result.Outcomes {
		if outcome.UnitID != claim.UnitIDs[index] {
			return ErrCanonicalReceiptConflict
		}
	}
	return nil
}

func sameCanonicalReceiptClaim(left, right canonicalReceiptClaim) bool {
	if left.SchemaVersion != right.SchemaVersion || left.BatchID != right.BatchID || left.RunID != right.RunID || left.Executor != right.Executor || left.MaterialDigest != right.MaterialDigest || left.CommandDigest != right.CommandDigest || len(left.UnitIDs) != len(right.UnitIDs) {
		return false
	}
	for index := range left.UnitIDs {
		if left.UnitIDs[index] != right.UnitIDs[index] {
			return false
		}
	}
	return true
}

func validCanonicalReceiptIdentity(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 256 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validCanonicalReceiptDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func digestCanonicalReceiptBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
