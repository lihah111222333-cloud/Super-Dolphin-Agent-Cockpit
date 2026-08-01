package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WorkloadCatalogObservation 绑定一次目录发现与精确 Git tree 和 CI 入口。
type WorkloadCatalogObservation struct {
	SourceTreeSHA string
	Entrypoint    CIEntrypointID
	Profile       Profile
	ObservedAt    time.Time
}

// WorkloadCatalogRecord 是内容寻址目录及其历史观测的 SQLite 查询模型。
type WorkloadCatalogRecord struct {
	CatalogDigest string
	Catalog       WorkloadCatalog
	Observations  []WorkloadCatalogObservation
}

// RecordWorkloadCatalog 按内容摘要保存不可变目录，并追加当前 tree 的观测。
func (store *DurationLedgerStore) RecordWorkloadCatalog(
	catalog WorkloadCatalog,
	observation WorkloadCatalogObservation,
) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	digest, err := validateWorkloadCatalogObservation(catalog, observation)
	if err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "workload catalog", func(transaction *sql.Tx) error {
		inserted, err := insertSQLiteWorkloadCatalog(transaction, digest, catalog, observation.ObservedAt)
		if err != nil {
			return err
		}
		if inserted {
			if err := insertSQLiteCatalogWorkloads(transaction, digest, catalog.Workloads); err != nil {
				return err
			}
		} else if err := verifySQLiteWorkloadCatalog(transaction, digest, catalog); err != nil {
			return err
		}
		if _, err := transaction.Exec(`
		INSERT INTO ci_catalog_observations (
			catalog_digest, source_tree_sha, entrypoint, profile, observed_at_unix_ms
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(catalog_digest, source_tree_sha, entrypoint, profile) DO UPDATE SET
			observed_at_unix_ms = MAX(
				ci_catalog_observations.observed_at_unix_ms,
				excluded.observed_at_unix_ms
			)
	`,
			digest,
			observation.SourceTreeSHA,
			string(observation.Entrypoint),
			string(observation.Profile),
			observation.ObservedAt.UTC().UnixMilli(),
		); err != nil {
			return mapDurationLedgerSQLiteError("store workload catalog observation", err)
		}
		return advanceCIQueryRevision(transaction, observation.ObservedAt.UTC())
	})
}

// LoadWorkloadCatalogRecord 按内容摘要恢复目录和全部观测。
func (store *DurationLedgerStore) LoadWorkloadCatalogRecord(
	catalogDigest string,
) (WorkloadCatalogRecord, error) {
	if store == nil {
		return WorkloadCatalogRecord{}, errors.New("duration ledger store is nil")
	}
	if !isPrefixedSHA256Digest(catalogDigest) {
		return WorkloadCatalogRecord{}, errors.New("workload catalog digest is invalid")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return WorkloadCatalogRecord{}, err
	}
	defer database.Close()
	record, err := loadSQLiteWorkloadCatalog(database, catalogDigest)
	if err != nil {
		return WorkloadCatalogRecord{}, err
	}
	record.Observations, err = loadSQLiteCatalogObservations(database, catalogDigest)
	if err != nil {
		return WorkloadCatalogRecord{}, err
	}
	return record, nil
}

func validateWorkloadCatalogObservation(
	catalog WorkloadCatalog,
	observation WorkloadCatalogObservation,
) (string, error) {
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		return "", err
	}
	if !validCalibrationOID(observation.SourceTreeSHA) {
		return "", errors.New("workload catalog source tree is invalid")
	}
	if observation.Entrypoint == "" || observation.Profile == "" {
		return "", errors.New("workload catalog entrypoint and profile are required")
	}
	if observation.ObservedAt.IsZero() {
		return "", errors.New("workload catalog observation time is required")
	}
	return digest, nil
}

