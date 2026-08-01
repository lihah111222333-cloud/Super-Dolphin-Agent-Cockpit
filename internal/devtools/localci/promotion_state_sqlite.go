package localci

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const promotionAuthoritySchemaSQL = `
CREATE TABLE IF NOT EXISTS coordinator_authority_migrations (
 name TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS coordinator_accepted_image_state_v2 (
 authority_key TEXT PRIMARY KEY,
 record_json BLOB NOT NULL,
 record_digest TEXT NOT NULL,
 generation INTEGER NOT NULL CHECK (generation > 0),
 predecessor_digest TEXT NOT NULL,
 canonical_payload BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS coordinator_promotion_candidates_v2 (
 authority_key TEXT NOT NULL,
 candidate_id TEXT NOT NULL,
 workload_id TEXT NOT NULL,
 status TEXT NOT NULL,
 expected_accepted_generation INTEGER NOT NULL CHECK (expected_accepted_generation > 0),
 expected_accepted_record_digest TEXT NOT NULL,
 candidate_json BLOB NOT NULL,
 revision INTEGER NOT NULL CHECK (revision > 0),
 PRIMARY KEY (authority_key, candidate_id),
 UNIQUE (authority_key, workload_id)
);
CREATE TABLE IF NOT EXISTS coordinator_promotion_candidate_state_v2 (
	authority_key TEXT PRIMARY KEY,
	revision INTEGER NOT NULL CHECK (revision > 0)
);
`

// EnsurePromotionAuthoritySQLiteSchema installs the two coordinator-owned
// authorities.  Production code calls this inside coordinator schema setup;
// constructors call it too so a direct caller cannot run against a partial DB.
type promotionAuthoritySchemaDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func EnsurePromotionAuthoritySQLiteSchema(ctx context.Context, db promotionAuthoritySchemaDB) error {
	if db == nil {
		return errors.New("promotion authority SQLite database is required")
	}
	if _, err := db.ExecContext(ctx, promotionAuthoritySchemaSQL); err != nil {
		return fmt.Errorf("initialize promotion authority SQLite schema: %w", err)
	}
	return nil
}

func (s *AcceptedImageState) importLegacyAcceptedImage(ctx context.Context) error {
	if err := EnsurePromotionAuthoritySQLiteSchema(ctx, s.db); err != nil {
		return err
	}
	var imported int
	marker := "accepted-image-json-v2:" + s.authorityKey
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coordinator_authority_migrations WHERE name = ?", marker).Scan(&imported); err != nil {
		return fmt.Errorf("read accepted image legacy migration marker: %w", err)
	}
	if imported != 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin accepted image legacy import: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM coordinator_accepted_image_state_v2 WHERE authority_key = ?", s.authorityKey).Scan(&count); err != nil {
		return fmt.Errorf("read accepted image SQLite state: %w", err)
	}
	if count == 0 {
		db := s.db
		s.db = nil
		record, loadErr := s.loadLocked(ctx)
		s.db = db
		if loadErr != nil && !errors.Is(loadErr, ErrAcceptedImageStateNotFound) {
			return fmt.Errorf("load legacy accepted image state: %w", loadErr)
		}
		if loadErr == nil {
			if err := insertAcceptedImageSQLite(ctx, tx, s.authorityKey, record); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO coordinator_authority_migrations(name) VALUES (?)", marker); err != nil {
		return fmt.Errorf("record accepted image legacy import: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit accepted image legacy import: %w", err)
	}
	return nil
}

