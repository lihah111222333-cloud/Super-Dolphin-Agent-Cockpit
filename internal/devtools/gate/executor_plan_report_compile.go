package gate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func validateCompileGroupExecutionList(executions []CompileGroupExecution) error {
	seen := make(map[string]struct{}, len(executions))
	for index, execution := range executions {
		if err := execution.Validate(); err != nil {
			return fmt.Errorf("compile_group_executions[%d]: %w", index, err)
		}
		if _, duplicate := seen[execution.GroupID]; duplicate {
			return fmt.Errorf("compile_group_executions[%d] duplicate group %q", index, execution.GroupID)
		}
		seen[execution.GroupID] = struct{}{}
	}
	return nil
}

func encodePlanCompileGroupCountRecord(count int) string {
	return fmt.Sprintf("%s %06d", planReportCompileCount, count)
}

// encodePlanCompileGroupRecords 使用有界明文 C/W/X 记录编码 compile ledger。
func encodePlanCompileGroupRecords(index int, execution CompileGroupExecution) ([]string, error) {
	errorText, err := encodePlanLogText([]byte(execution.ErrorText))
	if err != nil {
		return nil, err
	}
	errorFragments := splitPlanLogText(errorText)
	artifactDigest := execution.ArtifactSHA256
	if artifactDigest == "" {
		artifactDigest = "-"
	}
	commandDigest := execution.CompileCommandDigest
	if commandDigest == "" {
		commandDigest = "-"
	}
	header := fmt.Sprintf("%s %06d %s %s %s %d %d %d %s %d %d %d %d %s %d %s %s %s %06d %d %06d",
		planReportCompileRecord, index, execution.GroupID, execution.ArtifactKey, execution.PackageTarget,
		execution.StartedAtUnixMS, execution.CompletedAtUnixMS, execution.DurationMS, artifactDigest,
		execution.ArtifactSize, execution.CacheHits, execution.CacheMisses, execution.CachePuts,
		execution.Status, execution.ExitCode, commandDigest, execution.ProfileDigest,
		execution.ResourceClassID, len(execution.WorkloadIDs), len(execution.ErrorText), len(errorFragments))
	records := []string{header}
	workloadRecords, err := encodePlanCompileGroupWorkloadRecords(index, execution.WorkloadIDs)
	if err != nil {
		return nil, err
	}
	records = append(records, workloadRecords...)
	for fragmentIndex, fragment := range splitPlanLogText(errorText) {
		records = append(records, fmt.Sprintf("%s %06d %06d %06d %s", planReportCompileErrorRecord, index, fragmentIndex+1, len(errorFragments), fragment))
	}
	return records, nil
}

// encodePlanCompileGroupWorkloadRecords 将多个有序 workload ID 打包到一条 W 记录，
// 同时按最终 framed line 的固定上限切分，避免单个 compile group 的 selector 数量
// 线性膨胀 transport records。每条记录携带起始序号、总数和本批数量，供解码端
// 严格验证连续覆盖。
func encodePlanCompileGroupWorkloadRecords(index int, workloadIDs []GateID) ([]string, error) {
	if len(workloadIDs) == 0 || len(workloadIDs) > executorPlanMaxTransportRecords {
		return nil, errors.New("compile group workload count is invalid")
	}
	records := make([]string, 0, len(workloadIDs))
	for start := 0; start < len(workloadIDs); {
		end := start
		for end < len(workloadIDs) {
			workloadID := workloadIDs[end]
			if err := validateCompileGroupWorkloadID(workloadID); err != nil {
				return nil, err
			}
			candidate := encodePlanCompileGroupWorkloadRecord(index, start+1, len(workloadIDs), workloadIDs[start:end+1])
			if !planReportRecordPayloadFits(candidate) {
				break
			}
			end++
		}
		if end == start {
			return nil, errors.New("compile group workload record exceeds remote log line limit")
		}
		records = append(records, encodePlanCompileGroupWorkloadRecord(index, start+1, len(workloadIDs), workloadIDs[start:end]))
		start = end
	}
	return records, nil
}

