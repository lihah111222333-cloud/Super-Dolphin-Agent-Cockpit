// Package sqlc 是 sqlc 代码生成包的手工扩展，提供 SQLite 与 Go 类型之间的转换工具函数。
// 生成文件（Code generated 头部）不在此修改；本文件只放手工补充的类型别名与辅助函数。
package sqlc

import "time"

// SQLite 列类型别名，与 sqlc 生成代码保持兼容。
type (
	Timestamptz = int64   // 毫秒时间戳
	Interval    = int64   // 毫秒时长
	Text        = *string // 可空文本
	Int8        = *int64  // 可空 int64
)

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