func (s *AcceptedImageState) loadSQLite(ctx context.Context) (gateRecord gate.AcceptedImageRecord, retErr error) {
	var encoded, digest string
	var payload []byte
	var generation uint64
	var predecessor string
	err := s.db.QueryRowContext(ctx, `SELECT record_json, record_digest, generation, predecessor_digest, canonical_payload
		FROM coordinator_accepted_image_state_v2 WHERE authority_key = ?`, s.authorityKey).Scan(&encoded, &digest, &generation, &predecessor, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return gateRecord, ErrAcceptedImageStateNotFound
	}
	if err != nil {
		return gateRecord, fmt.Errorf("load accepted image SQLite state: %w", err)
	}
	if err := gate.DecodeStrictJSON([]byte(encoded), &gateRecord); err != nil {
		return gateRecord, fmt.Errorf("decode accepted image SQLite record: %w", err)
	}
	canonical, err := canonicalAcceptedImageBytes(gateRecord)
	if err != nil || !bytes.Equal([]byte(encoded), canonical) {
		if err != nil {
			return gateRecord, err
		}
		return gateRecord, errors.New("accepted image SQLite record is not canonical")
	}
	actualDigest, err := gate.AcceptedImageRecordDigest(gateRecord)
	if err != nil || actualDigest != digest || gateRecord.Generation != generation || gateRecord.PreviousRecordDigest != predecessor {
		return gateRecord, errors.New("accepted image SQLite identity drifted")
	}
	actualPayload, err := gate.AcceptedImageSigningPayload(gateRecord)
	if err != nil || !bytes.Equal(actualPayload, payload) {
		return gateRecord, errors.New("accepted image SQLite signing payload drifted")
	}
	if err := s.verifyRecord(ctx, gateRecord); err != nil {
		return gateRecord, err
	}
	return gateRecord, nil
}

func (s *AcceptedImageState) bootstrapSQLite(ctx context.Context, record gate.AcceptedImageRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin accepted image bootstrap: %w", err)
	}
	defer tx.Rollback()
	if err := insertAcceptedImageSQLite(ctx, tx, s.authorityKey, record); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit accepted image bootstrap: %w", err)
	}
	return nil
}

func insertAcceptedImageSQLite(ctx context.Context, tx *sql.Tx, authorityKey string, record gate.AcceptedImageRecord) error {
	encoded, err := canonicalAcceptedImageBytes(record)
	if err != nil {
		return err
	}
	digest, err := gate.AcceptedImageRecordDigest(record)
	if err != nil {
		return fmt.Errorf("digest accepted image SQLite record: %w", err)
	}
	payload, err := gate.AcceptedImageSigningPayload(record)
	if err != nil {
		return fmt.Errorf("canonicalize accepted image SQLite payload: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO coordinator_accepted_image_state_v2
		(authority_key, record_json, record_digest, generation, predecessor_digest, canonical_payload) VALUES (?, ?, ?, ?, ?, ?)`,
		authorityKey, encoded, digest, record.Generation, record.PreviousRecordDigest, payload)
	if err != nil {
		return fmt.Errorf("insert accepted image SQLite state: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return err
		}
		return ErrAcceptedImageStateExists
	}
	return nil
}

func (s *AcceptedImageState) promoteSQLite(ctx context.Context, promotion gate.PromotionRecord) error {
	return gateprivate.RetrySQLiteWrite(ctx, 20, func() error {
		return s.promoteSQLiteAttempt(ctx, promotion)
	})
}

func (s *AcceptedImageState) promoteSQLiteAttempt(ctx context.Context, promotion gate.PromotionRecord) error {
	current, err := s.loadSQLite(ctx)
	if err != nil {
		return err
	}
	currentDigest, err := gate.AcceptedImageRecordDigest(current)
	if err != nil {
		return fmt.Errorf("digest current accepted image: %w", err)
	}
	if currentDigest != promotion.ExpectedRecordDigest || current.Generation != promotion.ExpectedGeneration {
		return ErrAcceptedImageCASConflict
	}
	if err := s.validatePromotion(ctx, current, currentDigest, promotion.Next); err != nil {
		return err
	}
	encoded, err := canonicalAcceptedImageBytes(promotion.Next)
	if err != nil {
		return err
	}
	digest, err := gate.AcceptedImageRecordDigest(promotion.Next)
	if err != nil {
		return err
	}
	payload, err := gate.AcceptedImageSigningPayload(promotion.Next)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE coordinator_accepted_image_state_v2
		SET record_json = ?, record_digest = ?, generation = ?, predecessor_digest = ?, canonical_payload = ?
		WHERE authority_key = ? AND record_digest = ? AND generation = ?`, encoded, digest, promotion.Next.Generation, promotion.Next.PreviousRecordDigest, payload, s.authorityKey, promotion.ExpectedRecordDigest, promotion.ExpectedGeneration)
	if err != nil {
		return fmt.Errorf("promote accepted image SQLite state: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrAcceptedImageCASConflict
	}
	return nil
}

func (s *PromotionCandidateStore) importLegacyPromotionCandidates(ctx context.Context) error {
	if err := EnsurePromotionAuthoritySQLiteSchema(ctx, s.db); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin promotion candidate legacy import: %w", err)
	}
	defer tx.Rollback()
	var marked int
	marker := "promotion-candidates-json-v2:" + s.authorityKey
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM coordinator_authority_migrations WHERE name = ?", marker).Scan(&marked); err != nil {
		return err
	}
	if marked != 0 {
		return nil
	}
	db := s.db
	s.db = nil
	snapshot, loadErr := s.loadLocked()
	s.db = db
	if loadErr != nil {
		return fmt.Errorf("load legacy promotion candidates: %w", loadErr)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO coordinator_promotion_candidate_state_v2(authority_key, revision) VALUES (?, 1)", s.authorityKey); err != nil {
		return fmt.Errorf("initialize promotion candidate SQLite revision: %w", err)
	}
	if err := replacePromotionCandidatesSQLite(ctx, tx, s.authorityKey, snapshot); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO coordinator_authority_migrations(name) VALUES (?)", marker); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit promotion candidate legacy import: %w", err)
	}
	return nil
}

