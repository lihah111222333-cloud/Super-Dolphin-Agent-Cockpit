package remoteci

import (
	"bufio"
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

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
