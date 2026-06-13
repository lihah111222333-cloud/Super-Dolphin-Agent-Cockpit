package sqlc

import "time"

type Timestamptz = int64
type Interval = int64
type Text = *string
type Int8 = *int64

func TimeValue(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func TimePtr(value *int64) *time.Time {
	if value == nil || *value == 0 {
		return nil
	}
	copy := time.UnixMilli(*value).UTC()
	return &copy
}

func TimeValuePtr(value *time.Time) *int64 {
	if value == nil || value.IsZero() {
		return nil
	}
	millis := value.UTC().UnixMilli()
	return &millis
}

func TextPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func TextValuePtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func Int8Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func Int8ValuePtr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func IntervalMillis(value time.Duration) int64 {
	return value.Milliseconds()
}
