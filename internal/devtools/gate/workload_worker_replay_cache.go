package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	workerExecutionReplayCacheWorkloadID = "@worker-execution-digest"
	workerExecutionReplayCacheDomain     = "remote-worker-execution-replay-cache/v1"
)

// WorkerExecutionReplayCacheEntry 保存 immutable source tree 的精确 worker 执行摘要。
// 它复用 source replay 派生索引的物理表，但保留 key 不属于 workload PASS identity。
type WorkerExecutionReplayCacheEntry struct {
	SourceTreeSHA string
	WorkerDigest  string
}

// LookupWorkerExecutionReplayCache 批量读取同一 generation 与 worker 算法下的 tree 摘要。
func (store *DurationLedgerStore) LookupWorkerExecutionReplayCache(acceptedGeneration uint64, algorithmDigest string, sourceTrees []string) (map[string]string, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	trees, err := canonicalWorkerExecutionReplayTrees(sourceTrees)
	if err != nil {
		return nil, err
	}
	if err := validateWorkerExecutionReplayCoordinates(acceptedGeneration, algorithmDigest); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(trees))
	if len(trees) == 0 {
		return result, nil
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin worker execution replay cache lookup", err)
	}
	defer tx.Rollback()
	if err := requireWorkloadInputReplayCacheGeneration(tx, acceptedGeneration); err != nil {
		return nil, err
	}
	if err := loadWorkerExecutionReplayCache(tx, acceptedGeneration, algorithmDigest, trees, result); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit worker execution replay cache lookup", err)
	}
	return result, nil
}

// SaveWorkerExecutionReplayCache 原子写入 tree 级 worker 摘要；同键不同值立即拒绝。
func (store *DurationLedgerStore) SaveWorkerExecutionReplayCache(acceptedGeneration uint64, algorithmDigest string, entries []WorkerExecutionReplayCacheEntry) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	canonical, err := canonicalWorkerExecutionReplayEntries(entries)
	if err != nil || len(canonical) == 0 {
		return err
	}
	if err := validateWorkerExecutionReplayCoordinates(acceptedGeneration, algorithmDigest); err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return err
	}
	defer database.Close()
	return saveWorkerExecutionReplayCacheTransaction(database, acceptedGeneration, algorithmDigest, canonical)
}

func saveWorkerExecutionReplayCacheTransaction(database *sql.DB, acceptedGeneration uint64, algorithmDigest string, canonical []WorkerExecutionReplayCacheEntry) error {
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return mapDurationLedgerSQLiteError("begin worker execution replay cache write", err)
	}
	defer tx.Rollback()
	if err := requireWorkloadInputReplayCacheGeneration(tx, acceptedGeneration); err != nil {
		return err
	}
	if err := insertWorkerExecutionReplayEntries(tx, acceptedGeneration, algorithmDigest, canonical); err != nil {
		return err
	}
	if err := verifyWorkerExecutionReplayEntries(tx, acceptedGeneration, algorithmDigest, canonical); err != nil {
		return err
	}
	if err := compactDurationLedgerAuthority(tx); err != nil {
		return fmt.Errorf("compact worker execution replay cache authority: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return mapDurationLedgerSQLiteError("commit worker execution replay cache write", err)
	}
	return nil
}

func validateWorkerExecutionReplayCoordinates(acceptedGeneration uint64, algorithmDigest string) error {
	if acceptedGeneration == 0 {
		return errors.New("worker execution replay cache accepted generation is required")
	}
	if !validSHA256Digest(algorithmDigest) || algorithmDigest != strings.ToLower(algorithmDigest) {
		return errors.New("worker execution replay cache algorithm digest is invalid")
	}
	return nil
}

func canonicalWorkerExecutionReplayTrees(sourceTrees []string) ([]string, error) {
	seen := make(map[string]struct{}, len(sourceTrees))
	canonical := make([]string, 0, len(sourceTrees))
	for _, tree := range sourceTrees {
		if !validLocalTreeObjectID(tree) || tree != strings.ToLower(tree) {
			return nil, errors.New("worker execution replay cache source tree is invalid")
		}
		if _, duplicate := seen[tree]; duplicate {
			continue
		}
		seen[tree] = struct{}{}
		canonical = append(canonical, tree)
	}
	sort.Strings(canonical)
	return canonical, nil
}

func canonicalWorkerExecutionReplayEntries(entries []WorkerExecutionReplayCacheEntry) ([]WorkerExecutionReplayCacheEntry, error) {
	byTree := make(map[string]string, len(entries))
	for _, entry := range entries {
		if !validLocalTreeObjectID(entry.SourceTreeSHA) || entry.SourceTreeSHA != strings.ToLower(entry.SourceTreeSHA) {
			return nil, errors.New("worker execution replay cache source tree is invalid")
		}
		if !validSHA256Digest(entry.WorkerDigest) || entry.WorkerDigest != strings.ToLower(entry.WorkerDigest) {
			return nil, errors.New("worker execution replay cache worker digest is invalid")
		}
		if existing, ok := byTree[entry.SourceTreeSHA]; ok && existing != entry.WorkerDigest {
			return nil, fmt.Errorf("worker execution replay cache source tree %q is ambiguous", entry.SourceTreeSHA)
		}
		byTree[entry.SourceTreeSHA] = entry.WorkerDigest
	}
	trees := make([]string, 0, len(byTree))
	for tree := range byTree {
		trees = append(trees, tree)
	}
	sort.Strings(trees)
	canonical := make([]WorkerExecutionReplayCacheEntry, 0, len(trees))
	for _, tree := range trees {
		canonical = append(canonical, WorkerExecutionReplayCacheEntry{SourceTreeSHA: tree, WorkerDigest: byTree[tree]})
	}
	return canonical, nil
}

