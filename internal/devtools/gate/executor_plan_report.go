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
	"time"
	"unicode/utf8"
)

const (
	planReportHeaderRecord          = "H"
	planReportCompileCount          = "D"
	planReportCompileRecord         = "C"
	planReportCompileWorkloadRecord = "W"
	planReportCompileErrorRecord    = "X"
	planReportEndRecord             = "E"
)

type planReportRecord struct {
	reportID string
	digest   string
	sequence int
	total    int
	kind     string
	payload  string
}

// PlanExecutionReportRecordLimit 返回 gate 集允许的最大报告记录数，并受 ECI 日志传输合同封顶。
func PlanExecutionReportRecordLimit(gateCount int) (int, error) {
	if gateCount <= 0 || gateCount > 999999 {
		return 0, errors.New("plan report gate count is invalid")
	}
	perGate := 2 + executorPlanMaxLogRecords + executorPlanMaxTimingRecords
	return min(2+gateCount*perGate, executorPlanMaxTransportRecords), nil
}

func planExecutionReportRecordLimit(gateCount, compileCount int) (int, error) {
	if compileCount < 0 {
		return 0, errors.New("plan report compile group count is invalid")
	}
	gateLimit, err := PlanExecutionReportRecordLimit(gateCount)
	if err != nil {
		return 0, err
	}
	if compileCount == 0 {
		return gateLimit, nil
	}
	// Compile-group record 各自受限且可能被分片；transport hard cap 仍是最终 authority。
	_ = gateLimit
	return executorPlanMaxTransportRecords, nil
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
	normalizedReport, err := normalizeCompileGroupReportLogs(report)
	if err != nil {
		return nil, err
	}
	report = normalizedReport
	if err := validatePlanExecutionReportHeader(report); err != nil {
		return nil, err
	}
	if err := validatePlanExecutionReportResults(report); err != nil {
		return nil, err
	}
	records, err := encodePlanReportRecords(report)
	if err != nil {
		return nil, err
	}
	recordLimit, err := planExecutionReportRecordLimit(len(report.Gates), len(report.CompileGroupExecutions))
	if err != nil {
		return nil, err
	}
	if len(records) == 0 || len(records) > recordLimit {
		return nil, errors.New("plan report exceeds record count limit")
	}
	digest := digestPlanExecutionReport(report)
	return framePlanReportRecords(records, digest)
}

// validatePlanExecutionReportResults 校验报告内每个 gate 的状态、摘要与 schema 约束。
func validatePlanExecutionReportResults(report PlanExecutionReport) error {
	for _, result := range report.Gates {
		if err := ValidatePlanGateTimingEvidence(result); err != nil {
			return fmt.Errorf("plan gate result %q timing evidence is invalid: %w", result.GateID, err)
		}
		if !validPlanGateResult(result, report.SchemaVersion) {
			return fmt.Errorf(
				"plan gate result %q is invalid (status=%s exit_code=%d log_bytes=%d log_utf8=%t log_nul=%t log_digest=%t time_range=%t test_timings=%t)",
				result.GateID,
				result.Status,
				result.ExitCode,
				len(result.Log),
				utf8.Valid(result.Log),
				bytes.IndexByte(result.Log, 0) >= 0,
				result.LogDigest == digestPlanLog(result.Log),
				validPlanGateTimeRange(result),
				validPlanGateTestTimings(result.TestTimings, report.SchemaVersion),
			)
		}
	}
	if err := validateCompileGroupExecutionList(report.CompileGroupExecutions); err != nil {
		return err
	}
	return validateCompileGroupReportLogBudget(report)
}

// framePlanReportRecords 为普通文本记录添加报告身份、顺序和总字节数边界。
func framePlanReportRecords(records []string, digest string) ([]string, error) {
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
	header, err := encodePlanReportHeader(report)
	if err != nil {
		return nil, err
	}
	records := []string{header}
	if len(report.CompileGroupExecutions) > 0 {
		records = append(records, encodePlanCompileGroupCountRecord(len(report.CompileGroupExecutions)))
	}
	dictionaryRecords, err := encodePlanGateDictionary(report.Gates)
	if err != nil {
		return nil, err
	}
	records = append(records, dictionaryRecords...)
	for index, result := range report.Gates {
		gateRecords, gateErr := encodePlanGateReportRecords(index+1, result, report.SchemaVersion)
		if gateErr != nil {
			return nil, gateErr
		}
		records = append(records, gateRecords...)
	}
	for index, execution := range report.CompileGroupExecutions {
		compileRecords, compileErr := encodePlanCompileGroupRecords(index+1, execution)
		if compileErr != nil {
			return nil, compileErr
		}
		records = append(records, compileRecords...)
	}
	records = append(records, fmt.Sprintf("%s %06d", planReportEndRecord, len(report.Gates)))
	return records, nil
}

