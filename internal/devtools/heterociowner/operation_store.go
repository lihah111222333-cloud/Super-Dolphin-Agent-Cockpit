package heterociowner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// ErrOperationConflict 表示 operation identity 已存在但请求内容发生漂移。
var ErrOperationConflict = errors.New("heteroci owner operation conflict")

// OperationRequest 是一次 durable mutation 的完整幂等输入。
type OperationRequest struct {
	Fence                heteroci.OwnerFence
	OperationID          heteroci.OperationID
	Kind                 string
	RequestBytes         []byte
	ResultBytes          []byte
	ProjectionDigest     string
	CommittedAtUnixMilli int64
}

// OperationRecord 是已提交 journal 行的不可变内存表示。
type OperationRecord struct {
	Scope                heteroci.RoutingOwnerScope
	OwnerEpoch           heteroci.RoutingOwnerEpoch
	Revision             heteroci.RoutingOwnerRevision
	OperationID          heteroci.OperationID
	Kind                 string
	RequestBytes         []byte
	RequestDigest        string
	ResultBytes          []byte
	ResultDigest         string
	PreviousJournal      string
	JournalDigest        string
	ProjectionDigest     string
	CommittedAtUnixMilli int64
}

// CommitOperation 在同一 immediate transaction 中执行 replay、CAS、journal 和 owner head 更新。
func CommitOperation(ctx context.Context, db *sql.DB, request OperationRequest) (OperationRecord, error) {
	if err := validateOperationRequest(request); err != nil {
		return OperationRecord{}, err
	}
	var record OperationRecord
	err := WithImmediateTx(ctx, db, func(tx *sql.Tx) error {
		var err error
		record, err = commitOperationTx(ctx, tx, request)
		return err
	})
	if err != nil {
		return OperationRecord{}, err
	}
	return record, nil
}

// validateOperationRequest 检查 operation fence、identity、payload 和 projection digest。
func validateOperationRequest(request OperationRequest) error {
	if err := request.Fence.Validate(); err != nil {
		return err
	}
	if _, err := heteroci.ParseOperationID(string(request.OperationID)); err != nil {
		return err
	}
	if err := validateOperationKind(request.Kind); err != nil {
		return err
	}
	if len(request.RequestBytes) == 0 || len(request.ResultBytes) == 0 {
		return fmt.Errorf("heteroci owner operation payload is empty")
	}
	if !validOwnerDigest(request.ProjectionDigest) {
		return fmt.Errorf("heteroci owner projection digest is invalid")
	}
	if request.CommittedAtUnixMilli < 0 {
		return fmt.Errorf("heteroci owner operation commit time is invalid")
	}
	return nil
}

