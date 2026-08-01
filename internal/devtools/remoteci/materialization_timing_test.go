package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestDecodeShardMaterializationTimingLog(t *testing.T) {
	timing := gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: strings.Repeat("a", 64), Source: gate.MaterializationPhaseTiming{MaterializeMS: 1}}
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
