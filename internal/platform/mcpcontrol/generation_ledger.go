package mcpcontrol

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

type sqliteGenerationState struct {
	generation        uint64
	claimID           string
	externalCommitted int
	exists            bool
}

type generationLedgerRecord struct {
	Epoch      string `json:"epoch"`
	InstanceID string `json:"instance_id"`
	Generation uint64 `json:"generation"`
	ClaimID    string `json:"claim_id"`
}

type generationLedgerSnapshot struct {
	committedGeneration uint64
	committedClaimID    string
	pendingGeneration   uint64
	pendingClaimID      string
}

const generationLedgerRequiredMarker = "ledger-v1"

func readGenerationOwnerEpoch(markerPath string) (string, error) {
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return "", fmt.Errorf("read mcpcontrol generation owner epoch: %w", err)
	}
	epoch, _, err := parseGenerationOwnerMarker(raw)
	if err != nil {
		return "", err
	}
	return epoch, nil
}

// ensureGenerationLedger 建立并验证不可回滚 ledger 的外部 owner 与 SQLite 初始化位。
func (s *SQLiteGenerationStore) ensureGenerationLedger(epoch string) error {
	return platformdb.WithImmediateTx(context.Background(), s.db, func(tx *sql.Tx) error {
		initialized, err := loadGenerationLedgerInitialized(tx)
		if err != nil {
			return err
		}
		_, ledgerRequired, err := readGenerationOwnerMarkerState(s.markerPath)
		if err != nil {
			return err
		}
		ownerPath := filepath.Join(s.markerPath+".ledger", "owner")
		needsInitialization, err := inspectGenerationLedgerOwner(
			initialized,
			ledgerRequired,
			ownerPath,
			epoch,
		)
		if err != nil || !needsInitialization {
			return err
		}
		if err := initializeGenerationLedgerOwner(tx, ownerPath, epoch); err != nil {
			return err
		}
		if err := ensureGenerationLedgerRequiredMarker(s.markerPath, epoch, ledgerRequired); err != nil {
			return err
		}
		return markGenerationLedgerInitialized(tx)
	})
}

func loadGenerationLedgerInitialized(tx *sql.Tx) (int, error) {
	var initialized int
	if err := tx.QueryRow(
		"SELECT ledger_initialized FROM mcp_managed_generation_owner WHERE singleton_id = ?",
		generationOwnerMetaID,
	).Scan(&initialized); err != nil {
		return 0, fmt.Errorf("load mcpcontrol generation ledger metadata: %w", err)
	}
	return initialized, nil
}

// inspectGenerationLedgerOwner 校验 SQLite 初始化位与外部 owner 文件是否形成一致历史。
func inspectGenerationLedgerOwner(
	initialized int,
	ledgerRequired bool,
	ownerPath string,
	epoch string,
) (bool, error) {
	raw, err := os.ReadFile(ownerPath)
	if ledgerRequired && errors.Is(err, os.ErrNotExist) {
		return false, errors.New("mcpcontrol generation ledger history is missing after external initialization")
	}
	if initialized == 1 {
		return inspectInitializedGenerationLedgerOwner(raw, err, epoch)
	}
	if initialized != 0 {
		return false, errors.New("mcpcontrol generation ledger metadata is invalid")
	}
	return inspectUninitializedGenerationLedgerOwner(raw, err, epoch)
}

func inspectInitializedGenerationLedgerOwner(raw []byte, readErr error, epoch string) (bool, error) {
	if errors.Is(readErr, os.ErrNotExist) {
		return false, errors.New("mcpcontrol generation ledger history is missing after durable initialization")
	}
	if readErr != nil {
		return false, fmt.Errorf("read mcpcontrol generation ledger owner: %w", readErr)
	}
	if strings.TrimSpace(string(raw)) != epoch {
		return false, errors.New("mcpcontrol generation ledger owner conflicts with durable epoch")
	}
	return false, nil
}

func inspectUninitializedGenerationLedgerOwner(raw []byte, readErr error, epoch string) (bool, error) {
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return false, fmt.Errorf("read mcpcontrol generation ledger owner: %w", readErr)
	}
	if readErr == nil && strings.TrimSpace(string(raw)) != epoch {
		return false, errors.New("mcpcontrol generation ledger owner conflicts with durable epoch")
	}
	return true, nil
}