func validateCompileGroupWorkloadID(workloadID GateID) error {
	if workloadID == "" {
		return errors.New("compile group workload ID cannot be empty")
	}
	if strings.ContainsAny(string(workloadID), " \t\r\n\x00") {
		return errors.New("compile group workload ID cannot contain whitespace")
	}
	return nil
}

func encodePlanCompileGroupWorkloadRecord(index, start, total int, workloadIDs []GateID) string {
	return fmt.Sprintf("%s %06d %06d %06d %06d %s", planReportCompileWorkloadRecord, index, start, total, len(workloadIDs), strings.Join(gateIDsAsStrings(workloadIDs), " "))
}

// planReportRecordPayloadFits 按固定报告帧宽度预检记录；摘要恒为 71 字节的 sha256，
// 序号和总数恒为六位，因此在最终摘要尚未生成时也能确定行上限。
func planReportRecordPayloadFits(record string) bool {
	const reportIDBytes = 32
	const reportDigestBytes = len("sha256:") + 64
	const framingFieldsBytes = 4 + (6 * 2)
	overhead := len(ExecutorPlanReportChunkPrefix) + reportIDBytes + reportDigestBytes + framingFieldsBytes
	return overhead+len(record)+1 <= executorPlanReportMaxLineBytes
}

func decodePlanCompileGroupCount(records []planReportRecord) (int, int, error) {
	if len(records) <= 1 || records[1].kind != planReportCompileCount {
		return 0, 1, nil
	}
	count, err := parseSixDigitCount(records[1].payload)
	if err != nil || count > executorPlanMaxTransportRecords {
		return 0, 0, errors.New("plan report compile group count is invalid")
	}
	return count, 2, nil
}

func decodePlanCompileGroupRecords(records []planReportRecord, cursor, count int) ([]CompileGroupExecution, int, error) {
	if count == 0 {
		return nil, cursor, nil
	}
	executions := make([]CompileGroupExecution, 0, count)
	seenWorkloads := make(map[GateID]struct{})
	for index := 1; index <= count; index++ {
		execution, next, err := decodePlanCompileGroupRecord(records, cursor, index, seenWorkloads)
		if err != nil {
			return nil, 0, err
		}
		executions = append(executions, execution)
		cursor = next
	}
	return executions, cursor, nil
}

func decodePlanCompileGroupRecord(records []planReportRecord, cursor, expectedIndex int, seenWorkloads map[GateID]struct{}) (CompileGroupExecution, int, error) {
	fields, workloadCount, errorBytes, errorCount, err := parseCompileGroupHeader(records, cursor, expectedIndex)
	if err != nil {
		return CompileGroupExecution{}, 0, err
	}
	workloads, cursor, err := decodeCompileGroupWorkloads(records, cursor+1, expectedIndex, workloadCount, seenWorkloads)
	if err != nil {
		return CompileGroupExecution{}, 0, err
	}
	errorText, cursor, err := decodeCompileGroupError(records, cursor, expectedIndex, errorCount, errorBytes)
	if err != nil {
		return CompileGroupExecution{}, 0, err
	}
	execution, err := compileGroupExecutionFromFields(fields, workloads, errorText)
	if err != nil {
		return CompileGroupExecution{}, 0, err
	}
	return execution, cursor, nil
}

// parseCompileGroupHeader 解析单条 C 记录及其 workload/error 计数。
func parseCompileGroupHeader(records []planReportRecord, cursor, expectedIndex int) ([]string, int, int, int, error) {
	if cursor >= len(records) || records[cursor].kind != planReportCompileRecord {
		return nil, 0, 0, 0, errors.New("plan report compile group record is missing")
	}
	fields := strings.Fields(records[cursor].payload)
	if len(fields) != 20 || fields[0] != fmt.Sprintf("%06d", expectedIndex) {
		return nil, 0, 0, 0, errors.New("plan report compile group header is invalid")
	}
	workloadCount, err := parseCompileGroupCount(fields[17])
	if err != nil || workloadCount > executorPlanMaxTransportRecords {
		return nil, 0, 0, 0, errors.New("plan report compile group workload count is invalid")
	}
	errorBytes, err := parseCompileGroupBytes(fields[18])
	if err != nil {
		return nil, 0, 0, 0, err
	}
	errorCount, err := parseCompileGroupCountAllowZero(fields[19])
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return fields, workloadCount, errorBytes, errorCount, nil
}

