package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ShardMaterializationTimingRecordPrefix frames a materializer-owned shard evidence record.
// It is deliberately separate from ExecutionProfile, which belongs to an individual gate.
const ShardMaterializationTimingRecordPrefix = "SUPER_DOLPHIN_SHARD_MATERIALIZATION_TIMING "

// MaterializationPhaseTiming contains directly observed wall-clock durations for one artifact.
type MaterializationPhaseTiming struct {
	StartedAtUnixMS   int64 `json:"started_at_unix_ms"`
	CompletedAtUnixMS int64 `json:"completed_at_unix_ms"`
	DownloadMS        int64 `json:"download_ms"`
	VerifyMS          int64 `json:"verify_ms"`
	InstallMS         int64 `json:"install_ms"`
	MaterializeMS     int64 `json:"materialize_ms"`
}

// MaterializationMeasurement declares whether per-shard materialization timing is evidence.
type MaterializationMeasurement string

const (
	MaterializationMeasurementMeasured    MaterializationMeasurement = "measured"
	MaterializationMeasurementNotMeasured MaterializationMeasurement = "not_measured"
	MaterializationMeasurementUnavailable MaterializationMeasurement = "unavailable"
)

// ShardMaterializationTiming is init-container evidence bound to exactly one shard.
type ShardMaterializationTiming struct {
	Measurement           MaterializationMeasurement `json:"measurement"`
	ShardIdentity         string                     `json:"shard_identity"`
	Source                MaterializationPhaseTiming `json:"source"`
	Baseline              MaterializationPhaseTiming `json:"baseline"`
	CandidateCompile      MaterializationPhaseTiming `json:"candidate_compile"`
	CandidateTestBinaries MaterializationPhaseTiming `json:"candidate_test_binaries"`
}

func (timing ShardMaterializationTiming) Validate() error {
	if timing.Measurement == MaterializationMeasurementNotMeasured {
		if timing.ShardIdentity != "" || timing.Source != (MaterializationPhaseTiming{}) || timing.Baseline != (MaterializationPhaseTiming{}) || timing.CandidateCompile != (MaterializationPhaseTiming{}) || timing.CandidateTestBinaries != (MaterializationPhaseTiming{}) {
			return errors.New("not measured shard materialization timing contains evidence")
		}
		return nil
	}
	if timing.Measurement != MaterializationMeasurementMeasured && timing.Measurement != MaterializationMeasurementUnavailable {
		return errors.New("shard materialization timing measurement is invalid")
	}
	if timing.ShardIdentity != "" {
		identity := strings.TrimPrefix(timing.ShardIdentity, "sha256:")
		if len(identity) != sha256.Size*2 {
			return errors.New("shard materialization timing identity is invalid")
		}
		if _, err := hex.DecodeString(identity); err != nil || identity != strings.ToLower(identity) {
			return errors.New("shard materialization timing identity is invalid")
		}
	} else if timing.Measurement == MaterializationMeasurementMeasured {
		return errors.New("measured shard materialization timing identity is required")
	}
	for _, phase := range []MaterializationPhaseTiming{timing.Source, timing.Baseline, timing.CandidateCompile, timing.CandidateTestBinaries} {
		if phase.DownloadMS < 0 || phase.VerifyMS < 0 || phase.InstallMS < 0 || phase.MaterializeMS < 0 ||
			phase.MaterializeMS < phase.DownloadMS+phase.VerifyMS+phase.InstallMS {
			return errors.New("shard materialization timing phase is invalid")
		}
		if (phase.StartedAtUnixMS == 0) != (phase.CompletedAtUnixMS == 0) || (phase.StartedAtUnixMS != 0 && (phase.CompletedAtUnixMS <= phase.StartedAtUnixMS || phase.MaterializeMS == 0 || phase.CompletedAtUnixMS-phase.StartedAtUnixMS != phase.MaterializeMS)) {
			return errors.New("shard materialization timing phase interval is invalid")
		}
	}
	return nil
}

// EncodeShardMaterializationTimingRecord returns one bounded, digest-bound materializer log line.
func EncodeShardMaterializationTimingRecord(timing ShardMaterializationTiming) (string, error) {
	if timing.Measurement == "" {
		timing.Measurement = MaterializationMeasurementMeasured
	}
	if err := timing.Validate(); err != nil {
		return "", err
	}
	if timing.Measurement != MaterializationMeasurementMeasured {
		return "", errors.New("materialization timing record is not measured")
	}
	fields := []string{timing.ShardIdentity}
	for _, phase := range []MaterializationPhaseTiming{timing.Source, timing.Baseline, timing.CandidateCompile, timing.CandidateTestBinaries} {
		fields = append(fields, strconv.FormatInt(phase.StartedAtUnixMS, 10), strconv.FormatInt(phase.CompletedAtUnixMS, 10), strconv.FormatInt(phase.DownloadMS, 10), strconv.FormatInt(phase.VerifyMS, 10), strconv.FormatInt(phase.InstallMS, 10), strconv.FormatInt(phase.MaterializeMS, 10))
	}
	return ShardMaterializationTimingRecordPrefix + strings.Join(fields, " "), nil
}

// DecodeShardMaterializationTimingRecord strictly decodes one materializer evidence line.
func DecodeShardMaterializationTimingRecord(line string) (ShardMaterializationTiming, error) {
	if !strings.HasPrefix(line, ShardMaterializationTimingRecordPrefix) {
		return ShardMaterializationTiming{}, errors.New("shard materialization timing prefix is invalid")
	}
	fields := strings.Fields(strings.TrimPrefix(line, ShardMaterializationTimingRecordPrefix))
	if len(fields) != 25 {
		return ShardMaterializationTiming{}, errors.New("shard materialization timing field count is invalid")
	}
	values := make([]int64, 24)
	for index, field := range fields[1:] {
		value, err := strconv.ParseInt(field, 10, 64)
		if err != nil || value < 0 || strconv.FormatInt(value, 10) != field {
			return ShardMaterializationTiming{}, fmt.Errorf("shard materialization timing field %d is invalid", index)
		}
		values[index] = value
	}
	timing := ShardMaterializationTiming{Measurement: MaterializationMeasurementMeasured, ShardIdentity: fields[0]}
	phases := []*MaterializationPhaseTiming{&timing.Source, &timing.Baseline, &timing.CandidateCompile, &timing.CandidateTestBinaries}
	for index, phase := range phases {
		*phase = MaterializationPhaseTiming{StartedAtUnixMS: values[index*6], CompletedAtUnixMS: values[index*6+1], DownloadMS: values[index*6+2], VerifyMS: values[index*6+3], InstallMS: values[index*6+4], MaterializeMS: values[index*6+5]}
	}
	if err := timing.Validate(); err != nil {
		return timing, err
	}
	for _, phase := range []MaterializationPhaseTiming{timing.Source, timing.Baseline, timing.CandidateCompile, timing.CandidateTestBinaries} {
		if phase.MaterializeMS > 0 && phase.StartedAtUnixMS == 0 {
			return timing, errors.New("decoded shard materialization timing phase lacks a real interval")
		}
	}
	return timing, nil
}
