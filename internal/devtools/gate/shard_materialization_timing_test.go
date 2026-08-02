package gate

import (
	"strings"
	"testing"
)

func TestShardMaterializationTimingRecordRoundTrip(t *testing.T) {
	timing := ShardMaterializationTiming{Measurement: MaterializationMeasurementMeasured, ShardIdentity: strings.Repeat("a", 64), Source: MaterializationPhaseTiming{StartedAtUnixMS: 100, CompletedAtUnixMS: 107, DownloadMS: 1, VerifyMS: 2, InstallMS: 3, MaterializeMS: 7}, Baseline: MaterializationPhaseTiming{StartedAtUnixMS: 108, CompletedAtUnixMS: 109, MaterializeMS: 1}, CandidateCompile: MaterializationPhaseTiming{StartedAtUnixMS: 110, CompletedAtUnixMS: 116, DownloadMS: 3, VerifyMS: 2, InstallMS: 1, MaterializeMS: 6}, CandidateTestBinaries: MaterializationPhaseTiming{StartedAtUnixMS: 117, CompletedAtUnixMS: 132, DownloadMS: 8, VerifyMS: 4, InstallMS: 2, MaterializeMS: 15}}
	line, err := EncodeShardMaterializationTimingRecord(timing)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeShardMaterializationTimingRecord(line)
	if err != nil {
		t.Fatal(err)
	}
	if got != timing {
		t.Fatalf("round trip = %#v, want %#v", got, timing)
	}
}

func TestShardMaterializationTimingRecordRejectsUnaccountedPhase(t *testing.T) {
	timing := ShardMaterializationTiming{ShardIdentity: strings.Repeat("a", 64), Source: MaterializationPhaseTiming{DownloadMS: 2, MaterializeMS: 1}}
	if _, err := EncodeShardMaterializationTimingRecord(timing); err == nil {
		t.Fatal("EncodeShardMaterializationTimingRecord unexpectedly accepted unaccounted phase")
	}
}

func TestShardMaterializationTimingMeasurementStates(t *testing.T) {
	for name, timing := range map[string]ShardMaterializationTiming{
		"not measured placeholder": {Measurement: MaterializationMeasurementNotMeasured},
		"unavailable legacy":       {Measurement: MaterializationMeasurementUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			if err := timing.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
	if err := (ShardMaterializationTiming{
		Measurement: MaterializationMeasurementNotMeasured,
		Source:      MaterializationPhaseTiming{MaterializeMS: 1},
	}).Validate(); err == nil {
		t.Fatal("not_measured timing unexpectedly accepted evidence")
	}
}

func TestShardMaterializationTimingRecordRejectsLegacyDurationOnlyEncoding(t *testing.T) {
	legacy := ShardMaterializationTimingRecordPrefix + strings.Repeat("a", 64) + " 0 0 0 1 0 0 0 0 0 0 0 0 0 0 0 0"
	if _, err := DecodeShardMaterializationTimingRecord(legacy); err == nil {
		t.Fatal("DecodeShardMaterializationTimingRecord unexpectedly accepted duration-only legacy record")
	}
}
