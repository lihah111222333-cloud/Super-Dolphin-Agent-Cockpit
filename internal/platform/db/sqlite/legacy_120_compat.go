package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	terminalOutcomeOutboxMigration = "120_terminal_outcome_outbox.sql"
	legacyManagedGeneration120     = "120_mcp_managed_generations.sql"
	legacyProviderRecovery120      = "120_agent_provider_binding_recovery_owner.sql"
	canonicalManagedGeneration122  = "122_mcp_managed_generations.sql"
	canonicalProviderRecovery123   = "123_agent_provider_binding_recovery_owner.sql"
)

type legacy120MarkerSpec struct {
	legacyFilename string
	targetFilename string
	targetVersion  int
}

type storedMigrationMarker struct {
	version   int
	name      string
	filename  string
	appliedAt int64
}

func isLegacy120CanonicalTarget(name string) bool {
	return name == canonicalManagedGeneration122 || name == canonicalProviderRecovery123
}

func skipRecordedCanonicalTarget(
	ctx context.Context,
	tx *sql.Tx,
	dir string,
	name string,
) (bool, error) {
	spec, err := canonicalTargetSpec(name)
	if err != nil {
		return false, err
	}
	exists, err := canonicalTargetMarkerExists(ctx, tx, spec)
	if err != nil || !exists {
		return false, err
	}
	body, err := readCompatibilityMigrationBody(dir, name)
	if err != nil {
		return false, err
	}
	if err := validateLegacy120State(ctx, tx, spec, body); err != nil {
		return false, fmt.Errorf("validate recorded canonical migration %s: %w", name, err)
	}
	return true, nil
}

func canonicalTargetSpec(name string) (legacy120MarkerSpec, error) {
	switch name {
	case canonicalManagedGeneration122:
		return legacy120MarkerSpec{
			legacyFilename: legacyManagedGeneration120,
			targetFilename: canonicalManagedGeneration122,
			targetVersion:  122,
		}, nil
	case canonicalProviderRecovery123:
		return legacy120MarkerSpec{
			legacyFilename: legacyProviderRecovery120,
			targetFilename: canonicalProviderRecovery123,
			targetVersion:  123,
		}, nil
	default:
		return legacy120MarkerSpec{}, fmt.Errorf("unsupported canonical legacy migration target %q", name)
	}
}

func reconcileLegacy120Markers(ctx context.Context, tx *sql.Tx, dir string) error {
	legacy, err := migrationMarkerByVersion(ctx, tx, 120)
	if err != nil {
		return err
	}
	if legacy == nil {
		return rejectDetachedLegacy120Markers(ctx, tx)
	}
	spec, err := legacy120Spec(*legacy)
	if err != nil {
		return err
	}
	if err := requireOnlySelectedLegacy120Marker(ctx, tx, spec, *legacy); err != nil {
		return err
	}
	targetBody, err := readCompatibilityMigrationBody(dir, spec.targetFilename)
	if err != nil {
		return err
	}
	if err := validateLegacy120State(ctx, tx, spec, targetBody); err != nil {
		return err
	}
	targetExists, err := canonicalTargetMarkerExists(ctx, tx, spec)
	if err != nil {
		return err
	}
	return replaceOrReleaseLegacy120Marker(ctx, tx, spec, *legacy, targetExists)
}

func legacy120Spec(marker storedMigrationMarker) (legacy120MarkerSpec, error) {
	specs := []legacy120MarkerSpec{
		{legacyManagedGeneration120, canonicalManagedGeneration122, 122},
		{legacyProviderRecovery120, canonicalProviderRecovery123, 123},
	}
	for _, spec := range specs {
		if marker.filename != spec.legacyFilename {
			continue
		}
		if marker.name != strings.TrimSuffix(spec.legacyFilename, ".sql") {
			return legacy120MarkerSpec{}, fmt.Errorf("legacy migration marker %s has forged name %q", marker.filename, marker.name)
		}
		return spec, nil
	}
	return legacy120MarkerSpec{}, fmt.Errorf(
		"schema migration version 120 is owned by unsupported marker %q",
		marker.filename,
	)
}

func rejectDetachedLegacy120Markers(ctx context.Context, tx *sql.Tx) error {
	count, err := countMarkersByFilenames(ctx, tx, legacyManagedGeneration120, legacyProviderRecovery120)
	if err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("legacy migration 120 marker exists without owning schema version 120")
	}
	return nil
}

func requireOnlySelectedLegacy120Marker(
	ctx context.Context,
	tx *sql.Tx,
	spec legacy120MarkerSpec,
	legacy storedMigrationMarker,
) error {
	count, err := countMarkersByFilenames(ctx, tx, legacyManagedGeneration120, legacyProviderRecovery120)
	if err != nil {
		return err
	}
	if count != 1 || legacy.version != 120 || legacy.filename != spec.legacyFilename {
		return fmt.Errorf("legacy migration 120 marker set is ambiguous")
	}
	return nil
}

