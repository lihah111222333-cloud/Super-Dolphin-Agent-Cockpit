package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func runAgentProviderBindingRecoveryOwnerMigration(ctx context.Context, tx *sql.Tx) error {
	columns, err := sqliteTableColumns(ctx, tx, "agent_provider_binding")
	if err != nil {
		return err
	}
	if err := requireProviderBindingRecoveryColumns(columns); err != nil {
		return err
	}
	if err := ensureProviderRecoveryHomeColumn(ctx, tx, columns); err != nil {
		return err
	}
	rows, err := loadProviderBindingUUIDRows(ctx, tx)
	if err != nil {
		return err
	}
	if err := validateProviderBindingUUIDOwnership(rows); err != nil {
		return err
	}
	return backfillProviderBindingRecoveryOwner(ctx, tx, rows)
}

func requireProviderBindingRecoveryColumns(columns map[string]bool) error {
	for _, column := range []string{
		"agent_id", "provider", "provider_thread_id", "session_uuid", "codex_home",
		"codex_instance_key", "codex_model_provider",
	} {
		if !columns[column] {
			return fmt.Errorf("agent_provider_binding missing required column %s", column)
		}
	}
	return nil
}

func ensureProviderRecoveryHomeColumn(ctx context.Context, tx *sql.Tx, columns map[string]bool) error {
	if columns["provider_recovery_home"] {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE agent_provider_binding
		ADD COLUMN provider_recovery_home TEXT NOT NULL DEFAULT ''
	`); err != nil {
		return fmt.Errorf("add agent_provider_binding provider_recovery_home: %w", err)
	}
	return nil
}

func backfillProviderBindingRecoveryOwner(ctx context.Context, tx *sql.Tx, rows []providerBindingUUIDRow) error {
	if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS trg_prevent_agent_provider_binding_rebind"); err != nil {
		return fmt.Errorf("drop agent_provider_binding identity trigger: %w", err)
	}
	for _, row := range rows {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_provider_binding
			SET provider_thread_id = ?,
			    session_uuid = ?,
			    provider_recovery_home = CASE
			        WHEN provider_recovery_home <> '' THEN provider_recovery_home
			        WHEN provider = 'codex' THEN codex_home
			        ELSE ''
			    END
			WHERE agent_id = ?
		`, row.providerThreadID, row.sessionUUID, row.agentID); err != nil {
			return fmt.Errorf("backfill provider binding %q: %w", row.agentID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, agentProviderBindingIdentityTriggerSQL); err != nil {
		return fmt.Errorf("restore agent_provider_binding identity trigger: %w", err)
	}
	return nil
}

type providerBindingUUIDRow struct {
	agentID, provider, providerThreadID, sessionUUID string
}

func loadProviderBindingUUIDRows(ctx context.Context, tx *sql.Tx) ([]providerBindingUUIDRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT agent_id, provider, provider_thread_id, session_uuid
		FROM agent_provider_binding
		ORDER BY agent_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list provider binding UUIDs: %w", err)
	}
	defer rows.Close()
	var result []providerBindingUUIDRow
	for rows.Next() {
		var row providerBindingUUIDRow
		if err := rows.Scan(&row.agentID, &row.provider, &row.providerThreadID, &row.sessionUUID); err != nil {
			return nil, fmt.Errorf("scan provider binding UUIDs: %w", err)
		}
		row.provider = strings.ToLower(strings.TrimSpace(row.provider))
		row.sessionUUID, err = canonicalMigrationUUID("session_uuid", row.agentID, row.sessionUUID)
		if err != nil {
			return nil, err
		}
		row.providerThreadID, err = canonicalMigrationProviderThreadID(row)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider binding UUIDs: %w", err)
	}
	return result, nil
}

func canonicalMigrationProviderThreadID(row providerBindingUUIDRow) (string, error) {
	switch row.providerThreadID {
	case "":
		return row.sessionUUID, nil
	case row.agentID:
		if row.sessionUUID == "" {
			return "", fmt.Errorf("agent %q legacy provider_thread_id placeholder requires session_uuid", row.agentID)
		}
		return row.sessionUUID, nil
	default:
		return canonicalMigrationUUID("provider_thread_id", row.agentID, row.providerThreadID)
	}
}

func canonicalMigrationUUID(field, agentID, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.TrimSpace(raw) != raw {
		return "", fmt.Errorf("agent %q %s contains surrounding whitespace", agentID, field)
	}
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed == uuid.Nil {
		return "", fmt.Errorf("agent %q %s is not a valid non-nil UUID: %q", agentID, field, raw)
	}
	return parsed.String(), nil
}

func validateProviderBindingUUIDOwnership(rows []providerBindingUUIDRow) error {
	owners := make(map[string]string, len(rows)*2)
	for _, row := range rows {
		for _, identity := range []string{row.providerThreadID, row.sessionUUID} {
			if identity == "" {
				continue
			}
			key := row.provider + "\x00" + identity
			if owner := owners[key]; owner != "" && owner != row.agentID {
				return fmt.Errorf(
					"canonical UUID collision for provider %q UUID %q between agents %q and %q",
					row.provider, identity, owner, row.agentID,
				)
			}
			owners[key] = row.agentID
		}
	}
	return nil
}