func readGenerationOwnerMarkerState(markerPath string) (string, bool, error) {
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return "", false, fmt.Errorf("read mcpcontrol generation owner marker state: %w", err)
	}
	return parseGenerationOwnerMarker(raw)
}

// parseGenerationOwnerMarker 解析追加式 epoch 与 ledger-required 永久标志。
func parseGenerationOwnerMarker(raw []byte) (string, bool, error) {
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 0 || len(lines[0]) != 64 {
		return "", false, errors.New("mcpcontrol generation owner marker is invalid")
	}
	switch len(lines) {
	case 1:
		return lines[0], false, nil
	case 2:
		if lines[1] != generationLedgerRequiredMarker {
			return "", false, errors.New("mcpcontrol generation owner marker ledger state is invalid")
		}
		return lines[0], true, nil
	default:
		return "", false, errors.New("mcpcontrol generation owner marker has unexpected history")
	}
}

// ensureGenerationLedgerRequiredMarker 追加并 fsync 不可逆的 ledger-required 标志。
func ensureGenerationLedgerRequiredMarker(markerPath, epoch string, alreadyRequired bool) error {
	if alreadyRequired {
		return nil
	}
	file, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open mcpcontrol generation owner marker for ledger initialization: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(generationLedgerRequiredMarker + "\n"); err != nil {
		return fmt.Errorf("append mcpcontrol generation ledger initialization: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync mcpcontrol generation ledger initialization: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close mcpcontrol generation ledger initialization: %w", err)
	}
	ownerEpoch, required, err := readGenerationOwnerMarkerState(markerPath)
	if err != nil {
		return err
	}
	if ownerEpoch != epoch || !required {
		return errors.New("mcpcontrol generation ledger initialization marker did not persist")
	}
	return syncGenerationDirectory(filepath.Dir(markerPath))
}

func initializeGenerationLedgerOwner(tx *sql.Tx, ownerPath, epoch string) error {
	if _, err := os.Stat(ownerPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat mcpcontrol generation ledger owner: %w", err)
	}
	var generations int
	if err := tx.QueryRow("SELECT COUNT(*) FROM mcp_managed_generations").Scan(&generations); err != nil {
		return fmt.Errorf("count mcpcontrol generation history before ledger initialization: %w", err)
	}
	if generations != 0 {
		return errors.New("mcpcontrol generation ledger is missing for existing history")
	}
	return writeGenerationLedgerOwner(ownerPath, epoch)
}

func markGenerationLedgerInitialized(tx *sql.Tx) error {
	result, err := tx.Exec(
		"UPDATE mcp_managed_generation_owner SET ledger_initialized = 1 WHERE singleton_id = ? AND ledger_initialized = 0",
		generationOwnerMetaID,
	)
	if err != nil {
		return fmt.Errorf("initialize mcpcontrol generation ledger metadata: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect mcpcontrol generation ledger initialization CAS: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("mcpcontrol generation ledger initialization CAS affected %d rows", affected)
	}
	return nil
}

func writeGenerationLedgerOwner(path, epoch string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create mcpcontrol generation ledger directory: %w", err)
	}
	if err := writeExclusiveGenerationFile(path, []byte(epoch+"\n")); err != nil {
		return fmt.Errorf("create mcpcontrol generation ledger owner: %w", err)
	}
	return syncGenerationDirectory(filepath.Dir(path))
}

// nextWithLedger 在文件锁保护下完成 intent、SQLite CAS、commit 和确认四阶段推进。
func (s *SQLiteGenerationStore) nextWithLedger(
	epoch string,
	instanceID string,
	resumeFrom *uint64,
) (uint64, error) {
	state, snapshot, err := s.reconcileGenerationLedger(epoch, instanceID)
	if err != nil {
		return 0, err
	}
	if err := validateGenerationResume(state.generation, resumeFrom); err != nil {
		return 0, err
	}
	next, claimID, err := s.reserveGenerationClaim(epoch, instanceID, state, snapshot)
	if err != nil {
		return 0, err
	}
	if err := s.advanceSQLiteGenerationClaim(instanceID, state, next, claimID); err != nil {
		return 0, err
	}
	if err := s.writeGenerationLedgerRecord(epoch, instanceID, next, claimID, "commit"); err != nil {
		return 0, err
	}
	if err := s.confirmGenerationClaim(instanceID, next, claimID); err != nil {
		return 0, err
	}
	return next, nil
}