func readCompatibilityMigrationBody(dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf(
			"read compatibility migration %s from %s: %s",
			name,
			redactPath(dir),
			securefs.SafeErrorForPath(err, path),
		)
	}
	return string(body), nil
}

func validateLegacy120State(
	ctx context.Context,
	tx *sql.Tx,
	spec legacy120MarkerSpec,
	targetBody string,
) error {
	switch spec.legacyFilename {
	case legacyManagedGeneration120:
		return validateManagedGenerationMigrationState(ctx, tx, targetBody)
	case legacyProviderRecovery120:
		return validateProviderBindingRecoveryState(ctx, tx)
	default:
		return fmt.Errorf("unsupported legacy migration marker %q", spec.legacyFilename)
	}
}

func canonicalTargetMarkerExists(
	ctx context.Context,
	tx *sql.Tx,
	spec legacy120MarkerSpec,
) (bool, error) {
	byFilename, err := migrationMarkersByFilename(ctx, tx, spec.targetFilename)
	if err != nil {
		return false, err
	}
	byVersion, err := migrationMarkerByVersion(ctx, tx, spec.targetVersion)
	if err != nil {
		return false, err
	}
	if len(byFilename) == 0 && byVersion == nil {
		return false, nil
	}
	if len(byFilename) != 1 || byVersion == nil {
		return false, fmt.Errorf("canonical migration target %s has a marker collision", spec.targetFilename)
	}
	if !isExactCanonicalTargetMarker(spec, byFilename[0], *byVersion) {
		return false, fmt.Errorf("canonical migration target %s marker identity is invalid", spec.targetFilename)
	}
	return true, nil
}

func isExactCanonicalTargetMarker(
	spec legacy120MarkerSpec,
	byFilename storedMigrationMarker,
	byVersion storedMigrationMarker,
) bool {
	wantName := strings.TrimSuffix(spec.targetFilename, ".sql")
	return byFilename.version == spec.targetVersion &&
		byFilename.name == wantName &&
		byVersion.filename == spec.targetFilename &&
		byVersion.name == wantName
}

func replaceOrReleaseLegacy120Marker(
	ctx context.Context,
	tx *sql.Tx,
	spec legacy120MarkerSpec,
	legacy storedMigrationMarker,
	targetExists bool,
) error {
	var (
		result sql.Result
		err    error
	)
	if targetExists {
		result, err = tx.ExecContext(
			ctx,
			"DELETE FROM schema_migrations WHERE version = ? AND name = ? AND filename = ? AND applied_at = ?",
			legacy.version, legacy.name, legacy.filename, legacy.appliedAt,
		)
	} else {
		result, err = tx.ExecContext(
			ctx,
			`UPDATE schema_migrations
			 SET version = ?, name = ?, filename = ?
			 WHERE version = ? AND name = ? AND filename = ? AND applied_at = ?`,
			spec.targetVersion,
			strings.TrimSuffix(spec.targetFilename, ".sql"),
			spec.targetFilename,
			legacy.version,
			legacy.name,
			legacy.filename,
			legacy.appliedAt,
		)
	}
	if err != nil {
		return fmt.Errorf("convert legacy migration marker %s: %w", legacy.filename, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count converted legacy migration marker %s: %w", legacy.filename, err)
	}
	if affected != 1 {
		return fmt.Errorf("legacy migration marker %s changed concurrently", legacy.filename)
	}
	return nil
}

func migrationMarkerByVersion(ctx context.Context, tx *sql.Tx, version int) (*storedMigrationMarker, error) {
	var marker storedMigrationMarker
	err := tx.QueryRowContext(
		ctx,
		"SELECT version, name, filename, applied_at FROM schema_migrations WHERE version = ?",
		version,
	).Scan(&marker.version, &marker.name, &marker.filename, &marker.appliedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read schema migration version %d: %w", version, err)
	}
	return &marker, nil
}

func migrationMarkersByFilename(
	ctx context.Context,
	tx *sql.Tx,
	filename string,
) ([]storedMigrationMarker, error) {
	rows, err := tx.QueryContext(
		ctx,
		"SELECT version, name, filename, applied_at FROM schema_migrations WHERE filename = ?",
		filename,
	)
	if err != nil {
		return nil, fmt.Errorf("read schema migration marker %s: %w", filename, err)
	}
	defer rows.Close()
	var markers []storedMigrationMarker
	for rows.Next() {
		var marker storedMigrationMarker
		if err := rows.Scan(&marker.version, &marker.name, &marker.filename, &marker.appliedAt); err != nil {
			return nil, fmt.Errorf("scan schema migration marker %s: %w", filename, err)
		}
		markers = append(markers, marker)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema migration marker %s: %w", filename, err)
	}
	return markers, nil
}

func countMarkersByFilenames(ctx context.Context, tx *sql.Tx, filenames ...string) (int, error) {
	if len(filenames) != 2 {
		return 0, fmt.Errorf("legacy migration filename set must contain exactly two entries")
	}
	var count int
	if err := tx.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE filename IN (?, ?)",
		filenames[0],
		filenames[1],
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count legacy migration 120 markers: %w", err)
	}
	return count, nil
}
