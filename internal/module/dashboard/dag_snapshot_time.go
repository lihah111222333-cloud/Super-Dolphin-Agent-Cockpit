package dashboard

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// dashboardDAGSummaryTimes 聚合 DAG 摘要行中的时间字段，避免逐字段传参。
type dashboardDAGSummaryTimes struct {
	nextRunAt  *time.Time // 下次计划运行时间，schedule 未启用时为 nil
	startedAt  *time.Time // 最近一次启动时间，未运行时为 nil
	finishedAt *time.Time // 最近一次结束时间，未完成时为 nil
	createdAt  time.Time  // 记录创建时间
	updatedAt  time.Time  // 记录最后更新时间
}

// dashboardDAGNodeTimes 聚合 DAG 节点行中的时间字段。
type dashboardDAGNodeTimes struct {
	startedAt   *time.Time // 节点开始执行时间
	finishedAt  *time.Time // 节点完成时间
	lastEventAt *time.Time // 节点最后一次事件时间
	createdAt   time.Time  // 记录创建时间
	updatedAt   time.Time  // 记录最后更新时间
}

// dashboardRunTimes 聚合 DAG run 行中的时间字段。
type dashboardRunTimes struct {
	startedAt  time.Time  // run 启动时间，必填
	finishedAt *time.Time // run 结束时间，未完成时为 nil
	createdAt  time.Time  // 记录创建时间
	updatedAt  time.Time  // 记录最后更新时间
}

// dashboardDAGSummaryTimesFromRow 从查询结果行解析 DAG 摘要的全部时间字段。
func dashboardDAGSummaryTimesFromRow(row map[string]any) (dashboardDAGSummaryTimes, error) {
	createdAt, err := dashboardRowTime(row, "created_at", true)
	if err != nil {
		return dashboardDAGSummaryTimes{}, err
	}
	updatedAt, err := dashboardRowTime(row, "updated_at", true)
	if err != nil {
		return dashboardDAGSummaryTimes{}, err
	}
	nextRunAt, err := dashboardOptionalTime(row, "next_run_at")
	if err != nil {
		return dashboardDAGSummaryTimes{}, err
	}
	startedAt, err := dashboardOptionalTime(row, "started_at")
	if err != nil {
		return dashboardDAGSummaryTimes{}, err
	}
	finishedAt, err := dashboardOptionalTime(row, "finished_at")
	if err != nil {
		return dashboardDAGSummaryTimes{}, err
	}
	return dashboardDAGSummaryTimes{
		nextRunAt:  nextRunAt,
		startedAt:  startedAt,
		finishedAt: finishedAt,
		createdAt:  createdAt,
		updatedAt:  updatedAt,
	}, nil
}

// dashboardDAGNodeTimesFromRow 从查询结果行解析 DAG 节点的全部时间字段。
func dashboardDAGNodeTimesFromRow(row map[string]any) (dashboardDAGNodeTimes, error) {
	createdAt, err := dashboardRowTime(row, "created_at", true)
	if err != nil {
		return dashboardDAGNodeTimes{}, err
	}
	updatedAt, err := dashboardRowTime(row, "updated_at", true)
	if err != nil {
		return dashboardDAGNodeTimes{}, err
	}
	startedAt, err := dashboardOptionalTime(row, "started_at")
	if err != nil {
		return dashboardDAGNodeTimes{}, err
	}
	finishedAt, err := dashboardOptionalTime(row, "finished_at")
	if err != nil {
		return dashboardDAGNodeTimes{}, err
	}
	lastEventAt, err := dashboardOptionalTime(row, "last_event_at")
	if err != nil {
		return dashboardDAGNodeTimes{}, err
	}
	return dashboardDAGNodeTimes{
		startedAt:   startedAt,
		finishedAt:  finishedAt,
		lastEventAt: lastEventAt,
		createdAt:   createdAt,
		updatedAt:   updatedAt,
	}, nil
}

// dashboardRunTimesFromRow 从查询结果行解析 DAG run 的全部时间字段。
func dashboardRunTimesFromRow(row map[string]any) (dashboardRunTimes, error) {
	startedAt, err := dashboardRowTime(row, "started_at", true)
	if err != nil {
		return dashboardRunTimes{}, err
	}
	createdAt, err := dashboardRowTime(row, "created_at", true)
	if err != nil {
		return dashboardRunTimes{}, err
	}
	updatedAt, err := dashboardRowTime(row, "updated_at", true)
	if err != nil {
		return dashboardRunTimes{}, err
	}
	finishedAt, err := dashboardOptionalTime(row, "finished_at")
	if err != nil {
		return dashboardRunTimes{}, err
	}
	return dashboardRunTimes{
		startedAt:  startedAt,
		finishedAt: finishedAt,
		createdAt:  createdAt,
		updatedAt:  updatedAt,
	}, nil
}

