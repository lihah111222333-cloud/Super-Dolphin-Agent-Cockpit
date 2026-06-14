package dashboard

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

type dashboardDAGSummaryTimes struct {
	nextRunAt  *time.Time
	startedAt  *time.Time
	finishedAt *time.Time
	createdAt  time.Time
	updatedAt  time.Time
}

type dashboardDAGNodeTimes struct {
	startedAt   *time.Time
	finishedAt  *time.Time
	lastEventAt *time.Time
	createdAt   time.Time
	updatedAt   time.Time
}

type dashboardRunTimes struct {
	startedAt  time.Time
	finishedAt *time.Time
	createdAt  time.Time
	updatedAt  time.Time
}

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

func dashboardOptionalTime(row map[string]any, key string) (*time.Time, error) {
	return dashboardRowTimePtr(row, key, false)
}

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

func dashboardMissingTimePtr(key string, required bool) (*time.Time, error) {
	if required {
		return nil, fmt.Errorf("%s is required", key)
	}
	return nil, nil
}

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

func dashboardUnixMillisInt64Ptr(value *int64, key string, required bool) (*time.Time, error) {
	if value == nil {
		if required {
			return nil, fmt.Errorf("%s is required", key)
		}
		return nil, nil
	}
	return dashboardUnixMillisTimePtr(*value, required), nil
}

func dashboardJSONMillisTimePtr(value json.Number, key string, required bool) (*time.Time, bool, error) {
	parsed, err := value.Int64()
	if err != nil {
		return nil, true, fmt.Errorf("%s: %w", key, err)
	}
	return dashboardUnixMillisTimePtr(parsed, required), true, nil
}

func dashboardFloatMillisTimePtr(value float64, key string, required bool) (*time.Time, bool, error) {
	if math.Trunc(value) != value {
		return nil, true, fmt.Errorf("%s must be an integer millisecond timestamp", key)
	}
	return dashboardUnixMillisTimePtr(int64(value), required), true, nil
}

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
