package sqlc

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type Timestamptz = pgtype.Timestamptz
type Interval = pgtype.Interval
type Text = pgtype.Text
type Int8 = pgtype.Int8

// TimeValue 处理时间值。
func TimeValue(value Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

// TimePtr 处理时间指针。
func TimePtr(value Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time
	return &copy
}

// TimeValuePtr 处理时间值指针。
func TimeValuePtr(value *time.Time) Timestamptz {
	if value == nil {
		return Timestamptz{}
	}
	return Timestamptz{Time: *value, Valid: true}
}

// TextPtr 处理文本指针。
func TextPtr(value Text) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}

// TextValuePtr 处理文本值指针。
func TextValuePtr(value *string) Text {
	if value == nil {
		return Text{}
	}
	return Text{String: *value, Valid: true}
}

// Int8Ptr 处理int8指针。
func Int8Ptr(value Int8) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

// Int8ValuePtr 处理int8值指针。
func Int8ValuePtr(value *int64) Int8 {
	if value == nil {
		return Int8{}
	}
	return Int8{Int64: *value, Valid: true}
}