// validateOperationKind 检查 operation kind 的稳定文本边界。
func validateOperationKind(kind string) error {
	if kind == "" || strings.TrimSpace(kind) != kind || strings.IndexFunc(kind, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || len(kind) > 128 {
		return fmt.Errorf("heteroci owner operation kind is invalid")
	}
	return nil
}

// commitOperationTx 在已持有 immediate transaction 时完成一次 journal mutation。
func commitOperationTx(ctx context.Context, tx *sql.Tx, request OperationRequest) (OperationRecord, error) {
	current, found, err := readOperationByID(ctx, tx, request.Fence.Scope, request.Fence.OwnerEpoch, request.OperationID)
	if err != nil {
		return OperationRecord{}, err
	}
	if found {
		if operationRequestMatches(current, request) {
			return current, nil
		}
		return OperationRecord{}, ErrOperationConflict
	}
	head, previousJournal, err := readCurrentOperationHead(ctx, tx, request.Fence)
	if err != nil {
		return OperationRecord{}, err
	}
	requestDigest := digestBytes(request.RequestBytes)
	resultDigest := digestBytes(request.ResultBytes)
	journalDigest := journalChainDigest(head, request, previousJournal, requestDigest, resultDigest, head.Revision+1)
	record := OperationRecord{
		Scope:                request.Fence.Scope,
		OwnerEpoch:           request.Fence.OwnerEpoch,
		Revision:             request.Fence.Revision + 1,
		OperationID:          request.OperationID,
		Kind:                 request.Kind,
		RequestBytes:         append([]byte(nil), request.RequestBytes...),
		RequestDigest:        requestDigest,
		ResultBytes:          append([]byte(nil), request.ResultBytes...),
		ResultDigest:         resultDigest,
		PreviousJournal:      previousJournal,
		JournalDigest:        journalDigest,
		ProjectionDigest:     request.ProjectionDigest,
		CommittedAtUnixMilli: request.CommittedAtUnixMilli,
	}
	if err := persistOperation(ctx, tx, record, head, previousJournal); err != nil {
		return OperationRecord{}, err
	}
	return record, nil
}

// readCurrentOperationHead 读取并校验当前 fence 与前置 journal digest。
func readCurrentOperationHead(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence) (heteroci.OwnerFence, string, error) {
	head, found, err := readOwnerHead(ctx, tx, fence.Scope)
	if err != nil {
		return heteroci.OwnerFence{}, "", err
	}
	if !found || head != fence {
		return heteroci.OwnerFence{}, "", fmt.Errorf("heteroci owner fence is stale")
	}
	if head.Revision >= heteroci.RoutingOwnerRevision(math.MaxInt64-1) {
		return heteroci.OwnerFence{}, "", fmt.Errorf("heteroci owner revision exhausted")
	}
	previousJournal, err := readOwnerJournalDigest(ctx, tx, fence.Scope)
	if err != nil {
		return heteroci.OwnerFence{}, "", err
	}
	return head, previousJournal, nil
}

func persistOperation(ctx context.Context, tx *sql.Tx, record OperationRecord, head heteroci.OwnerFence, previousJournal string) error {
	if err := insertOperation(ctx, tx, record); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_owner_heads SET revision = ?, journal_digest = ?, projection_digest = ?, updated_at_unix_milli = ? WHERE scope = ? AND owner_id = ? AND owner_epoch = ? AND revision = ? AND journal_digest = ?", record.Revision, record.JournalDigest, record.ProjectionDigest, record.CommittedAtUnixMilli, head.Scope, head.OwnerID, head.OwnerEpoch, head.Revision, previousJournal)
	if err != nil {
		return fmt.Errorf("update heteroci owner operation head: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("heteroci owner operation head CAS affected %d rows", rows)
	}
	return nil
}

func operationRequestMatches(record OperationRecord, request OperationRequest) bool {
	return record.Kind == request.Kind && bytes.Equal(record.RequestBytes, request.RequestBytes) && bytes.Equal(record.ResultBytes, request.ResultBytes) && record.ProjectionDigest == request.ProjectionDigest
}

func insertOperation(ctx context.Context, tx *sql.Tx, record OperationRecord) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO heteroci_routing_operations(scope, owner_epoch, revision, operation_id, operation_kind, request_bytes, request_digest, result_bytes, result_digest, prev_journal_digest, journal_digest, projection_digest, committed_at_unix_milli) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", record.Scope, record.OwnerEpoch, record.Revision, record.OperationID, record.Kind, record.RequestBytes, record.RequestDigest, record.ResultBytes, record.ResultDigest, record.PreviousJournal, record.JournalDigest, record.ProjectionDigest, record.CommittedAtUnixMilli)
	if err != nil {
		return fmt.Errorf("append heteroci owner operation: %w", err)
	}
	return nil
}

// readOperationByID 按 scope、owner epoch 和 operation ID 读取 journal，并在返回前校验记录、epoch fence 与 journal 完整性。
func readOperationByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope heteroci.RoutingOwnerScope, epoch heteroci.RoutingOwnerEpoch, operationID heteroci.OperationID) (OperationRecord, bool, error) {
	var record OperationRecord
	var requestDigest, resultDigest string
	row := queryer.QueryRowContext(ctx, "SELECT revision, operation_kind, request_bytes, request_digest, result_bytes, result_digest, prev_journal_digest, journal_digest, projection_digest, committed_at_unix_milli FROM heteroci_routing_operations WHERE scope = ? AND owner_epoch = ? AND operation_id = ?", scope, epoch, operationID)
	if err := row.Scan(&record.Revision, &record.Kind, &record.RequestBytes, &requestDigest, &record.ResultBytes, &resultDigest, &record.PreviousJournal, &record.JournalDigest, &record.ProjectionDigest, &record.CommittedAtUnixMilli); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OperationRecord{}, false, nil
		}
		return OperationRecord{}, false, fmt.Errorf("read heteroci owner operation: %w", err)
	}
	record.Scope = scope
	record.OwnerEpoch = epoch
	record.OperationID = operationID
	record.RequestDigest = requestDigest
	record.ResultDigest = resultDigest
	if err := validateOperationRecordIntegrity(record, requestDigest, resultDigest); err != nil {
		return OperationRecord{}, false, err
	}
	head, err := readOperationEpochFence(ctx, queryer, scope, epoch, record.Revision)
	if err != nil {
		return OperationRecord{}, false, err
	}
	if err := validateOperationJournal(record, head, requestDigest, resultDigest); err != nil {
		return OperationRecord{}, false, err
	}
	return record, true, nil
}