// encodePlanGateReportRecords 保持 gate、profile、日志与 timing 的协议顺序，避免解码端出现歧义。
func encodePlanGateReportRecords(index int, result PlanGateExecution, schemaVersion uint32) ([]string, error) {
	if schemaVersion != ExecutorPlanReportSchemaVersion {
		return nil, errors.New("plan report gate schema is unsupported")
	}
	return encodePackedPlanGateRecords(index, result)
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
	recordLimit, err := planReportDecodeRecordLimit(expected)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	records, reportID, reportDigest, err := parsePlanReportRecords(chunks, recordLimit)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	report, err := decodePlanReportRecords(records)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	if err := validateDecodedPlanReportFrame(records, reportID, reportDigest, report); err != nil {
		return PlanExecutionReport{}, err
	}
	if err := validatePlanExecutionReportHeader(report); err != nil {
		return PlanExecutionReport{}, err
	}
	if err := validatePlanExecutionReportGates(report, expected); err != nil {
		return PlanExecutionReport{}, err
	}
	return report, nil
}

// planReportDecodeRecordLimit 以冻结 gate 集收窄日志扫描上限；未知集合只使用传输硬上限。
func planReportDecodeRecordLimit(expected []GateID) (int, error) {
	if len(expected) == 0 {
		return executorPlanMaxTransportRecords, nil
	}
	// Compile-group cardinality 携带在 strict D record 中，直到 framing 后才确定；
	// 首次 pass 使用 global transport cap，解码后再应用精确的 gate+group budget。
	return executorPlanMaxTransportRecords, nil
}

// validateDecodedPlanReportFrame 将重组报告绑定到精确 gate 预算和内容摘要。
func validateDecodedPlanReportFrame(
	records []planReportRecord,
	reportID string,
	reportDigest string,
	report PlanExecutionReport,
) error {
	exactRecordLimit, err := planExecutionReportRecordLimit(len(report.Gates), len(report.CompileGroupExecutions))
	if err != nil || len(records) > exactRecordLimit {
		return errors.New("plan report record count exceeds gate-set budget")
	}
	if digestPlanExecutionReport(report) != reportDigest ||
		strings.TrimPrefix(reportDigest, "sha256:")[:32] != reportID {
		return errors.New("plan report digest does not match reassembled text")
	}
	return nil
}

// parsePlanReportRecords 解析并校验同一报告的连续记录帧。
func parsePlanReportRecords(chunks []string, recordLimit int) ([]planReportRecord, string, string, error) {
	if !validPlanReportCollectionSize(len(chunks), recordLimit) {
		return nil, "", "", errors.New("plan report record count is invalid")
	}
	records := make([]planReportRecord, 0, len(chunks))
	var reportID, reportDigest string
	totalBytes := 0
	for index, chunk := range chunks {
		line := strings.TrimSuffix(chunk, "\n")
		if len(line)+1 > executorPlanReportMaxLineBytes {
			return nil, "", "", errors.New("plan report record exceeds remote log line limit")
		}
		totalBytes += len(line) + 1
		if totalBytes > executorPlanReportMaxOutputBytes {
			return nil, "", "", errors.New("plan report exceeds remote log response limit")
		}
		record, err := parsePlanReportRecord(chunk, recordLimit)
		if err != nil {
			return nil, "", "", err
		}
		if index == 0 {
			reportID, reportDigest = record.reportID, record.digest
		}
		if !samePlanReportFrame(record, reportID, reportDigest, index+1, len(chunks)) {
			return nil, "", "", errors.New("plan report records are missing, duplicated, reordered, or mixed")
		}
		records = append(records, record)
	}
	return records, reportID, reportDigest, nil
}

// validPlanReportCollectionSize 校验记录集合未超过调用方冻结的扫描预算。
func validPlanReportCollectionSize(count int, recordLimit int) bool {
	return recordLimit > 0 && count > 0 && count <= recordLimit
}

// samePlanReportFrame 校验一条记录属于同一报告且序号连续。
func samePlanReportFrame(record planReportRecord, reportID string, reportDigest string, sequence int, total int) bool {
	return record.reportID == reportID &&
		record.digest == reportDigest &&
		record.total == total &&
		record.sequence == sequence
}

