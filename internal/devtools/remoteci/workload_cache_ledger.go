package remoteci

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// loadPassedWorkloadCacheWithSQLite 先查共享 SQLite 投影，仅对未知身份访问 OSS。
func loadPassedWorkloadCacheWithSQLite(
	ctx context.Context,
	store ObjectStore,
	ledgerStore *gate.DurationLedgerStore,
	now func() time.Time,
	entries []remoteWorkloadCacheEntry,
	legacyEntries []remoteWorkloadCacheEntry,
	forceRerun bool,
	fallbackEntries ...[]remoteWorkloadCacheEntry,
) (map[string]gate.PlanGateExecution, error) {
	if ledgerStore == nil {
		return loadPassedWorkloadCacheWithLegacy(
			ctx,
			store,
			now,
			entries,
			legacyEntries,
			forceRerun,
			fallbackEntries...,
		)
	}
	cached, err := loadPassedWorkloadCacheLevelWithSQLite(
		ctx,
		store,
		ledgerStore,
		now,
		entries,
		forceRerun,
	)
	if err != nil || forceRerun {
		return cached, err
	}
	fallbacks := append([][]remoteWorkloadCacheEntry{legacyEntries}, fallbackEntries...)
	for _, fallback := range fallbacks {
		misses := remoteWorkloadCacheMissEntries(fallback, cached)
		fallbackCached, err := loadPassedWorkloadCacheLevelWithSQLite(
			ctx,
			store,
			ledgerStore,
			now,
			misses,
			false,
		)
		if err != nil {
			return nil, err
		}
		// 兼容证据只迁移到 SQLite；没有当前 receipt 时不得发布新的 OSS marker。
		maps.Copy(cached, fallbackCached)
		if err := recordPassedWorkloadCacheProofs(
			ledgerStore,
			entries,
			fallbackCached,
			now().UTC(),
		); err != nil {
			return nil, err
		}
	}
	return cached, nil
}

func loadPassedWorkloadCacheLevelWithSQLite(
	ctx context.Context,
	store ObjectStore,
	ledgerStore *gate.DurationLedgerStore,
	now func() time.Time,
	entries []remoteWorkloadCacheEntry,
	forceRerun bool,
) (map[string]gate.PlanGateExecution, error) {
	cached, err := lookupPassedWorkloadCacheProofsWithSQLite(
		ledgerStore,
		now,
		entries,
		forceRerun,
	)
	if err != nil || forceRerun {
		return cached, err
	}
	misses := remoteWorkloadCacheMissEntries(entries, cached)
	remoteCached, err := loadPassedWorkloadCache(ctx, store, now, misses, false)
	if err != nil {
		return nil, err
	}
	if err := recordPassedWorkloadCacheProofs(
		ledgerStore,
		misses,
		remoteCached,
		now().UTC(),
	); err != nil {
		return nil, err
	}
	maps.Copy(cached, remoteCached)
	return cached, nil
}

// lookupPassedWorkloadCacheProofsWithSQLite 仅按当前身份主键查询共享账本，不访问对象存储。
func lookupPassedWorkloadCacheProofsWithSQLite(
	ledgerStore *gate.DurationLedgerStore,
	now func() time.Time,
	entries []remoteWorkloadCacheEntry,
	forceRerun bool,
) (map[string]gate.PlanGateExecution, error) {
	cached := make(map[string]gate.PlanGateExecution)
	if forceRerun || len(entries) == 0 {
		return cached, nil
	}
	if ledgerStore == nil {
		return nil, errors.New("query SQLite PASS proofs: ledger store is required")
	}
	identityDigests := make([]string, len(entries))
	for index, entry := range entries {
		identityDigests[index] = entry.identityDigest
	}
	proofs, err := ledgerStore.LookupWorkloadPassProofs(identityDigests)
	if err != nil {
		return nil, fmt.Errorf("query SQLite PASS proofs: %w", err)
	}
	matched := make([]bool, len(entries))
	for index, entry := range entries {
		proof, ok := proofs[entry.identityDigest]
		if ok && !validRemoteWorkloadPassProof(entry, proof) {
			return nil, fmt.Errorf(
				"SQLite PASS proof %q conflicts with expected workload identity",
				entry.identityDigest,
			)
		}
		if ok {
			matched[index] = true
			continue
		}
	}
	cached = projectPassedWorkloadCache(now().UTC(), entries, matched)
	return cached, nil
}

// validRemoteWorkloadPassProof rejects stale or corrupt SQLite projections before cache reuse.
func validRemoteWorkloadPassProof(entry remoteWorkloadCacheEntry, proof gate.WorkloadPassProof) bool {
	return proof.IdentityDigest == entry.identityDigest &&
		proof.ExecutionDigest == entry.executionDigest &&
		proof.InputDigest == entry.inputDigest &&
		proof.EnvironmentDigest == entry.environmentDigest &&
		proof.ObjectKey == entry.key &&
		remoteWorkloadCacheIdentityDigest(entry.environmentDigest, entry.executionDigest, entry.inputDigest) == entry.identityDigest
}

func recordPassedWorkloadCacheProofs(
	store *gate.DurationLedgerStore,
	entries []remoteWorkloadCacheEntry,
	cached map[string]gate.PlanGateExecution,
	observedAt time.Time,
) error {
	if store == nil || len(cached) == 0 {
		return nil
	}
	proofs := make([]gate.WorkloadPassProof, 0, len(cached))
	for _, entry := range entries {
		if _, ok := cached[entry.workloadID]; !ok {
			continue
		}
		proofs = append(proofs, gate.WorkloadPassProof{
			IdentityDigest:    entry.identityDigest,
			WorkloadID:        entry.workloadID,
			ExecutionDigest:   entry.executionDigest,
			InputDigest:       entry.inputDigest,
			EnvironmentDigest: entry.environmentDigest,
			ObjectKey:         entry.key,
			ObservedAt:        observedAt,
		})
	}
	return store.RecordWorkloadPassProofs(proofs)
}

func recordRemoteWorkloadFingerprints(
	store *gate.DurationLedgerStore,
	entries []remoteWorkloadCacheEntry,
	sourceTree string,
	observedAt time.Time,
) error {
	if store == nil || len(entries) == 0 {
		return nil
	}
	records := make([]gate.WorkloadFingerprintRecord, len(entries))
	for index, entry := range entries {
		records[index] = gate.WorkloadFingerprintRecord{
			IdentityDigest:    entry.identityDigest,
			WorkloadID:        entry.workloadID,
			ExecutionDigest:   entry.executionDigest,
			InputDigest:       entry.inputDigest,
			EnvironmentDigest: entry.environmentDigest,
			SourceTreeSHA:     sourceTree,
			ObservedAt:        observedAt,
		}
	}
	return store.RecordWorkloadFingerprints(records)
}