// validateOperationRecordIntegrity 校验 operation payload、kind、提交时间和 digest 集合，发现持久化内容漂移立即失败。
func validateOperationRecordIntegrity(record OperationRecord, requestDigest, resultDigest string) error {
	if len(record.RequestBytes) == 0 || len(record.ResultBytes) == 0 {
		return fmt.Errorf("heteroci owner operation payload is empty")
	}
	if err := validateOperationKind(record.Kind); err != nil {
		return fmt.Errorf("validate heteroci owner operation kind: %w", err)
	}
	if record.CommittedAtUnixMilli < 0 {
		return fmt.Errorf("heteroci owner operation commit time is invalid")
	}
	if want := digestBytes(record.RequestBytes); requestDigest != want {
		return fmt.Errorf("heteroci owner operation request digest mismatch")
	}
	if want := digestBytes(record.ResultBytes); resultDigest != want {
		return fmt.Errorf("heteroci owner operation result digest mismatch")
	}
	if err := validateOperationDigestSet(record); err != nil {
		return err
	}
	return nil
}

func validateOperationDigest(name, digest string) error {
	if !validOwnerDigest(digest) {
		return fmt.Errorf("heteroci owner operation %s digest is invalid", name)
	}
	return nil
}

func validateOperationDigestSet(record OperationRecord) error {
	if err := validateOperationDigest("previous journal", record.PreviousJournal); err != nil {
		return err
	}
	if err := validateOperationDigest("journal", record.JournalDigest); err != nil {
		return err
	}
	if err := validateOperationDigest("projection", record.ProjectionDigest); err != nil {
		return err
	}
	return nil
}

func readOperationEpochFence(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope heteroci.RoutingOwnerScope, epoch heteroci.RoutingOwnerEpoch, revision heteroci.RoutingOwnerRevision) (heteroci.OwnerFence, error) {
	var ownerID string
	if err := queryer.QueryRowContext(ctx, "SELECT owner_id FROM heteroci_owner_epochs WHERE scope = ? AND owner_epoch = ?", scope, epoch).Scan(&ownerID); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("read heteroci owner operation epoch: %w", err)
	}
	parsedOwnerID, err := heteroci.ParseRoutingOwnerID(ownerID)
	if err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("validate heteroci owner operation epoch: %w", err)
	}
	if revision <= heteroci.RoutingOwnerRevision(epoch) {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci owner operation revision is not after epoch")
	}
	head, err := heteroci.NewOwnerFence(scope, parsedOwnerID, epoch, revision)
	if err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("validate heteroci owner operation fence: %w", err)
	}
	return head, nil
}

func validateOperationJournal(record OperationRecord, head heteroci.OwnerFence, requestDigest, resultDigest string) error {
	request := OperationRequest{
		Fence:            head,
		OperationID:      record.OperationID,
		Kind:             record.Kind,
		RequestBytes:     record.RequestBytes,
		ResultBytes:      record.ResultBytes,
		ProjectionDigest: record.ProjectionDigest,
	}
	if want := journalChainDigest(head, request, record.PreviousJournal, requestDigest, resultDigest, record.Revision); record.JournalDigest != want {
		return fmt.Errorf("heteroci owner operation journal digest mismatch")
	}
	return nil
}

// readOwnerJournalDigest 读取 CAS 链当前的前置 journal digest。
func readOwnerJournalDigest(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope heteroci.RoutingOwnerScope) (string, error) {
	var digest string
	if err := queryer.QueryRowContext(ctx, "SELECT journal_digest FROM heteroci_owner_heads WHERE scope = ?", scope).Scan(&digest); err != nil {
		return "", fmt.Errorf("read heteroci owner journal head: %w", err)
	}
	if !validOwnerDigest(digest) {
		return "", fmt.Errorf("heteroci owner journal digest is invalid")
	}
	return digest, nil
}

func journalChainDigest(head heteroci.OwnerFence, request OperationRequest, previousJournal, requestDigest, resultDigest string, revision heteroci.RoutingOwnerRevision) string {
	hash := sha256.New()
	writeFrame := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	writeFrame("heteroci-routing-journal-chain/v1")
	writeFrame(previousJournal)
	writeFrame(string(head.Scope))
	writeFrame(string(head.OwnerID))
	writeFrame(fmt.Sprint(head.OwnerEpoch))
	writeFrame(fmt.Sprint(revision))
	writeFrame(string(request.OperationID))
	writeFrame(request.Kind)
	writeFrame(requestDigest)
	writeFrame(resultDigest)
	writeFrame(request.ProjectionDigest)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// validOwnerDigest 检查带 sha256 前缀的 64 位小写摘要。
func validOwnerDigest(value string) bool {
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
