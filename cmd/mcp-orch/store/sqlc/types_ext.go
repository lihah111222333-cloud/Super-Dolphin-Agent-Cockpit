package sqlc

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type Timestamptz = pgtype.Timestamptz
type Interval = pgtype.Interval
type Text = pgtype.Text
type Int8 = pgtype.Int8

func TimeValue(value Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

func TimePtr(value Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time
	return &copy
}

func TimeValuePtr(value *time.Time) Timestamptz {
	if value == nil {
		return Timestamptz{}
	}
	return Timestamptz{Time: *value, Valid: true}
}

func TextPtr(value Text) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}

func TextValuePtr(value *string) Text {
	if value == nil {
		return Text{}
	}
	return Text{String: *value, Valid: true}
}

func Int8Ptr(value Int8) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

func Int8ValuePtr(value *int64) Int8 {
	if value == nil {
		return Int8{}
	}
	return Int8{Int64: *value, Valid: true}
}