func (s *PromotionCandidateStore) loadSQLite() (promotionCandidateSnapshot, error) {
	snapshot := promotionCandidateSnapshot{SchemaVersion: promotionCandidateSchemaVersion, Candidates: []PromotionCandidate{}}
	if err := s.db.QueryRow("SELECT revision FROM coordinator_promotion_candidate_state_v2 WHERE authority_key = ?", s.authorityKey).Scan(&snapshot.Revision); err != nil {
		return snapshot, fmt.Errorf("load promotion candidate SQLite revision: %w", err)
	}
	rows, err := s.db.Query(`SELECT candidate_json FROM coordinator_promotion_candidates_v2 WHERE authority_key = ? ORDER BY candidate_id`, s.authorityKey)
	if err != nil {
		return snapshot, fmt.Errorf("query promotion candidates SQLite state: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return snapshot, err
		}
		var candidate PromotionCandidate
		if err := gate.DecodeStrictJSON(encoded, &candidate); err != nil {
			return snapshot, fmt.Errorf("decode promotion candidate SQLite state: %w", err)
		}
		snapshot.Candidates = append(snapshot.Candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return snapshot, err
	}
	if err := snapshot.Validate(); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (s *PromotionCandidateStore) saveSQLite(snapshot promotionCandidateSnapshot) error {
	if snapshot.Revision == 0 {
		return errors.New("promotion candidate SQLite snapshot revision is required")
	}
	return gateprivate.RetrySQLiteWrite(context.Background(), 20, func() error {
		return s.saveSQLiteAttempt(snapshot)
	})
}

func (s *PromotionCandidateStore) saveSQLiteAttempt(snapshot promotionCandidateSnapshot) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin promotion candidate SQLite write: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(context.Background(), `UPDATE coordinator_promotion_candidate_state_v2
		SET revision = revision + 1 WHERE authority_key = ? AND revision = ?`, s.authorityKey, snapshot.Revision)
	if err != nil {
		return fmt.Errorf("advance promotion candidate SQLite revision: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrPromotionCandidateState
	}
	if err := replacePromotionCandidatesSQLite(context.Background(), tx, s.authorityKey, snapshot); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit promotion candidate SQLite write: %w", err)
	}
	return nil
}

func replacePromotionCandidatesSQLite(ctx context.Context, tx *sql.Tx, authorityKey string, snapshot promotionCandidateSnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM coordinator_promotion_candidates_v2 WHERE authority_key = ?", authorityKey); err != nil {
		return fmt.Errorf("clear promotion candidates SQLite state: %w", err)
	}
	for _, candidate := range snapshot.Candidates {
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO coordinator_promotion_candidates_v2
			(authority_key, candidate_id, workload_id, status, expected_accepted_generation, expected_accepted_record_digest, candidate_json, revision)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1)`, authorityKey, candidate.CandidateID, candidate.WorkloadID, candidate.Status, candidate.ExpectedAcceptedGeneration, candidate.ExpectedAcceptedRecordDigest, encoded); err != nil {
			return fmt.Errorf("insert promotion candidate SQLite state: %w", err)
		}
	}
	return nil
}
