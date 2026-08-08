package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ErrWorkloadCatalogNotFound 表示 SQLite 中不存在请求的 workload catalog。
var ErrWorkloadCatalogNotFound = errors.New("workload catalog not found")

// ErrWorkloadCatalogObservationNotFound 表示精确 observation identity 没有目录投影。
var ErrWorkloadCatalogObservationNotFound = errors.New("workload catalog observation not found")

// WorkloadCatalogObservation 绑定一次目录发现与精确 Git tree 和 CI 入口。
type WorkloadCatalogObservation struct {
	SourceTreeSHA      string
	Entrypoint         CIEntrypointID
	Profile            Profile
	AcceptedGeneration uint64
	ObservedAt         time.Time
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
		if err := requireHistoricallyAcceptedGeneration(transaction, observation.AcceptedGeneration); err != nil {
			return err
		}
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
			catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation, observed_at_unix_ms
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation) DO UPDATE SET
			observed_at_unix_ms = MAX(
				ci_catalog_observations.observed_at_unix_ms,
				excluded.observed_at_unix_ms
			)
	`,
			digest,
			observation.SourceTreeSHA,
			string(observation.Entrypoint),
			string(observation.Profile),
			strconv.FormatUint(observation.AcceptedGeneration, 10),
			observation.ObservedAt.UTC().UnixMilli(),
		); err != nil {
			return mapDurationLedgerSQLiteError("store workload catalog observation", err)
		}
		if err := advanceCIQueryRevision(transaction, observation.ObservedAt.UTC()); err != nil {
			return err
		}
		return compactDurationLedgerAuthority(transaction)
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

// LoadWorkloadCatalogRecordByObservationIdentity 按 source tree、入口、profile
// 和 accepted generation 唯一回读 workload catalog。没有观测返回
// ErrWorkloadCatalogObservationNotFound；同一 identity 关联多个不同 catalog
// 时直接失败，禁止按时间或插入顺序选择一个结果。
func (store *DurationLedgerStore) LoadWorkloadCatalogRecordByObservationIdentity(
	sourceTreeSHA string,
	entrypoint CIEntrypointID,
	profile Profile,
	acceptedGeneration uint64,
) (WorkloadCatalogRecord, error) {
	if store == nil {
		return WorkloadCatalogRecord{}, errors.New("duration ledger store is nil")
	}
	if err := validateWorkloadCatalogObservationIdentity(sourceTreeSHA, entrypoint, profile, acceptedGeneration); err != nil {
		return WorkloadCatalogRecord{}, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return WorkloadCatalogRecord{}, err
	}
	defer database.Close()
	return loadSQLiteWorkloadCatalogRecordByObservationIdentity(database, sourceTreeSHA, entrypoint, profile, acceptedGeneration)
}

// LoadWorkloadCatalogRecordForObservation 是按完整 observation 载荷回读目录的便捷入口；
// ObservedAt 不属于 catalog identity，因此会被有意忽略。
func (store *DurationLedgerStore) LoadWorkloadCatalogRecordForObservation(
	observation WorkloadCatalogObservation,
) (WorkloadCatalogRecord, error) {
	return store.LoadWorkloadCatalogRecordByObservationIdentity(
		observation.SourceTreeSHA,
		observation.Entrypoint,
		observation.Profile,
		observation.AcceptedGeneration,
	)
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
	if observation.Entrypoint == "" || observation.Profile == "" || observation.AcceptedGeneration == 0 {
		return "", errors.New("workload catalog entrypoint and profile are required")
	}
	if observation.ObservedAt.IsZero() {
		return "", errors.New("workload catalog observation time is required")
	}
	return digest, nil
}

func validateWorkloadCatalogObservationIdentity(
	sourceTreeSHA string,
	entrypoint CIEntrypointID,
	profile Profile,
	acceptedGeneration uint64,
) error {
	if !validCalibrationOID(sourceTreeSHA) {
		return errors.New("workload catalog source tree is invalid")
	}
	if entrypoint == "" || profile == "" || acceptedGeneration == 0 {
		return errors.New("workload catalog observation identity requires entrypoint, profile, and accepted generation")
	}
	return nil
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
			input_digest, bootstrap_estimate_ms, shardable, gate_id, target_kind, target_value
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			workload.InputDigest,
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
		return WorkloadCatalogRecord{}, fmt.Errorf("%w: %s", ErrWorkloadCatalogNotFound, catalogDigest)
	}
	if err != nil {
		return WorkloadCatalogRecord{}, mapDurationLedgerSQLiteError("load workload catalog", err)
	}
	record.Catalog.Authoritative = authoritative == 1
	rows, err := queryer.Query(`
		SELECT workload_id, kind, command_digest, input_digest, bootstrap_estimate_ms, shardable
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
			&workload.InputDigest,
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
	actualDigest, err := WorkloadCatalogDigest(record.Catalog)
	if err != nil {
		return WorkloadCatalogRecord{}, fmt.Errorf("digest stored workload catalog: %w", err)
	}
	if actualDigest != catalogDigest {
		return WorkloadCatalogRecord{}, errors.New("stored workload catalog content does not match its digest")
	}
	return record, nil
}

