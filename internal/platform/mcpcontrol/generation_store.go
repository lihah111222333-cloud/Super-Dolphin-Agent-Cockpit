package mcpcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)


const generationOwnerMetaID = 1

// GenerationStore 持久化每个 managed instance 已消费的 generation 高水位。
type GenerationStore interface {
	Next(instanceID string, resumeFrom *uint64) (uint64, error)
}

// MemoryGenerationStore 为测试和显式内存 runtime 保存 generation 高水位。
type MemoryGenerationStore struct {
	mu     sync.Mutex
	latest map[string]uint64
}

// NewMemoryGenerationStore 创建不会在 registry 重建时自动丢失的可共享内存 owner。
func NewMemoryGenerationStore() *MemoryGenerationStore {
	return &MemoryGenerationStore{latest: make(map[string]uint64)}
}

// Next 校验 resume fence 并原子推进 generation。
func (s *MemoryGenerationStore) Next(instanceID string, resumeFrom *uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nextGeneration(s.latest, instanceID, resumeFrom)
}

// SQLiteGenerationStore 使用产品 SQLite 的 IMMEDIATE 事务作为跨进程 generation owner。
type SQLiteGenerationStore struct {
	db         *sql.DB
	markerPath string
}

// NewSQLiteGenerationStore 创建共享 SQLite generation owner；schema 必须由规范 migration 提供。
func NewSQLiteGenerationStore(db *sql.DB, markerPath string) (*SQLiteGenerationStore, error) {
	markerPath = strings.TrimSpace(markerPath)
	if db == nil {
		return nil, errors.New("mcpcontrol generation store requires SQLite DB")
	}
	if markerPath == "" || !filepath.IsAbs(markerPath) {
		return nil, errors.New("mcpcontrol generation owner marker requires an absolute path")
	}
	return &SQLiteGenerationStore{db: db, markerPath: filepath.Clean(markerPath)}, nil
}