// decodeCompileGroupWorkloads 按固定序号读取并校验打包 W 记录。
func decodeCompileGroupWorkloads(records []planReportRecord, cursor, expectedIndex, count int, seenWorkloads map[GateID]struct{}) ([]GateID, int, error) {
	if seenWorkloads == nil {
		seenWorkloads = make(map[GateID]struct{})
	}
	workloads := make([]GateID, 0, count)
	nextIndex := 1
	for nextIndex <= count {
		fields, err := parsePackedCompileGroupWorkloadRecord(records, cursor, expectedIndex, nextIndex, count)
		if err != nil {
			return nil, 0, err
		}
		if err := appendPackedCompileGroupWorkloads(&workloads, seenWorkloads, fields); err != nil {
			return nil, 0, err
		}
		nextIndex += len(fields)
		cursor++
	}
	return workloads, cursor, nil
}

func parsePackedCompileGroupWorkloadRecord(records []planReportRecord, cursor, expectedIndex, nextIndex, total int) ([]string, error) {
	if cursor >= len(records) {
		return nil, errors.New("plan report compile group workload record is missing")
	}
	record := records[cursor]
	if record.kind != planReportCompileWorkloadRecord {
		return nil, errors.New("plan report compile group workload record is missing")
	}
	fields, err := parsePackedCompileGroupWorkloadFields(record.payload)
	if err != nil {
		return nil, err
	}
	if err := validatePackedCompileGroupWorkloadFields(fields, expectedIndex, nextIndex, total); err != nil {
		return nil, err
	}
	return fields[4:], nil
}

func parsePackedCompileGroupWorkloadFields(payload string) ([]string, error) {
	fields := strings.Fields(payload)
	if len(fields) < 5 || strings.Join(fields, " ") != payload {
		return nil, errors.New("plan report compile group workload record is invalid")
	}
	return fields, nil
}

// validatePackedCompileGroupWorkloadFields 校验编译组 workload 分批记录的序号、总数与载荷边界。
func validatePackedCompileGroupWorkloadFields(fields []string, expectedIndex, nextIndex, total int) error {
	if fields[0] != fmt.Sprintf("%06d", expectedIndex) {
		return errors.New("plan report compile group workload record is invalid")
	}
	if fields[1] != fmt.Sprintf("%06d", nextIndex) {
		return errors.New("plan report compile group workload record is invalid")
	}
	if fields[2] != fmt.Sprintf("%06d", total) {
		return errors.New("plan report compile group workload record is invalid")
	}
	batchCount, err := parseCompileGroupCount(fields[3])
	if err != nil || len(fields) != 4+batchCount || nextIndex+batchCount-1 > total {
		return errors.New("plan report compile group workload record is invalid")
	}
	return nil
}

