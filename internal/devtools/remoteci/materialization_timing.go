package remoteci

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const shardCompileTimingRecordPrefix = "SUPER_DOLPHIN_SHARD_COMPILE "

// decodeShardMaterializationTimingLog extracts the one init-container timing evidence record.
func decodeShardMaterializationTimingLog(log, expectedShardIdentity string) (gate.ShardMaterializationTiming, error) {
	if expectedShardIdentity == "" {
		return gate.ShardMaterializationTiming{}, errors.New("expected shard materialization timing identity is required")
	}
	var record string
	scanner := bufio.NewScanner(strings.NewReader(log))
	scanner.Buffer(make([]byte, 4<<10), 64<<10)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, gate.ShardMaterializationTimingRecordPrefix) {
			continue
		}
		if record != "" {
			return gate.ShardMaterializationTiming{}, errors.New("remote shard materialization timing record is not unique")
		}
		record = line
	}
	if err := scanner.Err(); err != nil {
		return gate.ShardMaterializationTiming{}, err
	}
	if record == "" {
		return gate.ShardMaterializationTiming{}, errors.New("remote shard materialization timing record is missing")
	}
	timing, err := gate.DecodeShardMaterializationTimingRecord(record)
	if err != nil {
		return gate.ShardMaterializationTiming{}, err
	}
	if timing.ShardIdentity != expectedShardIdentity {
		return gate.ShardMaterializationTiming{}, errors.New("remote shard materialization timing identity does not match assignment")
	}
	return timing, nil
}

// bindShardCandidateCompileTimingLog attaches the candidate gate build duration to
// the materializer receipt. The build is a shard-scoped init phase, not a workload
// startup cost, so it must never be inferred from a workload's residual time.
func bindShardCandidateCompileTimingLog(log string, timing gate.ShardMaterializationTiming) (gate.ShardMaterializationTiming, error) {
	var record string
	scanner := bufio.NewScanner(strings.NewReader(log))
	scanner.Buffer(make([]byte, 4<<10), 64<<10)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, shardCompileTimingRecordPrefix) {
			continue
		}
		if record != "" {
			return gate.ShardMaterializationTiming{}, errors.New("remote shard candidate compile timing record is not unique")
		}
		record = line
	}
	if err := scanner.Err(); err != nil {
		return gate.ShardMaterializationTiming{}, err
	}
	if record == "" {
		return gate.ShardMaterializationTiming{}, errors.New("remote shard candidate compile timing record is missing")
	}
	fields := strings.Fields(strings.TrimPrefix(record, shardCompileTimingRecordPrefix))
	if len(fields) != 4 {
		return gate.ShardMaterializationTiming{}, errors.New("remote shard candidate compile timing field count is invalid")
	}
	startedText, startedOK := strings.CutPrefix(fields[0], "started_at_unix_ms=")
	completedText, completedOK := strings.CutPrefix(fields[1], "completed_at_unix_ms=")
	durationText, durationOK := strings.CutPrefix(fields[2], "duration_ms=")
	cacheMetrics, cacheMetricsOK := strings.CutPrefix(fields[3], "cache_metrics=")
	if !startedOK || !completedOK || !durationOK || !cacheMetricsOK || cacheMetrics == "" {
		return gate.ShardMaterializationTiming{}, errors.New("remote shard candidate compile timing fields are invalid")
	}
	started, err := strconv.ParseInt(startedText, 10, 64)
	if err != nil || started <= 0 {
		return gate.ShardMaterializationTiming{}, errors.New("remote shard candidate compile start is invalid")
	}
	completed, err := strconv.ParseInt(completedText, 10, 64)
	if err != nil || completed <= started {
		return gate.ShardMaterializationTiming{}, errors.New("remote shard candidate compile completion is invalid")
	}
	duration, err := strconv.ParseInt(durationText, 10, 64)
	if err != nil {
		return gate.ShardMaterializationTiming{}, fmt.Errorf("remote shard candidate compile duration is invalid: %w", err)
	}
	if duration <= 0 || completed-started != duration {
		return gate.ShardMaterializationTiming{}, errors.New("remote shard candidate compile duration must be observed")
	}
	timing.CandidateCompile = gate.MaterializationPhaseTiming{StartedAtUnixMS: started, CompletedAtUnixMS: completed, MaterializeMS: duration}
	if err := timing.Validate(); err != nil {
		return gate.ShardMaterializationTiming{}, fmt.Errorf("validate remote shard candidate compile timing: %w", err)
	}
	return timing, nil
}
