package gate

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

const (
	planReportHeaderRecord = "H"
	planReportGateRecord   = "G"
	planReportLogRecord    = "L"
	planReportTimingRecord = "T"
	planReportEndRecord    = "E"
)

type planReportRecord struct {
	reportID string
	digest   string
	sequence int
	total    int
	kind     string
	payload  string
}

// writePlanExecutionReport writes the bounded canonical report framing to the executor output.
func writePlanExecutionReport(writer io.Writer, report PlanExecutionReport) error {
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if _, err := fmt.Fprintln(writer, chunk); err != nil {
			return err
		}
	}
	return nil
}

// EncodePlanExecutionReportChunks 将报告编码为带摘要的普通文本记录，日志正文不使用 JSON 或 base64。
func EncodePlanExecutionReportChunks(report PlanExecutionReport) ([]string, error) {
	if err := validatePlanExecutionReportHeader(report); err != nil {
		return nil, err
	}
	records, err := encodePlanReportRecords(report)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 || len(records) > executorPlanMaxReportChunks {
		return nil, errors.New("plan report exceeds record count limit")
	}
	digest := digestPlanExecutionReport(report)
	reportID := strings.TrimPrefix(digest, "sha256:")[:32]
	chunks := make([]string, 0, len(records))
	totalBytes := 0
	for index, record := range records {
		chunk := fmt.Sprintf(
			"%s%s %s %06d %06d %s",
			ExecutorPlanReportChunkPrefix,
			reportID,
			digest,
			index+1,
			len(records),
			record,
		)
		if len(chunk)+1 > executorPlanReportMaxLineBytes {
			return nil, errors.New("plan report record exceeds remote log line limit")
		}
		totalBytes += len(chunk) + 1
		if totalBytes > executorPlanReportMaxOutputBytes {
			return nil, errors.New("plan report exceeds remote log response limit")
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

// encodePlanReportRecords 将报告展开为有序的 header、gate、日志和结束记录。
func encodePlanReportRecords(report PlanExecutionReport) ([]string, error) {
	records := []string{fmt.Sprintf(
		"%s %06d %s %s %06d",
		planReportHeaderRecord,
		report.SchemaVersion,
		report.Profile,
		report.PlanDigest,
		len(report.Gates),
	)}
	for index, result := range report.Gates {
		encodedLog, err := encodePlanLogText(result.Log)
		if err != nil {
			return nil, fmt.Errorf("encode plan gate %q log: %w", result.GateID, err)
		}
		fragments := splitPlanLogText(encodedLog)
		argvDigest := result.ArgvDigest
		if argvDigest == "" {
			argvDigest = "-"
		}
		gateRecord := fmt.Sprintf(
			"%s %06d %s %s %d %s %s %s %s %d %06d",
			planReportGateRecord,
			index+1,
			result.GateID,
			result.Status,
			result.ExitCode,
			result.StartedAt.Format(time.RFC3339Nano),
			result.CompletedAt.Format(time.RFC3339Nano),
			argvDigest,
			result.LogDigest,
			len(result.Log),
			len(fragments),
		)
		if report.SchemaVersion >= 2 {
			gateRecord += fmt.Sprintf(" %06d", len(result.TestTimings))
		}
		records = append(records, gateRecord)
		for fragmentIndex, fragment := range fragments {
			records = append(records, fmt.Sprintf(
				"%s %06d %06d %06d %s",
				planReportLogRecord,
				index+1,
				fragmentIndex+1,
				len(fragments),
				fragment,
			))
		}
		if report.SchemaVersion >= 2 {
			for timingIndex, timing := range result.TestTimings {
				records = append(records, fmt.Sprintf(
					"%s %06d %06d %06d %s %d %s",
					planReportTimingRecord,
					index+1,
					timingIndex+1,
					len(result.TestTimings),
					timing.Status,
					timing.DurationMS,
					timing.Name,
				))
			}
		}
	}
	records = append(records, fmt.Sprintf("%s %06d", planReportEndRecord, len(report.Gates)))
	return records, nil
}

// encodePlanLogText 转义日志中的记录分隔字符，同时保持可读的 UTF-8 文本。
func encodePlanLogText(data []byte) ([]byte, error) {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("plan gate log must be NUL-free UTF-8 text")
	}
	encoded := make([]byte, 0, len(data))
	for _, value := range data {
		switch value {
		case '\\':
			encoded = append(encoded, '\\', '\\')
		case '\n':
			encoded = append(encoded, '\\', 'n')
		case '\r':
			encoded = append(encoded, '\\', 'r')
		default:
			encoded = append(encoded, value)
		}
	}
	return encoded, nil
}

func splitPlanLogText(data []byte) []string {
	var fragments []string
	for len(data) > 0 {
		end := min(executorPlanReportChunkBytes, len(data))
		for end > 0 && !utf8.Valid(data[:end]) {
			end--
		}
		if end == 0 {
			return nil
		}
		fragments = append(fragments, string(data[:end]))
		data = data[end:]
	}
	return fragments
}

// DecodePlanExecutionReportChunks 严格重组同一 digest-bound report 的连续普通文本记录。
func DecodePlanExecutionReportChunks(chunks []string) (PlanExecutionReport, error) {
	return decodePlanExecutionReportChunks(chunks, nil)
}

// DecodePlanExecutionReport 解码由换行分隔的普通文本报告记录。
func DecodePlanExecutionReport(text string) (PlanExecutionReport, error) {
	if text == "" {
		return PlanExecutionReport{}, errors.New("plan report text is empty")
	}
	return DecodePlanExecutionReportChunks(strings.Split(strings.TrimSuffix(text, "\n"), "\n"))
}

// DecodePlanExecutionReportChunksForGateSet 将 worker 报告绑定到 coordinator 冻结的 gate 集合。
func DecodePlanExecutionReportChunksForGateSet(chunks []string, expected []GateID) (PlanExecutionReport, error) {
	if len(expected) == 0 {
		return PlanExecutionReport{}, errors.New("expected report gate set is required")
	}
	return decodePlanExecutionReportChunks(chunks, slices.Clone(expected))
}

// decodePlanExecutionReportChunks 解析记录并验证摘要、报告头和预期 gate 集合。
func decodePlanExecutionReportChunks(chunks []string, expected []GateID) (PlanExecutionReport, error) {
	records, reportID, reportDigest, err := parsePlanReportRecords(chunks)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	report, err := decodePlanReportRecords(records)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	if digestPlanExecutionReport(report) != reportDigest || strings.TrimPrefix(reportDigest, "sha256:")[:32] != reportID {
		return PlanExecutionReport{}, errors.New("plan report digest does not match reassembled text")
	}
	if err := validatePlanExecutionReportHeader(report); err != nil {
		return PlanExecutionReport{}, err
	}
	if err := validatePlanExecutionReportGates(report, expected); err != nil {
		return PlanExecutionReport{}, err
	}
	return report, nil
}

// parsePlanReportRecords 解析并校验同一报告的连续记录帧。
func parsePlanReportRecords(chunks []string) ([]planReportRecord, string, string, error) {
	if len(chunks) == 0 || len(chunks) > executorPlanMaxReportChunks {
		return nil, "", "", errors.New("plan report record count is invalid")
	}
	records := make([]planReportRecord, 0, len(chunks))
	var reportID, reportDigest string
	for index, chunk := range chunks {
		record, err := parsePlanReportRecord(chunk)
		if err != nil {
			return nil, "", "", err
		}
		if index == 0 {
			reportID, reportDigest = record.reportID, record.digest
		}
		if record.reportID != reportID || record.digest != reportDigest || record.total != len(chunks) || record.sequence != index+1 {
			return nil, "", "", errors.New("plan report records are missing, duplicated, reordered, or mixed")
		}
		records = append(records, record)
	}
	return records, reportID, reportDigest, nil
}

// parsePlanReportRecord 解码单条文本记录及其位置和类型约束。
func parsePlanReportRecord(chunk string) (planReportRecord, error) {
	fields, err := parsePlanReportRecordFields(chunk)
	if err != nil {
		return planReportRecord{}, err
	}
	sequence, sequenceErr := parsePlanReportRecordNumber(fields[2])
	total, totalErr := parsePlanReportRecordNumber(fields[3])
	if sequenceErr != nil || totalErr != nil || sequence > total || total > executorPlanMaxReportChunks {
		return planReportRecord{}, errors.New("plan report record sequence is invalid")
	}
	if !validPlanReportRecordKind(fields[4]) || fields[5] == "" {
		return planReportRecord{}, errors.New("plan report record type is invalid")
	}
	return planReportRecord{
		reportID: fields[0],
		digest:   fields[1],
		sequence: sequence,
		total:    total,
		kind:     fields[4],
		payload:  fields[5],
	}, nil
}

// parsePlanReportRecordFields 校验记录行边界和固定头字段。
func parsePlanReportRecordFields(chunk string) ([]string, error) {
	chunk = strings.TrimSuffix(chunk, "\n")
	if len(chunk)+1 > executorPlanReportMaxLineBytes || strings.Contains(chunk, "\n") {
		return nil, errors.New("plan report record line is invalid")
	}
	if !strings.HasPrefix(chunk, ExecutorPlanReportChunkPrefix) {
		return nil, errors.New("plan report record prefix is invalid")
	}
	body := strings.TrimPrefix(chunk, ExecutorPlanReportChunkPrefix)
	fields := strings.SplitN(body, " ", 6)
	if len(fields) != 6 || len(fields[0]) != 32 || !digestPattern.MatchString(fields[1]) {
		return nil, errors.New("plan report record header is invalid")
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return nil, errors.New("plan report record id is invalid")
	}
	return fields, nil
}

func validPlanReportRecordKind(value string) bool {
	switch value {
	case planReportHeaderRecord, planReportGateRecord, planReportLogRecord, planReportTimingRecord, planReportEndRecord:
		return true
	default:
		return false
	}
}

func parsePlanReportRecordNumber(value string) (int, error) {
	if len(value) != 6 || value == "000000" {
		return 0, errors.New("plan report record number is invalid")
	}
	return strconv.Atoi(value)
}

// decodePlanReportRecords 将有序记录还原为完整的执行报告。
func decodePlanReportRecords(records []planReportRecord) (PlanExecutionReport, error) {
	if len(records) < 2 || records[0].kind != planReportHeaderRecord {
		return PlanExecutionReport{}, errors.New("plan report header record is missing")
	}
	report, gateCount, err := decodePlanReportHeader(records[0].payload)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	gates, cursor, err := decodePlanReportGates(records, gateCount, report.SchemaVersion)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	report.Gates = gates
	if cursor != len(records)-1 || records[cursor].kind != planReportEndRecord {
		return PlanExecutionReport{}, errors.New("plan report end record is missing or trailing records exist")
	}
	endCount, err := parseSixDigitCount(records[cursor].payload)
	if err != nil || endCount != gateCount {
		return PlanExecutionReport{}, errors.New("plan report end record is invalid")
	}
	return report, nil
}

func decodePlanReportGates(records []planReportRecord, gateCount int, schemaVersion uint32) ([]PlanGateExecution, int, error) {
	gates := make([]PlanGateExecution, 0, gateCount)
	cursor := 1
	for gateIndex := 1; gateIndex <= gateCount; gateIndex++ {
		result, next, err := decodePlanReportGate(records, cursor, gateIndex, schemaVersion)
		if err != nil {
			return nil, 0, err
		}
		gates = append(gates, result)
		cursor = next
	}
	return gates, cursor, nil
}

// decodePlanReportGate 解码一个 gate 及其后续日志记录，并推进游标。
func decodePlanReportGate(records []planReportRecord, cursor int, gateIndex int, schemaVersion uint32) (PlanGateExecution, int, error) {
	if cursor >= len(records) || records[cursor].kind != planReportGateRecord {
		return PlanGateExecution{}, 0, errors.New("plan report gate record is missing")
	}
	result, logBytes, logChunks, timingCount, err := decodePlanGateRecord(records[cursor].payload, gateIndex, schemaVersion)
	if err != nil {
		return PlanGateExecution{}, 0, err
	}
	log, consumed, err := decodePlanLogRecords(records[cursor+1:], gateIndex, logChunks)
	if err != nil {
		return PlanGateExecution{}, 0, err
	}
	if len(log) != logBytes || digestPlanLog(log) != result.LogDigest {
		return PlanGateExecution{}, 0, errors.New("plan report log length or digest is invalid")
	}
	result.Log = log
	timings, timingRecords, err := decodePlanTimingRecords(records[cursor+1+consumed:], gateIndex, timingCount)
	if err != nil {
		return PlanGateExecution{}, 0, err
	}
	result.TestTimings = timings
	return result, cursor + 1 + consumed + timingRecords, nil
}

// decodePlanReportHeader 解码报告元数据和 gate 总数。
func decodePlanReportHeader(payload string) (PlanExecutionReport, int, error) {
	fields := strings.Fields(payload)
	if len(fields) != 4 || strings.Join(fields, " ") != payload {
		return PlanExecutionReport{}, 0, errors.New("plan report header payload is invalid")
	}
	schema, err := strconv.ParseUint(fields[0], 10, 32)
	if err != nil {
		return PlanExecutionReport{}, 0, errors.New("plan report schema is invalid")
	}
	gateCount, err := parseSixDigitCount(fields[3])
	if err != nil || gateCount == 0 || gateCount > 64 {
		return PlanExecutionReport{}, 0, errors.New("plan report gate count is invalid")
	}
	return PlanExecutionReport{
		SchemaVersion: uint32(schema),
		Profile:       Profile(fields[1]),
		PlanDigest:    fields[2],
	}, gateCount, nil
}

// decodePlanGateRecord 解码单个 gate 记录及其日志元数据。
func decodePlanGateRecord(payload string, expectedIndex int, schemaVersion uint32) (PlanGateExecution, int, int, int, error) {
	fields, err := parsePlanGateRecordFields(payload, schemaVersion)
	if err != nil {
		return PlanGateExecution{}, 0, 0, 0, err
	}
	if err := validatePlanGateRecordIndex(fields[0], expectedIndex); err != nil {
		return PlanGateExecution{}, 0, 0, 0, err
	}
	exitCode, err := parsePlanGateExitCode(fields[3])
	if err != nil {
		return PlanGateExecution{}, 0, 0, 0, errors.New("plan report gate exit code is invalid")
	}
	startedAt, completedAt, err := parsePlanGateTimes(fields[4], fields[5])
	if err != nil {
		return PlanGateExecution{}, 0, 0, 0, err
	}
	argvDigest, logDigest, err := parsePlanGateDigests(fields[6], fields[7])
	if err != nil {
		return PlanGateExecution{}, 0, 0, 0, err
	}
	logBytes, logChunks, err := parsePlanGateLogMetadata(fields[8], fields[9])
	if err != nil {
		return PlanGateExecution{}, 0, 0, 0, err
	}
	timingCount, err := parsePlanGateTimingCount(fields, schemaVersion)
	if err != nil {
		return PlanGateExecution{}, 0, 0, 0, err
	}
	return PlanGateExecution{
		GateID:      GateID(fields[1]),
		Status:      ResultStatus(fields[2]),
		ExitCode:    exitCode,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		ArgvDigest:  argvDigest,
		LogDigest:   logDigest,
	}, logBytes, logChunks, timingCount, nil
}

func validatePlanGateRecordIndex(value string, expected int) error {
	index, err := parseSixDigitCount(value)
	if err != nil || index != expected {
		return errors.New("plan report gate index is invalid")
	}
	return nil
}

func parsePlanGateTimingCount(fields []string, schemaVersion uint32) (int, error) {
	if schemaVersion < 2 {
		return 0, nil
	}
	count, err := parseSixDigitCountAllowZero(fields[10])
	if err != nil || count > executorPlanMaxReportChunks {
		return 0, errors.New("plan report test timing count is invalid")
	}
	return count, nil
}

func parsePlanGateRecordFields(payload string, schemaVersion uint32) ([]string, error) {
	fields := strings.Fields(payload)
	expected := 10
	if schemaVersion >= 2 {
		expected = 11
	}
	if len(fields) != expected || strings.Join(fields, " ") != payload {
		return nil, errors.New("plan report gate payload is invalid")
	}
	return fields, nil
}

func parsePlanGateExitCode(value string) (int, error) {
	return strconv.Atoi(value)
}

func parsePlanGateTimes(startedAt string, completedAt string) (time.Time, time.Time, error) {
	started, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("plan report gate start time is invalid")
	}
	completed, err := time.Parse(time.RFC3339Nano, completedAt)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("plan report gate completion time is invalid")
	}
	return started, completed, nil
}