const workloadCatalogObservationIdentityQuery = `
	SELECT DISTINCT catalog_digest
	FROM ci_catalog_observations
	WHERE source_tree_sha = ?
		AND entrypoint = ?
		AND profile = ?
		AND accepted_generation = ?
	LIMIT 2
`

func loadSQLiteWorkloadCatalogRecordByObservationIdentity(
	queryer sqliteRowQueryer,
	sourceTreeSHA string,
	entrypoint CIEntrypointID,
	profile Profile,
	acceptedGeneration uint64,
) (WorkloadCatalogRecord, error) {
	if err := validateWorkloadCatalogObservationIdentity(sourceTreeSHA, entrypoint, profile, acceptedGeneration); err != nil {
		return WorkloadCatalogRecord{}, err
	}
	rows, err := queryer.Query(
		workloadCatalogObservationIdentityQuery,
		sourceTreeSHA,
		string(entrypoint),
		string(profile),
		strconv.FormatUint(acceptedGeneration, 10),
	)
	if err != nil {
		return WorkloadCatalogRecord{}, mapDurationLedgerSQLiteError("query workload catalog observation identity", err)
	}
	defer rows.Close()
	var catalogDigests []string
	for rows.Next() {
		var catalogDigest string
		if err := rows.Scan(&catalogDigest); err != nil {
			return WorkloadCatalogRecord{}, mapDurationLedgerSQLiteError("scan workload catalog observation identity", err)
		}
		catalogDigests = append(catalogDigests, catalogDigest)
	}
	if err := rows.Err(); err != nil {
		return WorkloadCatalogRecord{}, mapDurationLedgerSQLiteError("iterate workload catalog observation identity", err)
	}
	switch len(catalogDigests) {
	case 0:
		return WorkloadCatalogRecord{}, fmt.Errorf(
			"%w: source_tree_sha=%s entrypoint=%s profile=%s accepted_generation=%d",
			ErrWorkloadCatalogObservationNotFound,
			sourceTreeSHA,
			entrypoint,
			profile,
			acceptedGeneration,
		)
	case 1:
		record, err := loadSQLiteWorkloadCatalog(queryer, catalogDigests[0])
		if err != nil {
			return WorkloadCatalogRecord{}, err
		}
		record.Observations, err = loadSQLiteCatalogObservations(queryer, catalogDigests[0])
		if err != nil {
			return WorkloadCatalogRecord{}, err
		}
		return record, nil
	default:
		return WorkloadCatalogRecord{}, fmt.Errorf(
			"workload catalog observation identity maps to multiple catalogs: %s, %s",
			catalogDigests[0], catalogDigests[1],
		)
	}
}

func loadSQLiteCatalogObservations(
	queryer sqliteRowQueryer,
	catalogDigest string,
) ([]WorkloadCatalogObservation, error) {
	rows, err := queryer.Query(`
		SELECT source_tree_sha, entrypoint, profile, accepted_generation, observed_at_unix_ms
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
			acceptedGeneration  string
			observedAtMS        int64
		)
		if err := rows.Scan(
			&observation.SourceTreeSHA,
			&entrypoint,
			&profile,
			&acceptedGeneration,
			&observedAtMS,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan workload catalog observation", err)
		}
		observation.Entrypoint = CIEntrypointID(entrypoint)
		observation.Profile = Profile(profile)
		if observation.AcceptedGeneration, err = strconv.ParseUint(acceptedGeneration, 10, 64); err != nil || observation.AcceptedGeneration == 0 {
			return nil, fmt.Errorf("stored workload catalog accepted generation is invalid")
		}
		observation.ObservedAt = time.UnixMilli(observedAtMS).UTC()
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate workload catalog observations", err)
	}
	return observations, nil
}
