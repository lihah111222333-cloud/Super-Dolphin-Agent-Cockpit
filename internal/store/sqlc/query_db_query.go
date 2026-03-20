package sqlc

import "context"

const (
	placeholderDBQuerySQL = `SELECT NULL::text AS placeholder WHERE FALSE;`
)

func scanPlaceholderDBQueryRow(row rowScanner) (PlaceholderDBQueryRow, error) {
	var item PlaceholderDBQueryRow
	err := row.Scan(&item.Placeholder)
	return item, err
}

func (q *Queries) PlaceholderDBQuery(ctx context.Context) ([]PlaceholderDBQueryRow, error) {
	return queryMany(ctx, q, placeholderDBQuerySQL, scanPlaceholderDBQueryRow)
}