func parsePlanGateDigests(argvDigest string, logDigest string) (string, string, error) {
	if argvDigest == "-" {
		argvDigest = ""
	} else if !digestPattern.MatchString(argvDigest) {
		return "", "", errors.New("plan report argv digest is invalid")
	}
	if !digestPattern.MatchString(logDigest) {
		return "", "", errors.New("plan report log digest is invalid")
	}
	return argvDigest, logDigest, nil
}

// parsePlanGateLogMetadata 校验日志字节数和日志记录数。
func parsePlanGateLogMetadata(byteCount string, recordCount string) (int, int, error) {
	logBytes, err := strconv.Atoi(byteCount)
	if err != nil || logBytes < 0 || logBytes > executorPlanMaxLogBytes {
		return 0, 0, errors.New("plan report log byte count is invalid")
	}
	logChunks, err := parseSixDigitCountAllowZero(recordCount)
	if err != nil || logChunks > executorPlanMaxReportChunks {
		return 0, 0, errors.New("plan report log record count is invalid")
	}
	return logBytes, logChunks, nil
}

// decodePlanLogRecords 重组并解码一个 gate 的连续日志文本记录。
func decodePlanLogRecords(records []planReportRecord, gateIndex int, count int) ([]byte, int, error) {
	if count == 0 {
		return nil, 0, nil
	}
	encoded := make([]byte, 0)
	for index := 1; index <= count; index++ {
		if index > len(records) || records[index-1].kind != planReportLogRecord {
			return nil, 0, errors.New("plan report log record is missing")
		}
		fragment, err := decodePlanLogRecord(records[index-1].payload, gateIndex, index, count)
		if err != nil {
			return nil, 0, err
		}
		encoded = append(encoded, fragment...)
	}
	log, err := decodePlanLogText(encoded)
	if err != nil {
		return nil, 0, err
	}
	return log, count, nil
}