// dashboardRowTime 读取必填或可选时间字段并返回值类型。
// optional 字段缺失时返回零值；required 字段缺失会返回错误。
func dashboardRowTime(row map[string]any, key string, required bool) (time.Time, error) {
	ptr, err := dashboardRowTimePtr(row, key, required)
	if err != nil {
		return time.Time{}, err
	}
	if ptr == nil {
		return time.Time{}, nil
	}
	return *ptr, nil
}

// dashboardOptionalTime 读取可选时间字段，字段缺失时返回 nil。
func dashboardOptionalTime(row map[string]any, key string) (*time.Time, error) {
	return dashboardRowTimePtr(row, key, false)
}

// dashboardRowTimePtr 统一解析时间字段，依次尝试 time.Time、unix 毫秒整数和 RFC3339 字符串。
func dashboardRowTimePtr(row map[string]any, key string, required bool) (*time.Time, error) {
	value, ok := row[key]
	if !ok || value == nil {
		return dashboardMissingTimePtr(key, required)
	}
	if parsed, ok := dashboardNativeTimePtr(value); ok {
		return parsed, nil
	}
	if parsed, ok, err := dashboardUnixMillisValueTimePtr(value, key, required); ok || err != nil {
		return parsed, err
	}
	if parsed, ok, err := dashboardStringTimePtr(value, key); ok || err != nil {
		return parsed, err
	}
	return nil, fmt.Errorf("%s has unsupported type %T", key, value)
}

// dashboardMissingTimePtr 处理字段缺失时的 required/optional 分支。
// required=true 必须 fail-fast，optional 字段缺失则按 nil 指针表示未设置。
func dashboardMissingTimePtr(key string, required bool) (*time.Time, error) {
	if required {
		return nil, fmt.Errorf("%s is required", key)
	}
	return nil, nil
}

// dashboardNativeTimePtr 处理 time.Time 和 *time.Time 原生类型的解析。
func dashboardNativeTimePtr(value any) (*time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return &typed, true
	case *time.Time:
		return typed, true
	default:
		return nil, false
	}
}

// dashboardUnixMillisValueTimePtr 将常见整数类型和 json.Number/float64 解析为 unix 毫秒时间。
func dashboardUnixMillisValueTimePtr(value any, key string, required bool) (*time.Time, bool, error) {
	switch typed := value.(type) {
	case int64:
		return dashboardUnixMillisTimePtr(typed, required), true, nil
	case *int64:
		parsed, err := dashboardUnixMillisInt64Ptr(typed, key, required)
		return parsed, true, err
	case int:
		return dashboardUnixMillisTimePtr(int64(typed), required), true, nil
	case int32:
		return dashboardUnixMillisTimePtr(int64(typed), required), true, nil
	case json.Number:
		return dashboardJSONMillisTimePtr(typed, key, required)
	case float64:
		return dashboardFloatMillisTimePtr(typed, key, required)
	default:
		return nil, false, nil
	}
}

// dashboardUnixMillisInt64Ptr 解引用 *int64 后转换为时间指针，nil 时按 required 处理。
func dashboardUnixMillisInt64Ptr(value *int64, key string, required bool) (*time.Time, error) {
	if value == nil {
		if required {
			return nil, fmt.Errorf("%s is required", key)
		}
		return nil, nil
	}
	return dashboardUnixMillisTimePtr(*value, required), nil
}

// dashboardJSONMillisTimePtr 将 json.Number 解析为 int64 后转换为时间指针。
func dashboardJSONMillisTimePtr(value json.Number, key string, required bool) (*time.Time, bool, error) {
	parsed, err := value.Int64()
	if err != nil {
		return nil, true, fmt.Errorf("%s: %w", key, err)
	}
	return dashboardUnixMillisTimePtr(parsed, required), true, nil
}

// dashboardFloatMillisTimePtr 将 float64 毫秒时间戳转换为时间指针，非整数时 fail-fast。
func dashboardFloatMillisTimePtr(value float64, key string, required bool) (*time.Time, bool, error) {
	if math.Trunc(value) != value {
		return nil, true, fmt.Errorf("%s must be an integer millisecond timestamp", key)
	}
	return dashboardUnixMillisTimePtr(int64(value), required), true, nil
}

// dashboardStringTimePtr 尝试将字符串按 RFC3339Nano 格式解析为时间指针。
func dashboardStringTimePtr(value any, key string) (*time.Time, bool, error) {
	typed, ok := value.(string)
	if !ok {
		return nil, false, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(typed))
	if err != nil {
		return nil, true, fmt.Errorf("%s: %w", key, err)
	}
	return &parsed, true, nil
}

// dashboardUnixMillisTimePtr 将 unix 毫秒整数转为 UTC 时间指针；value=0 且 required=false 时返回 nil。
func dashboardUnixMillisTimePtr(value int64, required bool) *time.Time {
	if value == 0 {
		if !required {
			return nil
		}
		zero := time.Time{}
		return &zero
	}
	parsed := time.UnixMilli(value).UTC()
	return &parsed
}