func validateGenerationResume(current uint64, resumeFrom *uint64) error {
	if resumeFrom != nil && *resumeFrom != current {
		return fmt.Errorf(
			"mcpcontrol resume generation %d does not match durable high-water %d",
			*resumeFrom,
			current,
		)
	}
	if current == math.MaxInt64 {
		return errors.New("mcpcontrol generation exhausted")
	}
	return nil
}

func (s *SQLiteGenerationStore) reserveGenerationClaim(
	epoch string,
	instanceID string,
	state sqliteGenerationState,
	snapshot generationLedgerSnapshot,
) (uint64, string, error) {
	next := state.generation + 1
	if snapshot.pendingGeneration == next {
		return next, snapshot.pendingClaimID, nil
	}
	claimID, err := newGenerationClaimID()
	if err != nil {
		return 0, "", err
	}
	if err := s.writeGenerationLedgerRecord(epoch, instanceID, next, claimID, "intent"); err != nil {
		return 0, "", err
	}
	return next, claimID, nil
}

// advanceSQLiteGenerationClaim 将已持久化的 intent 以 CAS 方式写入 SQLite 当前状态。
func (s *SQLiteGenerationStore) advanceSQLiteGenerationClaim(
	instanceID string,
	state sqliteGenerationState,
	next uint64,
	claimID string,
) error {
	return platformdb.WithImmediateTx(context.Background(), s.db, func(tx *sql.Tx) error {
		if err := s.validateOwnerMarker(tx); err != nil {
			return err
		}
		latest, err := loadSQLiteGeneration(tx, instanceID)
		if err != nil {
			return err
		}
		if latest.generation != state.generation || latest.exists != state.exists {
			return errors.New("mcpcontrol generation state changed while owner lock was held")
		}
		if !latest.exists {
			return createSQLiteGeneration(tx, instanceID, claimID)
		}
		if next != latest.generation+1 {
			return errors.New("mcpcontrol reserved generation is not the next durable generation")
		}
		return advanceSQLiteGeneration(tx, instanceID, latest.generation, claimID)
	})
}

// reconcileGenerationLedger 比较 SQLite 与不可回滚 ledger，并恢复可判定的崩溃窗口。
func (s *SQLiteGenerationStore) reconcileGenerationLedger(
	epoch string,
	instanceID string,
) (sqliteGenerationState, generationLedgerSnapshot, error) {
	snapshot, err := s.readGenerationLedger(epoch, instanceID)
	if err != nil {
		return sqliteGenerationState{}, generationLedgerSnapshot{}, err
	}
	state, err := s.loadGenerationState(instanceID)
	if err != nil {
		return sqliteGenerationState{}, generationLedgerSnapshot{}, err
	}
	if state.generation < snapshot.committedGeneration {
		return sqliteGenerationState{}, generationLedgerSnapshot{}, fmt.Errorf(
			"mcpcontrol generation rollback detected for %q: SQLite=%d ledger=%d",
			instanceID,
			state.generation,
			snapshot.committedGeneration,
		)
	}
	if state.generation == 0 {
		return state, snapshot, nil
	}
	if state.generation == snapshot.committedGeneration {
		return s.reconcileMatchedGeneration(instanceID, state, snapshot)
	}
	return s.reconcilePendingGeneration(epoch, instanceID, state, snapshot)
}

func (s *SQLiteGenerationStore) reconcileMatchedGeneration(
	instanceID string,
	state sqliteGenerationState,
	snapshot generationLedgerSnapshot,
) (sqliteGenerationState, generationLedgerSnapshot, error) {
	if state.claimID != snapshot.committedClaimID {
		return sqliteGenerationState{}, generationLedgerSnapshot{},
			errors.New("mcpcontrol generation ledger claim does not match SQLite")
	}
	if state.externalCommitted == 0 {
		if err := s.confirmGenerationClaim(
			instanceID,
			state.generation,
			state.claimID,
		); err != nil {
			return sqliteGenerationState{}, generationLedgerSnapshot{}, err
		}
		state.externalCommitted = 1
	}
	return state, snapshot, nil
}

