package localci

import (
	"database/sql"
	"errors"
	"fmt"
)

func requireOneRow(result sql.Result, action string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", action, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s affected %d rows, want 1", action, rows)
	}
	return nil
}

func rollbackTransaction(tx *sql.Tx, retErr *error, action string) {
	if tx == nil || retErr == nil {
		return
	}
	err := tx.Rollback()
	if err == nil || errors.Is(err, sql.ErrTxDone) {
		return
	}
	*retErr = errors.Join(*retErr, fmt.Errorf("rollback %s: %w", action, err))
}

func closeRows(rows *sql.Rows, retErr *error, action string) {
	if rows == nil || retErr == nil {
		return
	}
	if err := rows.Close(); err != nil {
		*retErr = errors.Join(*retErr, fmt.Errorf("close %s: %w", action, err))
	}
}
