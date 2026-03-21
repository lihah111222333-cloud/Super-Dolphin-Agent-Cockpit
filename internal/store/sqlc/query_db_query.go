package sqlc

import "context"

const (
	// TODO(p7w2): replace this placeholder SQL once the dbquery migration lands
	// in sql/queries and sqlc can generate the real query surface.
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