func workerExecutionReplayCacheSHA256(acceptedGeneration uint64, algorithmDigest string, entry WorkerExecutionReplayCacheEntry) string {
	payload := fmt.Sprintf("%s\n%d\n%s\n%s\n%s\n", workerExecutionReplayCacheDomain, acceptedGeneration, entry.SourceTreeSHA, algorithmDigest, entry.WorkerDigest)
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("sha256:%x", digest)
}

func loadWorkerExecutionReplayCache(tx *sql.Tx, acceptedGeneration uint64, algorithmDigest string, trees []string, result map[string]string) error {
	for start := 0; start < len(trees); start += workloadPassSourceReplayBatchSize {
		end := min(start+workloadPassSourceReplayBatchSize, len(trees))
		if err := loadWorkerExecutionReplayCacheBatch(tx, acceptedGeneration, algorithmDigest, trees[start:end], result); err != nil {
			return err
		}
	}
	return nil
}

func loadWorkerExecutionReplayCacheBatch(tx *sql.Tx, acceptedGeneration uint64, algorithmDigest string, trees []string, result map[string]string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(trees)), ",")
	args := make([]any, 0, len(trees)+3)
	args = append(args, strconv.FormatUint(acceptedGeneration, 10), algorithmDigest, workerExecutionReplayCacheWorkloadID)
	for _, tree := range trees {
		args = append(args, tree)
	}
	rows, err := tx.Query(`SELECT source_tree_sha, input_digest, cache_sha256 FROM ci_workload_input_replay_cache WHERE accepted_generation = ? AND input_algorithm_digest = ? AND workload_id = ? AND source_tree_sha IN (`+placeholders+`) ORDER BY source_tree_sha`, args...)
	if err != nil {
		return mapDurationLedgerSQLiteError("query worker execution replay cache", err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scanWorkerExecutionReplayCacheRow(rows, acceptedGeneration, algorithmDigest, result); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate worker execution replay cache", err)
	}
	return nil
}

func scanWorkerExecutionReplayCacheRow(rows *sql.Rows, acceptedGeneration uint64, algorithmDigest string, result map[string]string) error {
	var tree, workerDigest, storedSHA string
	if err := rows.Scan(&tree, &workerDigest, &storedSHA); err != nil {
		return mapDurationLedgerSQLiteError("scan worker execution replay cache", err)
	}
	entry := WorkerExecutionReplayCacheEntry{SourceTreeSHA: tree, WorkerDigest: workerDigest}
	if !validLocalTreeObjectID(tree) || !validSHA256Digest(workerDigest) {
		return errors.New("worker execution replay cache row is invalid")
	}
	if storedSHA != workerExecutionReplayCacheSHA256(acceptedGeneration, algorithmDigest, entry) {
		return errors.New("worker execution replay cache digest mismatch")
	}
	result[tree] = workerDigest
	return nil
}

func insertWorkerExecutionReplayEntries(tx *sql.Tx, acceptedGeneration uint64, algorithmDigest string, entries []WorkerExecutionReplayCacheEntry) error {
	statement, err := tx.Prepare(`INSERT INTO ci_workload_input_replay_cache(accepted_generation, source_tree_sha, input_algorithm_digest, workload_id, input_digest, cache_sha256) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return mapDurationLedgerSQLiteError("prepare worker execution replay cache insert", err)
	}
	defer statement.Close()
	for _, entry := range entries {
		if _, err := statement.Exec(strconv.FormatUint(acceptedGeneration, 10), entry.SourceTreeSHA, algorithmDigest, workerExecutionReplayCacheWorkloadID, entry.WorkerDigest, workerExecutionReplayCacheSHA256(acceptedGeneration, algorithmDigest, entry)); err != nil {
			return mapDurationLedgerSQLiteError("insert worker execution replay cache", err)
		}
	}
	return nil
}

func verifyWorkerExecutionReplayEntries(tx *sql.Tx, acceptedGeneration uint64, algorithmDigest string, entries []WorkerExecutionReplayCacheEntry) error {
	for _, entry := range entries {
		var digest, cacheSHA string
		err := tx.QueryRow(`SELECT input_digest, cache_sha256 FROM ci_workload_input_replay_cache WHERE accepted_generation = ? AND source_tree_sha = ? AND input_algorithm_digest = ? AND workload_id = ?`, strconv.FormatUint(acceptedGeneration, 10), entry.SourceTreeSHA, algorithmDigest, workerExecutionReplayCacheWorkloadID).Scan(&digest, &cacheSHA)
		if err != nil {
			return mapDurationLedgerSQLiteError("verify worker execution replay cache", err)
		}
		if digest != entry.WorkerDigest || cacheSHA != workerExecutionReplayCacheSHA256(acceptedGeneration, algorithmDigest, entry) {
			return fmt.Errorf("worker execution replay cache source tree %q conflicts with stored digest", entry.SourceTreeSHA)
		}
	}
	return nil
}
