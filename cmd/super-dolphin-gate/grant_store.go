package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const coordinatorActionGrantSchema = `
CREATE TABLE IF NOT EXISTS coordinator_action_grants (
 grant_id TEXT PRIMARY KEY,
 request_nonce TEXT NOT NULL UNIQUE,
 receipt_id TEXT NOT NULL,
 state TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 grant_json BLOB NOT NULL
);`

var errActionGrantNotFound = errors.New("action grant not found")

// ensureCoordinatorActionGrantSchema 创建并核对 durable grant 状态表的必需列。
func ensureCoordinatorActionGrantSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, coordinatorActionGrantSchema); err != nil {
		return fmt.Errorf("initialize coordinator action grant SQLite: %w", err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(coordinator_action_grants)")
	if err != nil {
		return fmt.Errorf("inspect coordinator action grant schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return errors.Join(fmt.Errorf("scan coordinator action grant schema: %w", err), rows.Close())
		}
		columns[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("read coordinator action grant schema: %w", err)
	}
	for _, required := range []string{"grant_id", "request_nonce", "receipt_id", "state", "expires_at", "grant_json"} {
		if !columns[required] {
			return fmt.Errorf("coordinator action grant schema missing column %q", required)
		}
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS coordinator_action_grants_receipt
ON coordinator_action_grants(receipt_id)`); err != nil {
		return fmt.Errorf("create coordinator action grant receipt index: %w", err)
	}
	return nil
}

// issueActionGrant 在交付前持久化，并在幂等重试时返回既有 nonce 记录。
func (store *coordinatorStore) issueActionGrant(
	ctx context.Context,
	grant gatecontract.ActionGrant,
) (gatecontract.ActionGrant, bool, error) {
	encoded, err := encodeStoredActionGrant(grant)
	if err != nil {
		return gatecontract.ActionGrant{}, false, err
	}
	result, err := store.db.ExecContext(ctx, `INSERT INTO coordinator_action_grants (
grant_id, request_nonce, receipt_id, state, expires_at, grant_json
) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(request_nonce) DO NOTHING`,
		grant.GrantID, grant.Request.RequestNonce, grant.Request.ReceiptID, grant.State,
		grant.ExpiresAt.Format(time.RFC3339Nano), encoded,
	)
	if err != nil {
		return gatecontract.ActionGrant{}, false, fmt.Errorf("persist issued action grant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return gatecontract.ActionGrant{}, false, fmt.Errorf("read action grant insert result: %w", err)
	}
	if rows == 1 {
		return grant, true, nil
	}
	existing, err := store.actionGrantByNonce(ctx, grant.Request.RequestNonce)
	return existing, false, err
}

func (store *coordinatorStore) actionGrantByID(
	ctx context.Context,
	grantID string,
) (gatecontract.ActionGrant, error) {
	return scanStoredActionGrant(store.db.QueryRowContext(ctx, `SELECT grant_id, request_nonce,
receipt_id, state, expires_at, grant_json FROM coordinator_action_grants WHERE grant_id = ?`, grantID))
}

func (store *coordinatorStore) actionGrantByNonce(
	ctx context.Context,
	nonce string,
) (gatecontract.ActionGrant, error) {
	return scanStoredActionGrant(store.db.QueryRowContext(ctx, `SELECT grant_id, request_nonce,
receipt_id, state, expires_at, grant_json FROM coordinator_action_grants WHERE request_nonce = ?`, nonce))
}

// transitionActionGrant 以 issued 状态为前置条件原子写入唯一终态及其新签名。
func (store *coordinatorStore) transitionActionGrant(
	ctx context.Context,
	from gatecontract.ActionGrantState,
	next gatecontract.ActionGrant,
) (gatecontract.ActionGrant, error) {
	encoded, err := encodeStoredActionGrant(next)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_action_grants
SET state = ?, expires_at = ?, grant_json = ? WHERE grant_id = ? AND state = ?`,
		next.State, next.ExpiresAt.Format(time.RFC3339Nano), encoded, next.GrantID, from,
	)
	if err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("transition action grant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("read action grant transition result: %w", err)
	}
	if rows != 1 {
		current, loadErr := store.actionGrantByID(ctx, next.GrantID)
		if loadErr != nil {
			return gatecontract.ActionGrant{}, errors.Join(errors.New("action grant transition lost compare-and-swap"), loadErr)
		}
		return current, fmt.Errorf("action grant is already %s", current.State)
	}
	return next, nil
}

func encodeStoredActionGrant(grant gatecontract.ActionGrant) ([]byte, error) {
	if err := grant.Validate(); err != nil {
		return nil, fmt.Errorf("validate stored action grant: %w", err)
	}
	encoded, err := json.Marshal(grant)
	if err != nil {
		return nil, fmt.Errorf("encode stored action grant: %w", err)
	}
	return encoded, nil
}

// scanStoredActionGrant 严格解码并核对索引列与规范授权载荷。
func scanStoredActionGrant(row *sql.Row) (gatecontract.ActionGrant, error) {
	var grantID, nonce, receiptID, state, expiresAt string
	var encoded []byte
	if err := row.Scan(&grantID, &nonce, &receiptID, &state, &expiresAt, &encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return gatecontract.ActionGrant{}, errActionGrantNotFound
		}
		return gatecontract.ActionGrant{}, fmt.Errorf("scan stored action grant: %w", err)
	}
	var grant gatecontract.ActionGrant
	if err := gatecontract.DecodeStrictJSON(encoded, &grant); err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("decode stored action grant: %w", err)
	}
	parsedExpiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("parse stored action grant expiry: %w", err)
	}
	if grant.GrantID != grantID || grant.Request.RequestNonce != nonce ||
		grant.Request.ReceiptID != receiptID || string(grant.State) != state || !grant.ExpiresAt.Equal(parsedExpiry) {
		return gatecontract.ActionGrant{}, errors.New("stored action grant columns drifted from canonical payload")
	}
	return grant, nil
}