func appendPackedCompileGroupWorkloads(destination *[]GateID, seen map[GateID]struct{}, fields []string) error {
	for _, field := range fields {
		workloadID := GateID(field)
		if err := validateCompileGroupWorkloadID(workloadID); err != nil {
			return errors.New("plan report compile group workload record is invalid")
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return fmt.Errorf("plan report compile group workload %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
		*destination = append(*destination, workloadID)
	}
	return nil
}

// decodeCompileGroupError 重组 X 记录并校验原始错误字节数。
func decodeCompileGroupError(records []planReportRecord, cursor, expectedIndex, count, expectedBytes int) (string, int, error) {
	if count == 0 {
		if expectedBytes != 0 {
			return "", 0, errors.New("plan report compile group error byte count is invalid")
		}
		return "", cursor, nil
	}
	encoded := make([]byte, 0, expectedBytes)
	for index := 1; index <= count; index++ {
		if cursor >= len(records) || records[cursor].kind != planReportCompileErrorRecord {
			return "", 0, errors.New("plan report compile group error record is missing")
		}
		fields, err := parseCompileGroupErrorRecord(records[cursor].payload, expectedIndex, index, count)
		if err != nil {
			return "", 0, err
		}
		encoded = append(encoded, fields[3]...)
		cursor++
	}
	decoded, err := decodePlanLogText(encoded)
	if err != nil || len(decoded) != expectedBytes {
		return "", 0, errors.New("plan report compile group error text is invalid")
	}
	return string(decoded), cursor, nil
}

// parseCompileGroupErrorRecord 校验一条 X 记录的序号和分片数。
func parseCompileGroupErrorRecord(payload string, expectedIndex, fragment, count int) ([]string, error) {
	fields := strings.SplitN(payload, " ", 4)
	if len(fields) != 4 || fields[0] != fmt.Sprintf("%06d", expectedIndex) || fields[1] != fmt.Sprintf("%06d", fragment) || fields[2] != fmt.Sprintf("%06d", count) || fields[3] == "" {
		return nil, errors.New("plan report compile group error record is invalid")
	}
	return fields, nil
}

// compileGroupExecutionFromFields 将严格解析的 C/W/X 字段组装为 ledger。
func compileGroupExecutionFromFields(fields []string, workloads []GateID, errorText string) (CompileGroupExecution, error) {
	parsed, err := parseCompileGroupExecutionFields(fields)
	if err != nil {
		return CompileGroupExecution{}, err
	}
	artifactDigest := fields[7]
	if artifactDigest == "-" {
		artifactDigest = ""
	}
	execution := CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: fields[1], ArtifactKey: fields[2], PackageTarget: fields[3], WorkloadIDs: workloads,
		StartedAtUnixMS: parsed.started, CompletedAtUnixMS: parsed.completed, DurationMS: parsed.duration, ArtifactSHA256: artifactDigest,
		ArtifactSize: parsed.artifactSize, CacheHits: parsed.hits, CacheMisses: parsed.misses, CachePuts: parsed.puts,
		Status: ResultStatus(fields[12]), ExitCode: parsed.exitCode, CompileCommandDigest: parseCompileGroupCommandDigest(fields[14]), ProfileDigest: fields[15], ResourceClassID: fields[16], ErrorText: errorText,
	}
	if err := execution.Validate(); err != nil {
		return CompileGroupExecution{}, err
	}
	return execution, nil
}

func parseCompileGroupCommandDigest(value string) string {
	if value == "-" {
		return ""
	}
	return value
}

type parsedCompileGroupExecutionFields struct {
	started, completed, duration int64
	artifactSize                 int64
	hits, misses, puts           uint64
	exitCode                     int
}

func parseCompileGroupExecutionFields(fields []string) (parsedCompileGroupExecutionFields, error) {
	timing, err := parseCompileGroupTimingFields(fields)
	if err != nil {
		return parsedCompileGroupExecutionFields{}, err
	}
	caches, err := parseCompileGroupCacheFields(fields)
	if err != nil {
		return parsedCompileGroupExecutionFields{}, err
	}
	exitCode, err := parseCompileGroupExitCode(fields[13])
	if err != nil {
		return parsedCompileGroupExecutionFields{}, err
	}
	return parsedCompileGroupExecutionFields{
		started: timing.started, completed: timing.completed, duration: timing.duration, artifactSize: timing.artifactSize,
		hits: caches.hits, misses: caches.misses, puts: caches.puts, exitCode: exitCode,
	}, nil
}

type parsedCompileGroupTimingFields struct {
	started, completed, duration, artifactSize int64
}

func parseCompileGroupTimingFields(fields []string) (parsedCompileGroupTimingFields, error) {
	started, err := parseCompileGroupInt(fields, 4, "start time")
	if err != nil {
		return parsedCompileGroupTimingFields{}, err
	}
	completed, err := parseCompileGroupInt(fields, 5, "completion time")
	if err != nil {
		return parsedCompileGroupTimingFields{}, err
	}
	duration, err := parseCompileGroupInt(fields, 6, "duration")
	if err != nil {
		return parsedCompileGroupTimingFields{}, err
	}
	artifactSize, err := parseCompileGroupInt(fields, 8, "artifact size")
	if err != nil {
		return parsedCompileGroupTimingFields{}, err
	}
	return parsedCompileGroupTimingFields{started: started, completed: completed, duration: duration, artifactSize: artifactSize}, nil
}

type parsedCompileGroupCacheFields struct {
	hits, misses, puts uint64
}

func parseCompileGroupCacheFields(fields []string) (parsedCompileGroupCacheFields, error) {
	hits, err := parseCompileGroupUint(fields, 9, "cache hits")
	if err != nil {
		return parsedCompileGroupCacheFields{}, err
	}
	misses, err := parseCompileGroupUint(fields, 10, "cache misses")
	if err != nil {
		return parsedCompileGroupCacheFields{}, err
	}
	puts, err := parseCompileGroupUint(fields, 11, "cache puts")
	if err != nil {
		return parsedCompileGroupCacheFields{}, err
	}
	return parsedCompileGroupCacheFields{hits: hits, misses: misses, puts: puts}, nil
}

func parseCompileGroupInt(fields []string, index int, label string) (int64, error) {
	value, err := strconv.ParseInt(fields[index], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("plan report compile group %s is invalid", label)
	}
	return value, nil
}

func parseCompileGroupUint(fields []string, index int, label string) (uint64, error) {
	value, err := strconv.ParseUint(fields[index], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("plan report compile group %s are invalid", label)
	}
	return value, nil
}

func parseCompileGroupExitCode(value string) (int, error) {
	exitCode, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New("plan report compile group exit code is invalid")
	}
	return exitCode, nil
}

