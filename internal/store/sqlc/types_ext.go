package sqlc

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Rows = pgx.Rows
type FieldDescription = pgconn.FieldDescription
type Timestamptz = pgtype.Timestamptz
type Interval = pgtype.Interval

func RowsFieldNames(rows Rows) []string {
	if rows == nil {
		return nil
	}
	fields := rows.FieldDescriptions()
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, string(field.Name))
	}
	return names
}
