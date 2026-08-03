package gate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

// parsePlanGateTimingMetadata 按当前协议解析测试数量与承载记录数量。
func parsePlanGateTimingMetadata(fields []string, schemaVersion uint32) (int, int, error) {
	if schemaVersion != ExecutorPlanReportSchemaVersion {
		return 0, 0, errors.New("plan report timing schema is unsupported")
	}
	count, err := parseSixDigitCountAllowZero(fields[10])
	if err != nil || count > executorPlanMaxTimingRecords {
		return 0, 0, errors.New("plan report test timing count is invalid")
	}
	recordCount, err := parseSixDigitCountAllowZero(fields[11])
	if err != nil || recordCount > executorPlanMaxTransportRecords ||
		(count == 0) != (recordCount == 0) || recordCount > count {
		return 0, 0, errors.New("plan report test timing record count is invalid")
	}
	return count, recordCount, nil
}

// encodePlanTimingReportRecords 按当前 schema 将多条耗时紧凑装入有界普通文本记录。
func encodePlanTimingReportRecords(gateIndex int, timings []GoTestTiming, schemaVersion uint32) ([]string, error) {
	if schemaVersion != ExecutorPlanReportSchemaVersion {
		return nil, errors.New("plan report timing schema is unsupported")
	}
	return encodePackedPlanTimingRecords(gateIndex, timings)
}

// encodePackedPlanTimingRecords 将多条测试耗时装入受单行字节合同约束的当前记录。
func encodePackedPlanTimingRecords(gateIndex int, timings []GoTestTiming) ([]string, error) {
	if len(timings) == 0 {
		return nil, nil
	}
	const metadataFields = "T 000000 000000 000000 000000"
	payloadLimit := executorPlanReportChunkBytes - len(metadataFields)
	groups := make([]string, 0, len(timings))
	current := ""
	for _, timing := range timings {
		entry := fmt.Sprintf("%s %s %d", timing.Name, timing.Status, timing.DurationMS)
		if len(entry) > payloadLimit {
			return nil, errors.New("plan report test timing entry exceeds record limit")
		}
		if current != "" && len(current)+1+len(entry) > payloadLimit {
			groups = append(groups, current)
			current = ""
		}
		if current == "" {
			current = entry
		} else {
			current += " " + entry
		}
	}
	groups = append(groups, current)
	records := make([]string, len(groups))
	for index, group := range groups {
		records[index] = fmt.Sprintf(
			"%s %06d %06d %06d %06d %s",
			planReportTimingRecord,
			gateIndex,
			index+1,
			len(groups),
			len(timings),
			group,
		)
	}
	return records, nil
}

// decodePlanTimingReportRecords 只解码当前 schema 的紧凑记录。
func decodePlanTimingReportRecords(
	records []planReportRecord,
	gateIndex int,
	timingCount int,
	recordCount int,
	schemaVersion uint32,
) ([]GoTestTiming, int, error) {
	if schemaVersion != ExecutorPlanReportSchemaVersion {
		return nil, 0, errors.New("plan report timing schema is unsupported")
	}
	return decodePackedPlanTimingReportRecords(records, gateIndex, timingCount, recordCount)
}

// decodePackedPlanTimingReportRecords 解码当前紧凑记录并校验完整测试集合。
func decodePackedPlanTimingReportRecords(
	records []planReportRecord,
	gateIndex int,
	timingCount int,
	recordCount int,
) ([]GoTestTiming, int, error) {
	if timingCount == 0 {
		return nil, 0, nil
	}
	if recordCount <= 0 || recordCount > len(records) {
		return nil, 0, errors.New("plan report packed timing record is missing")
	}
	timings := make([]GoTestTiming, 0, timingCount)
	for index := 1; index <= recordCount; index++ {
		record := records[index-1]
		if record.kind != planReportTimingRecord {
			return nil, 0, errors.New("plan report packed timing record is missing")
		}
		decoded, err := decodePackedPlanTimingRecord(record.payload, gateIndex, index, recordCount, timingCount)
		if err != nil {
			return nil, 0, err
		}
		timings = append(timings, decoded...)
	}
	if len(timings) != timingCount || testtiming.ValidateList(timings, executorPlanMaxTimingRecords) != nil {
		return nil, 0, errors.New("plan report packed timing set is invalid")
	}
	return timings, recordCount, nil
}

// decodePackedPlanTimingRecord 严格解码一条 v3 紧凑耗时记录。
func decodePackedPlanTimingRecord(
	payload string,
	gateIndex int,
	recordIndex int,
	recordCount int,
	timingCount int,
) ([]GoTestTiming, error) {
	fields := strings.Fields(payload)
	if len(fields) < 7 || (len(fields)-4)%3 != 0 || strings.Join(fields, " ") != payload {
		return nil, errors.New("plan report packed timing payload is invalid")
	}
	if err := validatePackedPlanTimingSequence(fields[:4], gateIndex, recordIndex, recordCount, timingCount); err != nil {
		return nil, err
	}
	timings := make([]GoTestTiming, 0, (len(fields)-4)/3)
	for index := 4; index < len(fields); index += 3 {
		duration, err := strconv.ParseInt(fields[index+2], 10, 64)
		if err != nil {
			return nil, errors.New("plan report packed timing duration is invalid")
		}
		timing := GoTestTiming{Name: fields[index], Status: GoTestStatus(fields[index+1]), DurationMS: duration}
		if err := testtiming.Validate(timing); err != nil {
			return nil, fmt.Errorf("plan report packed timing: %w", err)
		}
		timings = append(timings, timing)
	}
	return timings, nil
}

// validatePackedPlanTimingSequence 校验紧凑记录的 gate、序号、记录总数与测试总数。
func validatePackedPlanTimingSequence(fields []string, gateIndex int, recordIndex int, recordCount int, timingCount int) error {
	observedGate, gateErr := parseSixDigitCount(fields[0])
	observedIndex, indexErr := parseSixDigitCount(fields[1])
	observedRecords, recordsErr := parseSixDigitCount(fields[2])
	observedTimings, timingsErr := parseSixDigitCount(fields[3])
	if gateErr != nil || indexErr != nil || recordsErr != nil || timingsErr != nil ||
		observedGate != gateIndex || observedIndex != recordIndex ||
		observedRecords != recordCount || observedTimings != timingCount {
		return errors.New("plan report packed timing sequence is invalid")
	}
	return nil
}