func decodePlanLogRecord(payload string, gateIndex int, fragmentIndex int, fragmentCount int) (string, error) {
	fields, err := parsePlanLogRecordFields(payload)
	if err != nil {
		return "", err
	}
	if err := validatePlanLogRecordSequence(fields, gateIndex, fragmentIndex, fragmentCount); err != nil {
		return "", err
	}
	if !utf8.ValidString(fields[3]) || strings.ContainsAny(fields[3], "\r\n\x00") {
		return "", errors.New("plan report log text is invalid")
	}
	return fields[3], nil
}

func parsePlanLogRecordFields(payload string) ([]string, error) {
	fields := strings.SplitN(payload, " ", 4)
	if len(fields) != 4 || fields[3] == "" {
		return nil, errors.New("plan report log payload is invalid")
	}
	return fields, nil
}

// validatePlanLogRecordSequence 校验日志片段归属和连续位置。
func validatePlanLogRecordSequence(fields []string, gateIndex int, fragmentIndex int, fragmentCount int) error {
	observedGate, gateErr := parseSixDigitCount(fields[0])
	observedIndex, indexErr := parseSixDigitCount(fields[1])
	observedCount, countErr := parseSixDigitCount(fields[2])
	if gateErr != nil || indexErr != nil || countErr != nil || observedGate != gateIndex || observedIndex != fragmentIndex || observedCount != fragmentCount {
		return errors.New("plan report log sequence is invalid")
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

// decodePlanLogText 还原记录内转义并拒绝不安全的日志字节。
func decodePlanLogText(encoded []byte) ([]byte, error) {
	decoded := make([]byte, 0, len(encoded))
	for index := 0; index < len(encoded); index++ {
		if encoded[index] != '\\' {
			decoded = append(decoded, encoded[index])
			continue
		}
		index++
		if index == len(encoded) {
			return nil, errors.New("plan report log text ends with an escape")
		}
		switch encoded[index] {
		case '\\':
			decoded = append(decoded, '\\')
		case 'n':
			decoded = append(decoded, '\n')
		case 'r':
			decoded = append(decoded, '\r')
		default:
			return nil, errors.New("plan report log text contains an invalid escape")
		}
	}
	if !utf8.Valid(decoded) || bytes.IndexByte(decoded, 0) >= 0 {
		return nil, errors.New("decoded plan report log is not NUL-free UTF-8 text")
	}
	return decoded, nil
}

func parseSixDigitCount(value string) (int, error) {
	return parsePlanReportRecordNumber(value)
}

func parseSixDigitCountAllowZero(value string) (int, error) {
	if len(value) != 6 {
		return 0, errors.New("plan report count is invalid")
	}
	return strconv.Atoi(value)
}

// digestPlanExecutionReport 对完整执行报告生成顺序稳定的内容摘要。
func digestPlanExecutionReport(report PlanExecutionReport) string {
	data := make([]byte, 0, 512)
	data = appendPlanReportField(data, "schema", strconv.FormatUint(uint64(report.SchemaVersion), 10))
	data = appendPlanReportField(data, "profile", string(report.Profile))
	data = appendPlanReportField(data, "plan-digest", report.PlanDigest)
	data = appendPlanReportField(data, "gate-count", strconv.Itoa(len(report.Gates)))
	for index, result := range report.Gates {
		data = appendPlanReportField(data, "gate-index", strconv.Itoa(index+1))
		data = appendPlanReportField(data, "gate-id", string(result.GateID))
		data = appendPlanReportField(data, "status", string(result.Status))
		data = appendPlanReportField(data, "exit-code", strconv.Itoa(result.ExitCode))
		data = appendPlanReportField(data, "started-at", result.StartedAt.Format(time.RFC3339Nano))
		data = appendPlanReportField(data, "completed-at", result.CompletedAt.Format(time.RFC3339Nano))
		data = appendPlanReportField(data, "argv-digest", result.ArgvDigest)
		data = appendPlanReportField(data, "log-digest", result.LogDigest)
		data = appendPlanReportField(data, "log-bytes", strconv.Itoa(len(result.Log)))
		data = append(data, result.Log...)
		data = append(data, '\n')
		if report.SchemaVersion >= 2 {
			data = appendPlanReportField(data, "test-timing-count", strconv.Itoa(len(result.TestTimings)))
			for _, timing := range result.TestTimings {
				data = appendPlanReportField(data, "test-name", timing.Name)
				data = appendPlanReportField(data, "test-status", string(timing.Status))
				data = appendPlanReportField(data, "test-duration-ms", strconv.FormatInt(timing.DurationMS, 10))
			}
		}
	}
	return digestPlanLog(data)
}

func appendPlanReportField(destination []byte, key string, value string) []byte {
	destination = append(destination, key...)
	destination = append(destination, ' ')
	destination = append(destination, value...)
	return append(destination, '\n')
}

// validatePlanExecutionReportHeader 校验版本、profile 和计划摘要。
func validatePlanExecutionReportHeader(report PlanExecutionReport) error {
	if (report.SchemaVersion != 1 && report.SchemaVersion != executorPlanReportSchemaVersion) ||
		report.Profile.Validate() != nil || !digestPattern.MatchString(report.PlanDigest) {
		return errors.New("plan report header is invalid")
	}
	return nil
}

// validatePlanExecutionReportGates 验证完整 canonical plan 或单个 canonical shard 的精确结果集。
func validatePlanExecutionReportGates(report PlanExecutionReport, expected []GateID) error {
	observed := make([]GateID, len(report.Gates))
	for index, result := range report.Gates {
		observed[index] = result.GateID
	}
	if err := validatePlanReportGateSet(report.Profile, observed, expected); err != nil {
		return err
	}
	for _, result := range report.Gates {
		if !validPlanGateResult(result, report.SchemaVersion) {
			return errors.New("plan gate result is invalid")
		}
	}
	return nil
}

// validatePlanReportGateSet 保持协调端冻结集合或 canonical 分片集合的精确匹配。
func validatePlanReportGateSet(profile Profile, observed []GateID, expected []GateID) error {
	if len(expected) != 0 {
		if err := validateExpectedPlanReportGateSet(profile, expected); err != nil {
			return err
		}
		if !slices.Equal(observed, expected) {
			return errors.New("plan report does not match the coordinator-frozen gate set")
		}
		return nil
	}
	if slices.Equal(observed, requiredGateIDs(profile)) {
		return nil
	}
	if err := validateCanonicalReportShardGateIDs(profile, observed); err != nil {
		return errors.New("plan report does not contain a canonical plan or shard gate set")
	}
	return nil
}

// validateExpectedPlanReportGateSet 拒绝 coordinator 集合中的未知、release 或重复 workload。
func validateExpectedPlanReportGateSet(profile Profile, expected []GateID) error {
	requiredSet := make(map[GateID]struct{}, len(requiredGateIDs(profile)))
	for _, id := range requiredGateIDs(profile) {
		requiredSet[id] = struct{}{}
	}
	seen := make(map[GateID]struct{}, len(expected))
	for _, id := range expected {
		parent, err := workloadParentGateID(string(id))
		if err != nil {
			return err
		}
		if _, ok := requiredSet[parent]; !ok || parent == GateIDReleaseLayeredCheck {
			return fmt.Errorf("expected report gate %q is not required by profile %q", id, profile)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("expected report gate %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// validPlanGateResult 仅接受有界文本证据、单调时钟和有效退出状态。
func validPlanGateResult(result PlanGateExecution, schemaVersion uint32) bool {
	return validPlanGateTestTimings(result.TestTimings, schemaVersion) &&
		validPlanGateLog(result) &&
		validPlanGateTimeRange(result) &&
		validPlanGateExit(result.Status, result.ExitCode)
}

// validPlanGateLog 校验日志边界、文本编码和内容摘要。
func validPlanGateLog(result PlanGateExecution) bool {
	return len(result.Log) <= executorPlanMaxLogBytes &&
		utf8.Valid(result.Log) &&
		bytes.IndexByte(result.Log, 0) < 0 &&
		result.LogDigest == digestPlanLog(result.Log) &&
		(result.ArgvDigest == "" || digestPattern.MatchString(result.ArgvDigest))
}

func validPlanGateTimeRange(result PlanGateExecution) bool {
	return !result.StartedAt.IsZero() &&
		!result.CompletedAt.IsZero() &&
		!result.CompletedAt.Before(result.StartedAt)
}

func validPlanGateTestTimings(timings []GoTestTiming, schemaVersion uint32) bool {
	if schemaVersion == 1 {
		return len(timings) == 0
	}
	return testtiming.ValidateList(timings, executorPlanMaxReportChunks) == nil
}

// validPlanGateExit 校验状态与执行器退出码的稳定组合。
func validPlanGateExit(status ResultStatus, exitCode int) bool {
	switch status {
	case ResultStatusPassed:
		return exitCode == 0
	case ResultStatusFailed:
		return exitCode > 0
	case ResultStatusCancelled, ResultStatusTimeout:
		return exitCode == -1
	default:
		return false
	}
}

type boundedPlanLog struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

// newBoundedPlanLog 创建只保留末尾诊断窗口的并发安全日志。
func newBoundedPlanLog(limit int) *boundedPlanLog { return &boundedPlanLog{limit: limit} }

// Write 保留输入长度语义，并以固定内存保留最新日志字节。
func (log *boundedPlanLog) Write(value []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	written := len(value)
	if log.limit <= 0 {
		return written, nil
	}
	if len(value) >= log.limit {
		log.data = append(log.data[:0], value[len(value)-log.limit:]...)
		return written, nil
	}
	overflow := len(log.data) + len(value) - log.limit
	if overflow > 0 {
		copy(log.data, log.data[overflow:])
		log.data = log.data[:len(log.data)-overflow]
	}
	log.data = append(log.data, value...)
	return written, nil
}

// Bytes 返回最多固定行数的最新 UTF-8 诊断窗口。
func (log *boundedPlanLog) Bytes() []byte {
	log.mu.Lock()
	defer log.mu.Unlock()
	data := bytes.Clone(log.data)
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	return tailPlanLogLines(data, executorPlanMaxLogLines)
}

// tailPlanLogLines 返回不超过给定行数的日志尾部。
func tailPlanLogLines(data []byte, limit int) []byte {
	if len(data) == 0 || limit <= 0 {
		return nil
	}
	lineCount := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		lineCount++
	}
	drop := lineCount - limit
	if drop <= 0 {
		return data
	}
	for index, value := range data {
		if value != '\n' {
			continue
		}
		drop--
		if drop == 0 {
			return data[index+1:]
		}
	}
	return data
}