func parseCompileGroupCount(value string) (int, error) {
	count, err := parseSixDigitCount(value)
	if err != nil {
		return 0, errors.New("plan report compile group count is invalid")
	}
	return count, nil
}

func parseCompileGroupCountAllowZero(value string) (int, error) {
	count, err := parseSixDigitCountAllowZero(value)
	if err != nil {
		return 0, errors.New("plan report compile group count is invalid")
	}
	return count, nil
}

func parseCompileGroupBytes(value string) (int, error) {
	bytes, err := strconv.Atoi(value)
	if err != nil || bytes < 0 || bytes > compileGroupErrorTextBytes {
		return 0, errors.New("plan report compile group error byte count is invalid")
	}
	return bytes, nil
}

func appendCompileGroupExecutionDigest(destination []byte, execution CompileGroupExecution) []byte {
	destination = appendPlanReportField(destination, "compile-group-id", execution.GroupID)
	destination = appendPlanReportField(destination, "compile-group-artifact-key", execution.ArtifactKey)
	destination = appendPlanReportField(destination, "compile-group-package", execution.PackageTarget)
	destination = appendPlanReportField(destination, "compile-group-workloads", strings.Join(gateIDsAsStrings(execution.WorkloadIDs), ","))
	destination = appendPlanReportField(destination, "compile-group-started", strconv.FormatInt(execution.StartedAtUnixMS, 10))
	destination = appendPlanReportField(destination, "compile-group-completed", strconv.FormatInt(execution.CompletedAtUnixMS, 10))
	destination = appendPlanReportField(destination, "compile-group-duration", strconv.FormatInt(execution.DurationMS, 10))
	destination = appendPlanReportField(destination, "compile-group-artifact-sha256", execution.ArtifactSHA256)
	destination = appendPlanReportField(destination, "compile-group-artifact-size", strconv.FormatInt(execution.ArtifactSize, 10))
	destination = appendPlanReportField(destination, "compile-group-cache-hits", strconv.FormatUint(execution.CacheHits, 10))
	destination = appendPlanReportField(destination, "compile-group-cache-misses", strconv.FormatUint(execution.CacheMisses, 10))
	destination = appendPlanReportField(destination, "compile-group-cache-puts", strconv.FormatUint(execution.CachePuts, 10))
	destination = appendPlanReportField(destination, "compile-group-status", string(execution.Status))
	destination = appendPlanReportField(destination, "compile-group-exit-code", strconv.Itoa(execution.ExitCode))
	destination = appendPlanReportField(destination, "compile-group-error", execution.ErrorText)
	destination = appendPlanReportField(destination, "compile-group-command-digest", execution.CompileCommandDigest)
	destination = appendPlanReportField(destination, "compile-group-profile-digest", execution.ProfileDigest)
	return appendPlanReportField(destination, "compile-group-resource-class", execution.ResourceClassID)
}

func gateIDsAsStrings(ids []GateID) []string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = string(id)
	}
	return values
}
