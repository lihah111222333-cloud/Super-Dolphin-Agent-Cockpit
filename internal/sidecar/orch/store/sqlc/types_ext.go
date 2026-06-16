package sqlc

import "time"

// Timestamptz describes a sqlc API type.
type Timestamptz = int64

// Interval describes a sqlc API type.
type Interval = int64

// Text describes a sqlc API type.
type Text = *string

// Int8 describes a sqlc API type.
type Int8 = *int64

// TimeValue 将 SQLite 毫秒时间戳转换为 UTC 时间。
func TimeValue(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

// TimePtr 将可空 SQLite 毫秒时间戳转换为 UTC 时间指针。
func TimePtr(value *int64) *time.Time {
	if value == nil || *value == 0 {
		return nil
	}
	copy := time.UnixMilli(*value).UTC()
	return &copy
}

// TimeValuePtr 将时间指针转换为 SQLite 毫秒时间戳指针。
func TimeValuePtr(value *time.Time) *int64 {
	if value == nil || value.IsZero() {
		return nil
	}
	millis := value.UTC().UnixMilli()
	return &millis
}

// TextPtr 返回可空文本的拷贝指针。
func TextPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// TextValuePtr 将字符串指针转换为 sqlc 文本值。
func TextValuePtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// Int8Ptr 返回可空 int64 的拷贝指针。
func Int8Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// Int8ValuePtr 将 int64 指针转换为 sqlc int8 值。
func Int8ValuePtr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// IntervalMillis 将 Go duration 转换为 SQLite 毫秒间隔。
func IntervalMillis(value time.Duration) int64 {
	return value.Milliseconds()
}