// parsePlanReportRecord 解码单条文本记录及其位置和类型约束。
func parsePlanReportRecord(chunk string, recordLimit int) (planReportRecord, error) {
	fields, err := parsePlanReportRecordFields(chunk)
	if err != nil {
		return planReportRecord{}, err
	}
	sequence, sequenceErr := parsePlanReportRecordNumber(fields[2])
	total, totalErr := parsePlanReportRecordNumber(fields[3])
	if sequenceErr != nil || totalErr != nil || sequence > total || total > recordLimit {
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
	case planReportHeaderRecord, planReportCompileCount, planReportCompileRecord, planReportCompileWorkloadRecord, planReportCompileErrorRecord, planReportEndRecord, planReportGateDictionaryCountRecord, planReportGateDictionaryRecord, planReportPackedGateRecord, planReportPackedGateLogRecord, planReportPackedGateTimingRecord:
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
	decoded, err := decodePlanReportBody(records, gateCount, report.SchemaVersion)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	report.Gates, report.CompileGroupExecutions = decoded.gates, decoded.compileGroups
	if decoded.cursor != len(records)-1 || records[decoded.cursor].kind != planReportEndRecord {
		return PlanExecutionReport{}, errors.New("plan report end record is missing or trailing records exist")
	}
	endCount, err := parseSixDigitCount(records[decoded.cursor].payload)
	if err != nil || endCount != gateCount {
		return PlanExecutionReport{}, errors.New("plan report end record is invalid")
	}
	return report, nil
}

type decodedPlanReportBody struct {
	gates         []PlanGateExecution
	compileGroups []CompileGroupExecution
	cursor        int
}

func decodePlanReportBody(records []planReportRecord, gateCount int, schemaVersion uint32) (decodedPlanReportBody, error) {
	compileCount, cursor, err := decodePlanCompileGroupCount(records)
	if err != nil {
		return decodedPlanReportBody{}, err
	}
	dictionary, cursor, err := decodePlanGateDictionary(records, cursor, gateCount)
	if err != nil {
		return decodedPlanReportBody{}, err
	}
	gates, cursor, err := decodePlanReportGatesFrom(records, cursor, gateCount, schemaVersion, dictionary)
	if err != nil {
		return decodedPlanReportBody{}, err
	}
	compileGroups, cursor, err := decodePlanCompileGroupRecords(records, cursor, compileCount)
	if err != nil {
		return decodedPlanReportBody{}, err
	}
	return decodedPlanReportBody{gates: gates, compileGroups: compileGroups, cursor: cursor}, nil
}

func decodePlanReportGatesFrom(records []planReportRecord, start int, gateCount int, schemaVersion uint32, dictionary []GateID) ([]PlanGateExecution, int, error) {
	gates := make([]PlanGateExecution, 0, gateCount)
	cursor := start
	for gateIndex := 1; gateIndex <= gateCount; gateIndex++ {
		result, next, err := decodePlanReportGate(records, cursor, gateIndex, schemaVersion, dictionary)
		if err != nil {
			return nil, 0, err
		}
		gates = append(gates, result)
		cursor = next
	}
	return gates, cursor, nil
}

// decodePlanReportGate 解码一个 dictionary-indexed packed gate，并推进游标。
func decodePlanReportGate(records []planReportRecord, cursor int, gateIndex int, schemaVersion uint32, dictionary []GateID) (PlanGateExecution, int, error) {
	if schemaVersion != ExecutorPlanReportSchemaVersion {
		return PlanGateExecution{}, 0, errors.New("plan report gate schema is unsupported")
	}
	return decodePackedPlanGate(records, cursor, gateIndex, dictionary)
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
	data = appendPlanReportField(data, "agent-token-digest", report.AgentTokenDigest)
	data = appendPlanReportField(data, "execution-status", string(report.ExecutionOutcome.Status))
	data = appendPlanReportField(data, "execution-exit-code", strconv.Itoa(report.ExecutionOutcome.ExitCode))
	data = appendPlanReportField(data, "execution-reason-code", string(report.ExecutionOutcome.ReasonCode))
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
		data = appendExecutionProfileDigest(data, result.ExecutionProfile)
		data = appendPlanReportField(data, "test-timing-count", strconv.Itoa(len(result.TestTimings)))
		for _, timing := range result.TestTimings {
			data = appendPlanReportField(data, "test-name", timing.Name)
			data = appendPlanReportField(data, "test-status", string(timing.Status))
			data = appendPlanReportField(data, "test-duration-ms", strconv.FormatInt(timing.DurationMS, 10))
		}
	}
	data = appendPlanReportField(data, "compile-group-count", strconv.Itoa(len(report.CompileGroupExecutions)))
	data = appendCompileGroupLogBudgetDigest(data, len(report.CompileGroupExecutions))
	for _, execution := range report.CompileGroupExecutions {
		data = appendCompileGroupExecutionDigest(data, execution)
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
	if validatePlanExecutionReportSchema(uint64(report.SchemaVersion)) != nil ||
		report.Profile.Validate() != nil || !digestPattern.MatchString(report.PlanDigest) {
		return errors.New("plan report header is invalid")
	}
	if err := report.ExecutionOutcome.Validate(); err != nil {
		return fmt.Errorf("plan report execution outcome is invalid: %w", err)
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
		if err := ValidatePlanGateTimingEvidence(result); err != nil {
			return fmt.Errorf("plan gate result %q timing evidence is invalid: %w", result.GateID, err)
		}
		if !validPlanGateResult(result, report.SchemaVersion) {
			return errors.New("plan gate result is invalid")
		}
	}
	return validateCompileGroupReportLogBudget(report)
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
	return errors.New("plan report shard set must be coordinator-frozen")
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