func insertSQLiteWorkloadCatalog(
	transaction *sql.Tx,
	digest string,
	catalog WorkloadCatalog,
	observedAt time.Time,
) (bool, error) {
	result, err := transaction.Exec(`
		INSERT INTO ci_workload_catalogs (
			catalog_digest, catalog_version, authoritative, workload_count, created_at_unix_ms
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(catalog_digest) DO NOTHING
	`,
		digest,
		catalog.Version,
		boolToSQLite(catalog.Authoritative),
		len(catalog.Workloads),
		observedAt.UTC().UnixMilli(),
	)
	if err != nil {
		return false, mapDurationLedgerSQLiteError("store workload catalog", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read workload catalog insert count: %w", err)
	}
	return affected == 1, nil
}

func insertSQLiteCatalogWorkloads(
	transaction *sql.Tx,
	catalogDigest string,
	workloads []Workload,
) error {
	statement, err := transaction.Prepare(`
		INSERT INTO ci_catalog_workloads (
			catalog_digest, ordinal, workload_id, kind, command_digest,
			bootstrap_estimate_ms, shardable, gate_id, target_kind, target_value
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return mapDurationLedgerSQLiteError("prepare workload catalog target insert", err)
	}
	for ordinal, workload := range workloads {
		gateID, targetKind, targetValue, _, err := ParseWorkloadID(workload.ID)
		if err != nil {
			return errors.Join(
				err,
				closeSQLiteStatement(statement, "close workload catalog target insert"),
			)
		}
		if _, err := statement.Exec(
			catalogDigest,
			ordinal,
			workload.ID,
			string(workload.Kind),
			workload.CommandDigest,
			workload.BootstrapEstimateMS,
			boolToSQLite(workload.Shardable),
			string(gateID),
			string(targetKind),
			targetValue,
		); err != nil {
			return errors.Join(
				mapDurationLedgerSQLiteError("store workload catalog target", err),
				closeSQLiteStatement(statement, "close workload catalog target insert"),
			)
		}
	}
	return closeSQLiteStatement(statement, "close workload catalog target insert")
}

func verifySQLiteWorkloadCatalog(
	transaction *sql.Tx,
	catalogDigest string,
	expected WorkloadCatalog,
) error {
	actual, err := loadSQLiteWorkloadCatalog(transaction, catalogDigest)
	if err != nil {
		return err
	}
	actualDigest, err := WorkloadCatalogDigest(actual.Catalog)
	if err != nil {
		return err
	}
	if actualDigest != catalogDigest {
		return errors.New("stored workload catalog content does not match its digest")
	}
	expectedDigest, err := WorkloadCatalogDigest(expected)
	if err != nil {
		return err
	}
	if expectedDigest != actualDigest {
		return errors.New("workload catalog digest collision")
	}
	return nil
}

type sqliteRowQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

func loadSQLiteWorkloadCatalog(
	queryer sqliteRowQueryer,
	catalogDigest string,
) (WorkloadCatalogRecord, error) {
	var (
		record        WorkloadCatalogRecord
		authoritative int
		workloadCount int
	)
	record.CatalogDigest = catalogDigest
	err := queryer.QueryRow(`
		SELECT catalog_version, authoritative, workload_count
		FROM ci_workload_catalogs
		WHERE catalog_digest = ?
	`, catalogDigest).Scan(
		&record.Catalog.Version,
		&authoritative,
		&workloadCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkloadCatalogRecord{}, fmt.Errorf("workload catalog %q is not found", catalogDigest)
	}
	if err != nil {
		return WorkloadCatalogRecord{}, mapDurationLedgerSQLiteError("load workload catalog", err)
	}
	record.Catalog.Authoritative = authoritative == 1
	rows, err := queryer.Query(`
		SELECT workload_id, kind, command_digest, bootstrap_estimate_ms, shardable
		FROM ci_catalog_workloads
		WHERE catalog_digest = ?
		ORDER BY ordinal
	`, catalogDigest)
	if err != nil {
		return WorkloadCatalogRecord{}, mapDurationLedgerSQLiteError("query workload catalog targets", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			workload  Workload
			kind      string
			shardable int
		)
		if err := rows.Scan(
			&workload.ID,
			&kind,
			&workload.CommandDigest,
			&workload.BootstrapEstimateMS,
			&shardable,
		); err != nil {
			return WorkloadCatalogRecord{}, mapDurationLedgerSQLiteError("scan workload catalog target", err)
		}
		workload.Kind = WorkloadKind(kind)
		workload.Shardable = shardable == 1
		record.Catalog.Workloads = append(record.Catalog.Workloads, workload)
	}
	if err := rows.Err(); err != nil {
		return WorkloadCatalogRecord{}, mapDurationLedgerSQLiteError("iterate workload catalog targets", err)
	}
	if len(record.Catalog.Workloads) != workloadCount {
		return WorkloadCatalogRecord{}, errors.New("workload catalog target count is inconsistent")
	}
	if err := ValidateWorkloadCatalog(record.Catalog); err != nil {
		return WorkloadCatalogRecord{}, fmt.Errorf("validate stored workload catalog: %w", err)
	}
	return record, nil
}

func loadSQLiteCatalogObservations(
	database *sql.DB,
	catalogDigest string,
) ([]WorkloadCatalogObservation, error) {
	rows, err := database.Query(`
		SELECT source_tree_sha, entrypoint, profile, observed_at_unix_ms
		FROM ci_catalog_observations
		WHERE catalog_digest = ?
		ORDER BY observed_at_unix_ms DESC, source_tree_sha, entrypoint, profile
	`, catalogDigest)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query workload catalog observations", err)
	}
	defer rows.Close()
	var observations []WorkloadCatalogObservation
	for rows.Next() {
		var (
			observation         WorkloadCatalogObservation
			entrypoint, profile string
			observedAtMS        int64
		)
		if err := rows.Scan(
			&observation.SourceTreeSHA,
			&entrypoint,
			&profile,
			&observedAtMS,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan workload catalog observation", err)
		}
		observation.Entrypoint = CIEntrypointID(entrypoint)
		observation.Profile = Profile(profile)
		observation.ObservedAt = time.UnixMilli(observedAtMS).UTC()
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate workload catalog observations", err)
	}
	return observations, nil
}
