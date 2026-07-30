package gate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// RemoteCIPhaseOutcome 标识一个协调器阶段是否成功完成。
type RemoteCIPhaseOutcome string

const (
	RemoteCIPhaseOutcomeSucceeded RemoteCIPhaseOutcome = "succeeded"
	RemoteCIPhaseOutcomeFailed    RemoteCIPhaseOutcome = "failed"
)

// RemoteCIPhaseTiming 是可实时输出并持久化查询的协调器阶段性能记录。
type RemoteCIPhaseTiming struct {
	Phase          string               `json:"phase"`
	StartedAt      time.Time            `json:"started_at"`
	DurationMillis int64                `json:"duration_ms"`
	Outcome        RemoteCIPhaseOutcome `json:"outcome"`
	WorkloadCount  int                  `json:"workload_count"`
	ShardCount     int                  `json:"shard_count"`
	CacheHitCount  int                  `json:"cache_hit_count"`
	CacheMissCount int                  `json:"cache_miss_count"`
}

func validateRemoteCIPhaseTimings(timings []RemoteCIPhaseTiming) error {
	for index, timing := range timings {
		if err := validateRemoteCIPhaseTiming(timing); err != nil {
			return fmt.Errorf("remote CI phase timing[%d]: %w", index, err)
		}
	}
	return nil
}

func validateRemoteCIPhaseTiming(timing RemoteCIPhaseTiming) error {
	if !validRemoteCIPhaseName(timing.Phase) {
		return fmt.Errorf("phase %q is not canonical", timing.Phase)
	}
	if timing.StartedAt.IsZero() {
		return errors.New("start timestamp is required")
	}
	if timing.DurationMillis < 0 {
		return errors.New("duration cannot be negative")
	}
	switch timing.Outcome {
	case RemoteCIPhaseOutcomeSucceeded, RemoteCIPhaseOutcomeFailed:
	default:
		return fmt.Errorf("outcome %q is invalid", timing.Outcome)
	}
	for field, count := range map[string]int{
		"workload":   timing.WorkloadCount,
		"shard":      timing.ShardCount,
		"cache hit":  timing.CacheHitCount,
		"cache miss": timing.CacheMissCount,
	} {
		if count < 0 {
			return fmt.Errorf("%s count cannot be negative", field)
		}
	}
	return nil
}

func validRemoteCIPhaseName(phase string) bool {
	if phase == "" || phase != strings.TrimSpace(phase) {
		return false
	}
	for _, character := range phase {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
