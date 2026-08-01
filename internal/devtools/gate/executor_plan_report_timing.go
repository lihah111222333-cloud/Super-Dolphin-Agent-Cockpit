package gate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

// parsePlanGateTimingMetadata 按协议版本解析测试数量与承载记录数量。
func parsePlanGateTimingMetadata(fields []string, schemaVersion uint32) (int, int, error) {
	if schemaVersion < 2 {
		return 0, 0, nil
	}
	count, err := parseSixDigitCountAllowZero(fields[10])
	if err != nil || count > executorPlanMaxTimingRecords {
		return 0, 0, errors.New("plan report test timing count is invalid")
	}
	if schemaVersion < 3 {
		return count, count, nil
	}
	recordCount, err := parseSixDigitCountAllowZero(fields[11])
	if err != nil || recordCount > executorPlanMaxTransportRecords ||
		(count == 0) != (recordCount == 0) || recordCount > count {
		return 0, 0, errors.New("plan report test timing record count is invalid")
	}
	return count, recordCount, nil
}

// encodePlanTimingReportRecords 按 schema 编码测试耗时；v3 将多条耗时紧凑装入有界普通文本记录。
func encodePlanTimingReportRecords(gateIndex int, timings []GoTestTiming, schemaVersion uint32) ([]string, error) {
	switch schemaVersion {
	case 1:
		if len(timings) != 0 {
			return nil, errors.New("legacy plan report cannot contain test timings")
		}
		return nil, nil
	case executorPlanTimingSchemaVersion:
		return encodeLegacyPlanTimingRecords(gateIndex, timings), nil
	case executorPlanReportSchemaVersion:
		return encodePackedPlanTimingRecords(gateIndex, timings)
	default:
		return nil, errors.New("plan report timing schema is unsupported")
	}
}

// encodeLegacyPlanTimingRecords 保留 v2 每条测试占一条记录的兼容编码。
func encodeLegacyPlanTimingRecords(gateIndex int, timings []GoTestTiming) []string {
	records := make([]string, 0, len(timings))
	for timingIndex, timing := range timings {
		records = append(records, fmt.Sprintf(
			"%s %06d %06d %06d %s %d %s",
			planReportTimingRecord,
			gateIndex,
			timingIndex+1,
			len(timings),
			timing.Status,
			timing.DurationMS,
			timing.Name,
		))
	}
	return records
}

// encodePackedPlanTimingRecords 将多条测试耗时装入受单行字节合同约束的 v3 记录。
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

// decodePlanTimingReportRecords 解码 legacy 单条记录或 v3 紧凑记录。
func decodePlanTimingReportRecords(
	records []planReportRecord,
	gateIndex int,
	timingCount int,
	recordCount int,
	schemaVersion uint32,
) ([]GoTestTiming, int, error) {
	if schemaVersion < executorPlanReportSchemaVersion {
		return decodeLegacyPlanTimingReportRecords(records, gateIndex, timingCount, recordCount)
	}
	return decodePackedPlanTimingReportRecords(records, gateIndex, timingCount, recordCount)
}

// decodeLegacyPlanTimingReportRecords 校验 v2 一测试一记录合同后执行兼容解码。
func decodeLegacyPlanTimingReportRecords(
	records []planReportRecord,
	gateIndex int,
	timingCount int,
	recordCount int,
) ([]GoTestTiming, int, error) {
	if recordCount != timingCount {
		return nil, 0, errors.New("legacy plan report timing record count is invalid")
	}
	return decodePlanTimingRecords(records, gateIndex, timingCount)
}

// decodePackedPlanTimingReportRecords 解码 v3 紧凑记录并校验完整测试集合。
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

// decodePlanTimingRecords 解码一个 gate 的完整测试级耗时列表。
func decodePlanTimingRecords(records []planReportRecord, gateIndex int, count int) ([]GoTestTiming, int, error) {
	if count == 0 {
		return nil, 0, nil
	}
	timings := make([]GoTestTiming, 0, count)
	for index := 1; index <= count; index++ {
		if index > len(records) || records[index-1].kind != planReportTimingRecord {
			return nil, 0, errors.New("plan report test timing record is missing")
		}
		timing, err := decodePlanTimingRecord(records[index-1].payload, gateIndex, index, count)
		if err != nil {
			return nil, 0, err
		}
		timings = append(timings, timing)
	}
	return timings, count, nil
}

// decodePlanTimingRecord 严格解码并校验一条测试耗时记录。
func decodePlanTimingRecord(payload string, gateIndex int, timingIndex int, timingCount int) (GoTestTiming, error) {
	fields := strings.Fields(payload)
	if len(fields) != 6 || strings.Join(fields, " ") != payload {
		return GoTestTiming{}, errors.New("plan report test timing payload is invalid")
	}
	if err := validatePlanTimingRecordSequence(fields, gateIndex, timingIndex, timingCount); err != nil {
		return GoTestTiming{}, err
	}
	duration, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return GoTestTiming{}, errors.New("plan report test timing duration is invalid")
	}
	timing := GoTestTiming{Name: fields[5], Status: GoTestStatus(fields[3]), DurationMS: duration}
	if err := testtiming.Validate(timing); err != nil {
		return GoTestTiming{}, fmt.Errorf("plan report test timing: %w", err)
	}
	return timing, nil
}

// validatePlanTimingRecordSequence 校验耗时记录所属 gate、序号和总数。
func validatePlanTimingRecordSequence(fields []string, gateIndex int, timingIndex int, timingCount int) error {
	observedGate, err := parseSixDigitCount(fields[0])
	if err != nil || observedGate != gateIndex {
		return errors.New("plan report test timing gate is invalid")
	}
	observedIndex, err := parseSixDigitCount(fields[1])
	if err != nil || observedIndex != timingIndex {
		return errors.New("plan report test timing index is invalid")
	}
	observedCount, err := parseSixDigitCount(fields[2])
	if err != nil || observedCount != timingCount {
		return errors.New("plan report test timing count is invalid")
	}
	return nil
}
