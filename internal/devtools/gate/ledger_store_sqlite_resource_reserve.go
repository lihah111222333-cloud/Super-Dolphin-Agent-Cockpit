package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func resolveSQLitePlanningContext(transaction *sql.Tx, generation uint64, planning PlanningContext, ledger DurationLedger) (PlanningContext, []cicontract.WorkloadResourceTierReserve, error) {
	reserves, err := loadSQLiteNormalResourceTierReserves(transaction, generation, planning, ledger.ShardOverhead)
	if err != nil {
		return PlanningContext{}, nil, err
	}
	planning.ResourceTierReserves = append([]cicontract.WorkloadResourceTierReserve(nil), reserves...)
	planning, err = ResolvePlanningContext(planning, ledger)
	return planning, reserves, err
}

// loadSQLiteNormalResourceTierReserves derives normal tier reserves from the
// existing immutable run/shard/timing projections. No new table or migration
// is introduced; missing or unverifiable tier evidence fails closed.
func loadSQLiteNormalResourceTierReserves(transaction *sql.Tx, generation uint64, planning PlanningContext, overhead *ShardOrchestrationOverhead) ([]cicontract.WorkloadResourceTierReserve, error) {
	if planning.Calibration {
		return nil, nil
	}
	if overhead == nil {
		return nil, errors.New("normal resource-tier reserve requires accepted shard overhead identity")
	}
	runs, err := loadSQLiteNormalReserveRuns(transaction, generation, planning.AcceptedSnapshotID)
	if err != nil {
		return nil, err
	}
	observations, shards, err := loadSQLiteNormalReserveEvidence(transaction, runs)
	if err != nil {
		return nil, err
	}
	reserves, err := DeriveNormalResourceTierReserves(observations, shards, overhead.CalibrationResourceClassID)
	if err != nil {
		return nil, err
	}
	return reserves, nil
}

type normalReserveRun struct{ jobID string }

func loadSQLiteNormalReserveRuns(transaction *sql.Tx, generation uint64, snapshotID string) ([]normalReserveRun, error) {
	rows, err := transaction.Query(`
		SELECT job_id, accepted_generation, image_cache_snapshot_id
		FROM ci_runs
		WHERE authoritative = 1 AND status = 'passed' AND cleanup_complete = 1
		ORDER BY accepted_generation, job_id
	`)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query normal resource-tier reserve runs", err)
	}
	defer rows.Close()
	minGeneration := uint64(1)
	if generation > 2 {
		minGeneration = generation - 2
	}
	runs := make([]normalReserveRun, 0)
	for rows.Next() {
		run, selected, err := scanSQLiteNormalReserveRun(rows, generation, minGeneration, snapshotID)
		if err != nil {
			return nil, err
		}
		if !selected {
			continue
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate normal resource-tier reserve runs", err)
	}
	return runs, nil
}

func scanSQLiteNormalReserveRun(rows *sql.Rows, generation, minGeneration uint64, acceptedSnapshotID string) (normalReserveRun, bool, error) {
	var run normalReserveRun
	var generationText, snapshotID string
	if err := rows.Scan(&run.jobID, &generationText, &snapshotID); err != nil {
		return normalReserveRun{}, false, mapDurationLedgerSQLiteError("scan normal resource-tier reserve run", err)
	}
	runGeneration, err := strconv.ParseUint(generationText, 10, 64)
	if err != nil || runGeneration == 0 {
		return normalReserveRun{}, false, errors.New("normal resource-tier reserve run generation is invalid")
	}
	if runGeneration > generation {
		return normalReserveRun{}, false, fmt.Errorf("normal resource-tier reserve run generation %d is in the future", runGeneration)
	}
	return run, runGeneration >= minGeneration && snapshotID == acceptedSnapshotID, nil
}

func loadSQLiteNormalReserveEvidence(transaction *sql.Tx, runs []normalReserveRun) ([]TimingObservation, []RemoteCIShardRecord, error) {
	allObservations := make([]TimingObservation, 0)
	allShards := make([]RemoteCIShardRecord, 0)
	for _, run := range runs {
		shards, err := loadRemoteCIShardRows(transaction, run.jobID, ResultStatusPassed, true)
		if err != nil {
			return nil, nil, fmt.Errorf("load normal resource-tier reserve run %q shards: %w", run.jobID, err)
		}
		observations, err := loadTimingObservations(transaction, run.jobID)
		if err != nil {
			return nil, nil, fmt.Errorf("load normal resource-tier reserve run %q timing: %w", run.jobID, err)
		}
		allShards = append(allShards, shards...)
		allObservations = append(allObservations, observations...)
	}
	return allObservations, allShards, nil
}