// reconcilePendingGeneration 补齐 SQLite 已提交但外部 commit 尚未落盘的单步崩溃窗口。
func (s *SQLiteGenerationStore) reconcilePendingGeneration(
	epoch string,
	instanceID string,
	state sqliteGenerationState,
	snapshot generationLedgerSnapshot,
) (sqliteGenerationState, generationLedgerSnapshot, error) {
	if state.generation != snapshot.committedGeneration+1 {
		return sqliteGenerationState{}, generationLedgerSnapshot{},
			errors.New("mcpcontrol generation ledger history is incomplete")
	}
	if state.externalCommitted == 1 {
		return sqliteGenerationState{}, generationLedgerSnapshot{},
			errors.New("mcpcontrol committed generation ledger record is missing")
	}
	if snapshot.pendingGeneration != state.generation || snapshot.pendingClaimID != state.claimID {
		return sqliteGenerationState{}, generationLedgerSnapshot{},
			errors.New("mcpcontrol pending generation ledger claim does not match SQLite")
	}
	if err := s.writeGenerationLedgerRecord(
		epoch,
		instanceID,
		state.generation,
		state.claimID,
		"commit",
	); err != nil {
		return sqliteGenerationState{}, generationLedgerSnapshot{}, err
	}
	if err := s.confirmGenerationClaim(instanceID, state.generation, state.claimID); err != nil {
		return sqliteGenerationState{}, generationLedgerSnapshot{}, err
	}
	return s.reconcileGenerationLedger(epoch, instanceID)
}

func (s *SQLiteGenerationStore) loadGenerationState(instanceID string) (sqliteGenerationState, error) {
	var state sqliteGenerationState
	err := platformdb.WithImmediateTx(context.Background(), s.db, func(tx *sql.Tx) error {
		if err := s.validateOwnerMarker(tx); err != nil {
			return err
		}
		loaded, err := loadSQLiteGeneration(tx, instanceID)
		state = loaded
		return err
	})
	return state, err
}

func (s *SQLiteGenerationStore) confirmGenerationClaim(
	instanceID string,
	generation uint64,
	claimID string,
) error {
	return platformdb.WithImmediateTx(context.Background(), s.db, func(tx *sql.Tx) error {
		if err := s.validateOwnerMarker(tx); err != nil {
			return err
		}
		return confirmSQLiteGeneration(tx, instanceID, generation, claimID)
	})
}

func (s *SQLiteGenerationStore) readGenerationLedger(
	epoch string,
	instanceID string,
) (generationLedgerSnapshot, error) {
	records, err := s.readGenerationLedgerRecords(epoch, instanceID)
	if err != nil {
		return generationLedgerSnapshot{}, err
	}
	return buildGenerationLedgerSnapshot(records)
}

type generationLedgerRecords struct {
	intents map[uint64]string
	commits map[uint64]string
}

func (s *SQLiteGenerationStore) readGenerationLedgerRecords(
	epoch string,
	instanceID string,
) (generationLedgerRecords, error) {
	records := generationLedgerRecords{
		intents: make(map[uint64]string),
		commits: make(map[uint64]string),
	}
	instanceDir := generationLedgerInstanceDir(s.markerPath, instanceID)
	entries, err := os.ReadDir(instanceDir)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return records, fmt.Errorf("read mcpcontrol generation ledger: %w", err)
	}
	for _, entry := range entries {
		if err := collectGenerationLedgerEntry(
			records,
			instanceDir,
			entry,
			epoch,
			instanceID,
		); err != nil {
			return generationLedgerRecords{}, err
		}
	}
	return records, nil
}

// collectGenerationLedgerEntry 读取并验证一个不可变 intent 或 commit 记录。
func collectGenerationLedgerEntry(
	records generationLedgerRecords,
	instanceDir string,
	entry os.DirEntry,
	epoch string,
	instanceID string,
) error {
	if entry.IsDir() {
		return errors.New("mcpcontrol generation ledger contains a directory")
	}
	generation, claimID, kind, err := parseGenerationLedgerName(entry.Name())
	if err != nil {
		return err
	}
	record, err := readGenerationLedgerRecord(
		filepath.Join(instanceDir, entry.Name()),
		epoch,
		instanceID,
		generation,
		claimID,
	)
	if err != nil {
		return err
	}
	target := records.intents
	if kind == "commit" {
		target = records.commits
	}
	if previous, exists := target[generation]; exists && previous != record.ClaimID {
		return errors.New("mcpcontrol generation ledger has conflicting claims")
	}
	target[generation] = record.ClaimID
	return nil
}

