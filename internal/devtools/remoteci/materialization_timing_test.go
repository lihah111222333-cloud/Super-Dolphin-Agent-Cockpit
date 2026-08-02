package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestDecodeShardMaterializationTimingLog(t *testing.T) {
	timing := gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: strings.Repeat("a", 64), Source: gate.MaterializationPhaseTiming{StartedAtUnixMS: 100, CompletedAtUnixMS: 101, MaterializeMS: 1}}
	record, err := gate.EncodeShardMaterializationTimingRecord(timing)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeShardMaterializationTimingLog("before\n"+record+"\nafter", timing.ShardIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if got != timing {
		t.Fatalf("timing = %#v, want %#v", got, timing)
	}
}

func TestDecodeShardMaterializationTimingLogRejectsMissingOrDuplicateRecord(t *testing.T) {
	timing := gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: strings.Repeat("a", 64)}
	record, err := gate.EncodeShardMaterializationTimingRecord(timing)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range []string{"ordinary materializer log", record + "\n" + record} {
		if _, err := decodeShardMaterializationTimingLog(log, timing.ShardIdentity); err == nil {
			t.Fatalf("log %q unexpectedly decoded", log)
		}
	}
}

func TestBindShardCandidateCompileTimingLog(t *testing.T) {
	timing := gate.ShardMaterializationTiming{
		Measurement:   gate.MaterializationMeasurementMeasured,
		ShardIdentity: strings.Repeat("a", 64),
		Source:        gate.MaterializationPhaseTiming{MaterializeMS: 1},
	}
	got, err := bindShardCandidateCompileTimingLog(
		"before\n"+shardCompileTimingRecordPrefix+"started_at_unix_ms=100 completed_at_unix_ms=117 duration_ms=17 cache_metrics=/workspace/work/go-cache/shard-compile.metrics\nafter",
		timing,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.CandidateCompile.MaterializeMS != 17 {
		t.Fatalf("candidate compile duration = %d, want 17", got.CandidateCompile.MaterializeMS)
	}
}

func TestBindShardCandidateCompileTimingLogRejectsMissingDuplicateOrZeroObservation(t *testing.T) {
	timing := gate.ShardMaterializationTiming{
		Measurement:   gate.MaterializationMeasurementMeasured,
		ShardIdentity: strings.Repeat("a", 64),
		Source:        gate.MaterializationPhaseTiming{MaterializeMS: 1},
	}
	for _, log := range []string{
		"ordinary materializer log",
		shardCompileTimingRecordPrefix + "started_at_unix_ms=100 completed_at_unix_ms=100 duration_ms=0 cache_metrics=/workspace/work/go-cache/shard-compile.metrics",
		shardCompileTimingRecordPrefix + "started_at_unix_ms=100 completed_at_unix_ms=101 duration_ms=1 cache_metrics=/workspace/work/go-cache/shard-compile.metrics\n" + shardCompileTimingRecordPrefix + "started_at_unix_ms=102 completed_at_unix_ms=104 duration_ms=2 cache_metrics=/workspace/work/go-cache/shard-compile.metrics",
	} {
		if _, err := bindShardCandidateCompileTimingLog(log, timing); err == nil {
			t.Fatalf("log %q unexpectedly bound candidate compile timing", log)
		}
	}
}