// Next 在跨进程 IMMEDIATE 事务内验证 owner epoch、resume fence 并推进高水位。
func (s *SQLiteGenerationStore) Next(instanceID string, resumeFrom *uint64) (uint64, error) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return 0, errors.New("mcpcontrol generation store requires instance_id")
	}
	var next uint64
	err := withGenerationOwnerLock(s.markerPath+".lock", func() error {
		if err := s.ensureOwnerMarker(); err != nil {
			return err
		}
		epoch, err := readGenerationOwnerEpoch(s.markerPath)
		if err != nil {
			return err
		}
		if err := s.ensureGenerationLedger(epoch); err != nil {
			return err
		}
		generation, err := s.nextWithLedger(epoch, instanceID, resumeFrom)
		next = generation
		return err
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// ensureOwnerMarker 单独提交 owner 初始化，避免非法 resume 回滚数据库标记却留下已 fsync 的外部 marker。
func (s *SQLiteGenerationStore) ensureOwnerMarker() error {
	return platformdb.WithImmediateTx(context.Background(), s.db, s.validateOwnerMarker)
}

// validateOwnerMarker 交叉验证数据库 epoch 与外部 marker，阻断任一侧历史丢失。
func (s *SQLiteGenerationStore) validateOwnerMarker(tx *sql.Tx) error {
	var epoch string
	var initialized int
	if err := tx.QueryRow(
		"SELECT owner_epoch, marker_initialized FROM mcp_managed_generation_owner WHERE singleton_id = ?",
		generationOwnerMetaID,
	).Scan(&epoch, &initialized); err != nil {
		return fmt.Errorf("load mcpcontrol generation owner metadata: %w", err)
	}
	epoch = strings.TrimSpace(epoch)
	if epoch == "" || (initialized != 0 && initialized != 1) {
		return errors.New("mcpcontrol generation owner metadata is invalid")
	}
	marker, err := os.ReadFile(s.markerPath)
	if initialized == 0 {
		return s.initializeOwnerMarker(tx, epoch, marker, err)
	}
	return validateInitializedOwnerMarker(epoch, marker, err)
}

// initializeOwnerMarker 只允许 migration 创建的新 epoch 首次写入外部 marker。
func (s *SQLiteGenerationStore) initializeOwnerMarker(
	tx *sql.Tx,
	epoch string,
	marker []byte,
	markerErr error,
) error {
	switch {
	case errors.Is(markerErr, os.ErrNotExist):
		if err := writeGenerationOwnerMarker(s.markerPath, epoch); err != nil {
			return err
		}
	case markerErr != nil:
		return fmt.Errorf("read uninitialized mcpcontrol generation owner marker: %w", markerErr)
	default:
		markerEpoch, _, err := parseGenerationOwnerMarker(marker)
		if err != nil {
			return err
		}
		if markerEpoch != epoch {
			return errors.New("mcpcontrol generation owner marker conflicts with uninitialized durable epoch")
		}
	}
	result, err := tx.Exec(
		"UPDATE mcp_managed_generation_owner SET marker_initialized = 1 WHERE singleton_id = ? AND marker_initialized = 0",
		generationOwnerMetaID,
	)
	if err != nil {
		return fmt.Errorf("initialize mcpcontrol generation owner marker: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect mcpcontrol generation owner marker CAS: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("initialize mcpcontrol generation owner marker CAS affected %d rows", affected)
	}
	return nil
}

func validateInitializedOwnerMarker(epoch string, marker []byte, markerErr error) error {
	if errors.Is(markerErr, os.ErrNotExist) {
		return errors.New("mcpcontrol generation owner marker is missing after durable initialization")
	}
	if markerErr != nil {
		return fmt.Errorf("read mcpcontrol generation owner marker: %w", markerErr)
	}
	markerEpoch, _, err := parseGenerationOwnerMarker(marker)
	if err != nil {
		return err
	}
	if markerEpoch != epoch {
		return errors.New("mcpcontrol generation owner marker does not match durable epoch")
	}
	return nil
}

// loadSQLiteGeneration 要求实例存在标记和高水位成对出现，任何半状态都 fail-fast。
func loadSQLiteGeneration(tx *sql.Tx, instanceID string) (sqliteGenerationState, error) {
	var markerCount int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM mcp_managed_generation_instances WHERE instance_id = ?",
		instanceID,
	).Scan(&markerCount); err != nil {
		return sqliteGenerationState{}, fmt.Errorf("load mcpcontrol generation instance marker: %w", err)
	}
	var state sqliteGenerationState
	err := tx.QueryRow(
		"SELECT generation, claim_id, external_committed FROM mcp_managed_generations WHERE instance_id = ?",
		instanceID,
	).Scan(&state.generation, &state.claimID, &state.externalCommitted)
	return validateLoadedSQLiteGeneration(state, markerCount, err)
}

// validateLoadedSQLiteGeneration 要求实例 marker 与完整 generation claim 同时存在或同时缺失。
func validateLoadedSQLiteGeneration(
	state sqliteGenerationState,
	markerCount int,
	queryErr error,
) (sqliteGenerationState, error) {
	if markerCount == 0 && errors.Is(queryErr, sql.ErrNoRows) {
		return sqliteGenerationState{}, nil
	}
	if markerCount == 1 && queryErr == nil {
		state.exists = true
		if len(state.claimID) != 64 || (state.externalCommitted != 0 && state.externalCommitted != 1) {
			return sqliteGenerationState{}, errors.New("mcpcontrol generation durable claim is invalid")
		}
		return state, nil
	}
	if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
		return sqliteGenerationState{}, fmt.Errorf("load mcpcontrol generation high-water: %w", queryErr)
	}
	return sqliteGenerationState{}, errors.New("mcpcontrol generation history is incomplete")
}

func createSQLiteGeneration(tx *sql.Tx, instanceID, claimID string) error {
	if _, err := tx.Exec(
		"INSERT INTO mcp_managed_generation_instances(instance_id) VALUES (?)",
		instanceID,
	); err != nil {
		return fmt.Errorf("create mcpcontrol generation instance marker: %w", err)
	}
	if _, err := tx.Exec(
		"INSERT INTO mcp_managed_generations(instance_id, generation, claim_id, external_committed) VALUES (?, 1, ?, 0)",
		instanceID, claimID,
	); err != nil {
		return fmt.Errorf("create mcpcontrol generation high-water: %w", err)
	}
	return nil
}

func advanceSQLiteGeneration(tx *sql.Tx, instanceID string, current uint64, claimID string) error {
	result, err := tx.Exec(
		"UPDATE mcp_managed_generations SET generation = ?, claim_id = ?, external_committed = 0 WHERE instance_id = ? AND generation = ? AND external_committed = 1",
		current+1, claimID, instanceID, current,
	)
	if err != nil {
		return fmt.Errorf("advance mcpcontrol generation high-water: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect mcpcontrol generation CAS: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("mcpcontrol generation CAS lost for %q at %d", instanceID, current)
	}
	return nil
}

func confirmSQLiteGeneration(tx *sql.Tx, instanceID string, generation uint64, claimID string) error {
	result, err := tx.Exec(
		"UPDATE mcp_managed_generations SET external_committed = 1 WHERE instance_id = ? AND generation = ? AND claim_id = ? AND external_committed = 0",
		instanceID, generation, claimID,
	)
	if err != nil {
		return fmt.Errorf("confirm mcpcontrol external generation claim: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect mcpcontrol generation confirmation CAS: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("mcpcontrol generation confirmation CAS affected %d rows", affected)
	}
	return nil
}

// writeGenerationOwnerMarker 以 create-exclusive 和 fsync 持久化首次 owner epoch。
func writeGenerationOwnerMarker(path, epoch string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create mcpcontrol generation marker directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create mcpcontrol generation owner marker: %w", err)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.WriteString(epoch + "\n"); err != nil {
		return fmt.Errorf("write mcpcontrol generation owner marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync mcpcontrol generation owner marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close mcpcontrol generation owner marker: %w", err)
	}
	if err := securefs.SyncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	ok = true
	return nil
}

func nextGeneration(latest map[string]uint64, instanceID string, resumeFrom *uint64) (uint64, error) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return 0, errors.New("mcpcontrol generation store requires instance_id")
	}
	current := latest[instanceID]
	if resumeFrom != nil && *resumeFrom != current {
		return 0, fmt.Errorf("mcpcontrol resume generation %d does not match durable high-water %d", *resumeFrom, current)
	}
	if current == math.MaxUint64 {
		return 0, errors.New("mcpcontrol generation exhausted")
	}
	current++
	latest[instanceID] = current
	return current, nil
}