// buildGenerationLedgerSnapshot 要求 commit 从 1 连续，并仅允许一个下一代 pending intent。
func buildGenerationLedgerSnapshot(
	records generationLedgerRecords,
) (generationLedgerSnapshot, error) {
	var snapshot generationLedgerSnapshot
	for generation := uint64(1); ; generation++ {
		claimID, ok := records.commits[generation]
		if !ok {
			break
		}
		if records.intents[generation] != claimID {
			return generationLedgerSnapshot{}, errors.New("mcpcontrol committed generation lacks matching intent")
		}
		snapshot.committedGeneration = generation
		snapshot.committedClaimID = claimID
	}
	if len(records.commits) != int(snapshot.committedGeneration) {
		return generationLedgerSnapshot{}, errors.New("mcpcontrol committed generation ledger has a gap")
	}
	pending := snapshot.committedGeneration + 1
	if claimID, ok := records.intents[pending]; ok {
		snapshot.pendingGeneration = pending
		snapshot.pendingClaimID = claimID
	}
	if len(records.intents) > len(records.commits)+boolToInt(snapshot.pendingGeneration != 0) {
		return generationLedgerSnapshot{}, errors.New("mcpcontrol generation ledger has stray intents")
	}
	return snapshot, nil
}

func (s *SQLiteGenerationStore) writeGenerationLedgerRecord(
	epoch string,
	instanceID string,
	generation uint64,
	claimID string,
	kind string,
) error {
	record := generationLedgerRecord{
		Epoch:      epoch,
		InstanceID: instanceID,
		Generation: generation,
		ClaimID:    claimID,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode mcpcontrol generation ledger record: %w", err)
	}
	instanceDir := generationLedgerInstanceDir(s.markerPath, instanceID)
	if err := os.MkdirAll(instanceDir, 0o700); err != nil {
		return fmt.Errorf("create mcpcontrol generation ledger instance directory: %w", err)
	}
	name := fmt.Sprintf("%020d.%s.%s", generation, claimID, kind)
	path := filepath.Join(instanceDir, name)
	if err := writeExclusiveGenerationFile(path, append(raw, '\n')); err != nil {
		return fmt.Errorf("create mcpcontrol generation ledger record: %w", err)
	}
	return syncGenerationDirectory(instanceDir)
}

// writeExclusiveGenerationFile 以 create-exclusive 和 fsync 持久化不可变 ledger 文件。
func writeExclusiveGenerationFile(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

// readGenerationLedgerRecord 校验 ledger 文件名承诺与文件内容完全一致。
func readGenerationLedgerRecord(
	path string,
	epoch string,
	instanceID string,
	generation uint64,
	claimID string,
) (generationLedgerRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return generationLedgerRecord{}, fmt.Errorf("read mcpcontrol generation ledger record: %w", err)
	}
	var record generationLedgerRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return generationLedgerRecord{}, fmt.Errorf("decode mcpcontrol generation ledger record: %w", err)
	}
	if record.Epoch != epoch || record.InstanceID != instanceID ||
		record.Generation != generation || record.ClaimID != claimID {
		return generationLedgerRecord{}, errors.New("mcpcontrol generation ledger record content is invalid")
	}
	return record, nil
}

// parseGenerationLedgerName 解析固定 generation、claim 和阶段三元组。
func parseGenerationLedgerName(name string) (uint64, string, string, error) {
	parts := strings.Split(name, ".")
	if len(parts) != 3 || (parts[2] != "intent" && parts[2] != "commit") ||
		len(parts[0]) != 20 || len(parts[1]) != 64 {
		return 0, "", "", fmt.Errorf("mcpcontrol generation ledger filename %q is invalid", name)
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return 0, "", "", fmt.Errorf("mcpcontrol generation ledger claim %q is invalid", parts[1])
	}
	generation, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || generation == 0 {
		return 0, "", "", fmt.Errorf("mcpcontrol generation ledger generation %q is invalid", parts[0])
	}
	return generation, parts[1], parts[2], nil
}

func generationLedgerInstanceDir(markerPath, instanceID string) string {
	digest := sha256.Sum256([]byte(instanceID))
	return filepath.Join(markerPath+".ledger", hex.EncodeToString(digest[:]))
}

func newGenerationClaimID() (string, error) {
	var claim [32]byte
	if _, err := rand.Read(claim[:]); err != nil {
		return "", fmt.Errorf("generate mcpcontrol generation claim: %w", err)
	}
	return hex.EncodeToString(claim[:]), nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