func validateProviderBindingRecoveryState(ctx context.Context, tx *sql.Tx) error {
	columns, err := sqliteTableColumns(ctx, tx, "agent_provider_binding")
	if err != nil {
		return err
	}
	if err := requireProviderBindingRecoveryStateColumns(columns); err != nil {
		return err
	}
	rows, err := loadStoredProviderBindingRecoveryRows(ctx, tx)
	if err != nil {
		return err
	}
	if err := validateStoredProviderBindingRecoveryRows(rows); err != nil {
		return err
	}
	return requireProviderBindingRecoveryTrigger(ctx, tx)
}

func requireProviderBindingRecoveryStateColumns(columns map[string]bool) error {
	if err := requireProviderBindingRecoveryColumns(columns); err != nil {
		return err
	}
	if !columns["provider_recovery_home"] {
		return fmt.Errorf("agent_provider_binding missing required column provider_recovery_home")
	}
	return nil
}

type storedProviderBindingRecoveryRow struct {
	providerBindingUUIDRow
	codexHome    string
	recoveryHome string
}

func loadStoredProviderBindingRecoveryRows(
	ctx context.Context,
	tx *sql.Tx,
) ([]storedProviderBindingRecoveryRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT agent_id, provider, provider_thread_id, session_uuid,
		       codex_home, provider_recovery_home
		FROM agent_provider_binding
		ORDER BY agent_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list stored provider binding recovery owners: %w", err)
	}
	defer rows.Close()
	var result []storedProviderBindingRecoveryRow
	for rows.Next() {
		var row storedProviderBindingRecoveryRow
		if err := rows.Scan(
			&row.agentID,
			&row.provider,
			&row.providerThreadID,
			&row.sessionUUID,
			&row.codexHome,
			&row.recoveryHome,
		); err != nil {
			return nil, fmt.Errorf("scan stored provider binding recovery owner: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stored provider binding recovery owners: %w", err)
	}
	return result, nil
}

func validateStoredProviderBindingRecoveryRows(rows []storedProviderBindingRecoveryRow) error {
	canonical := make([]providerBindingUUIDRow, 0, len(rows))
	for _, row := range rows {
		if err := validateStoredProviderBindingRecoveryRow(row); err != nil {
			return err
		}
		canonical = append(canonical, row.providerBindingUUIDRow)
	}
	return validateProviderBindingUUIDOwnership(canonical)
}

func validateStoredProviderBindingRecoveryRow(row storedProviderBindingRecoveryRow) error {
	if row.provider != "codex" && row.provider != "claude" {
		return fmt.Errorf("agent %q has unsupported provider %q", row.agentID, row.provider)
	}
	sessionUUID, err := canonicalMigrationUUID("session_uuid", row.agentID, row.sessionUUID)
	if err != nil || sessionUUID != row.sessionUUID {
		return fmt.Errorf("agent %q session_uuid is not canonical", row.agentID)
	}
	providerThreadID, err := canonicalMigrationProviderThreadID(row.providerBindingUUIDRow)
	if err != nil || providerThreadID != row.providerThreadID {
		return fmt.Errorf("agent %q provider_thread_id is not canonical", row.agentID)
	}
	if row.provider == "codex" && row.codexHome != "" && row.recoveryHome != row.codexHome {
		return fmt.Errorf("agent %q provider recovery owner does not match codex_home", row.agentID)
	}
	return nil
}

func requireProviderBindingRecoveryTrigger(ctx context.Context, tx *sql.Tx) error {
	var actual string
	if err := tx.QueryRowContext(
		ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?",
		"trg_prevent_agent_provider_binding_rebind",
	).Scan(&actual); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("provider binding recovery identity trigger is missing")
		}
		return fmt.Errorf("read provider binding recovery identity trigger: %w", err)
	}
	if normalizeSQLiteDefinition(actual) != normalizeSQLiteDefinition(agentProviderBindingIdentityTriggerSQL) {
		return fmt.Errorf("provider binding recovery identity trigger does not match canonical migration")
	}
	return nil
}

const agentProviderBindingIdentityTriggerSQL = `
CREATE TRIGGER trg_prevent_agent_provider_binding_rebind
BEFORE UPDATE ON agent_provider_binding
FOR EACH ROW
WHEN NEW.agent_id <> OLD.agent_id
  OR NEW.provider <> OLD.provider
  OR (OLD.provider_thread_id <> '' AND NEW.provider_thread_id <> OLD.provider_thread_id)
  OR (OLD.codex_instance_key <> '' AND NEW.codex_instance_key <> OLD.codex_instance_key)
  OR (OLD.codex_model_provider <> '' AND NEW.codex_model_provider <> OLD.codex_model_provider)
  OR (OLD.provider_recovery_home <> '' AND NEW.provider_recovery_home <> OLD.provider_recovery_home)
  OR (
      OLD.codex_home <> ''
      AND NEW.codex_home <> OLD.codex_home
      AND (
          (OLD.codex_instance_key <> '' AND NEW.codex_instance_key <> OLD.codex_instance_key)
          OR (OLD.codex_model_provider <> '' AND NEW.codex_model_provider <> OLD.codex_model_provider)
      )
  )
BEGIN
    SELECT RAISE(ABORT, 'agent_provider_binding identity is immutable');
END`
