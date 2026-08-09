package gate

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

const (
	planReportGateDictionaryCountRecord = "N"
	planReportGateDictionaryRecord      = "I"
	planReportPackedGateRecord          = "R"
	planReportPackedGateLogRecord       = "r"
	planReportPackedGateTimingRecord    = "t"
)

// encodePlanGateDictionary 将每个 selector identity 只写入一次，后续结果行只携带索引。
func encodePlanGateDictionary(gates []PlanGateExecution) ([]string, error) {
	if len(gates) == 0 {
		return nil, nil
	}
	if len(gates) > executorPlanMaxTransportRecords {
		return nil, errors.New("plan report gate dictionary count is invalid")
	}
	records := []string{fmt.Sprintf("%s %06d", planReportGateDictionaryCountRecord, len(gates))}
	seen := make(map[GateID]struct{}, len(gates))
	for _, result := range gates {
		if err := validatePackedGateID(result.GateID); err != nil {
			return nil, err
		}
		if _, duplicate := seen[result.GateID]; duplicate {
			return nil, fmt.Errorf("plan report gate %q is duplicated", result.GateID)
		}
		seen[result.GateID] = struct{}{}
	}
	for start := 0; start < len(gates); {
		end := start
		for end < len(gates) {
			candidate := encodePlanGateDictionaryRecord(start+1, len(gates), gates[start:end+1])
			if !planReportRecordPayloadFits(candidate) {
				break
			}
			end++
		}
		if end == start {
			return nil, errors.New("plan report gate dictionary record exceeds remote log line limit")
		}
		records = append(records, encodePlanGateDictionaryRecord(start+1, len(gates), gates[start:end]))
		start = end
	}
	return records, nil
}

func encodePlanGateDictionaryRecord(start, total int, gates []PlanGateExecution) string {
	ids := make([]string, len(gates))
	for index, result := range gates {
		ids[index] = string(result.GateID)
	}
	return fmt.Sprintf("%s %06d %06d %06d %s", planReportGateDictionaryRecord, start, total, len(ids), strings.Join(ids, " "))
}

func validatePackedGateID(id GateID) error {
	if id == "" || strings.ContainsAny(string(id), " \t\r\n\x00") {
		return errors.New("plan report gate identity is invalid")
	}
	return nil
}

// decodePlanGateDictionary 读取可选的 identity dictionary，并拒绝缺失、重复或乱序条目。
func decodePlanGateDictionary(records []planReportRecord, cursor, gateCount int) ([]GateID, int, error) {
	if cursor >= len(records) || records[cursor].kind != planReportGateDictionaryCountRecord {
		return nil, 0, errors.New("plan report gate dictionary record is missing")
	}
	count, err := parseSixDigitCount(records[cursor].payload)
	if err != nil || count != gateCount {
		return nil, 0, errors.New("plan report gate dictionary count is invalid")
	}
	cursor++
	dictionary := make([]GateID, 0, count)
	seen := make(map[GateID]struct{}, count)
	nextIndex := 1
	for nextIndex <= count {
		batch, batchErr := decodePlanGateDictionaryBatch(records, cursor, nextIndex, count, seen)
		if batchErr != nil {
			return nil, 0, batchErr
		}
		dictionary = append(dictionary, batch...)
		nextIndex += len(batch)
		cursor++
	}
	return dictionary, cursor, nil
}

// decodePlanGateDictionaryBatch 解码单个 dictionary 批次并拒绝重复 identity。
func decodePlanGateDictionaryBatch(records []planReportRecord, cursor, nextIndex, total int, seen map[GateID]struct{}) ([]GateID, error) {
	if cursor >= len(records) || records[cursor].kind != planReportGateDictionaryRecord {
		return nil, errors.New("plan report gate dictionary record is missing")
	}
	fields, err := parsePlanGateDictionaryRecord(records[cursor].payload)
	if err != nil {
		return nil, err
	}
	batchCount, err := validatePlanGateDictionaryBatch(fields, nextIndex, total)
	if err != nil {
		return nil, err
	}
	batch := make([]GateID, 0, batchCount)
	for _, value := range fields[3:] {
		id := GateID(value)
		if err := validatePackedGateID(id); err != nil {
			return nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("plan report gate dictionary identity %q is duplicated", id)
		}
		seen[id] = struct{}{}
		batch = append(batch, id)
	}
	return batch, nil
}

// validatePlanGateDictionaryBatch 校验 dictionary 批次的连续序号和数量。
func validatePlanGateDictionaryBatch(fields []string, nextIndex, total int) (int, error) {
	if fields[0] != fmt.Sprintf("%06d", nextIndex) || fields[1] != fmt.Sprintf("%06d", total) {
		return 0, errors.New("plan report gate dictionary sequence is invalid")
	}
	batchCount, err := parseCompileGroupCount(fields[2])
	if err != nil || batchCount != len(fields)-3 || nextIndex+batchCount-1 > total {
		return 0, errors.New("plan report gate dictionary batch is invalid")
	}
	return batchCount, nil
}

func parsePlanGateDictionaryRecord(payload string) ([]string, error) {
	fields := strings.Fields(payload)
	if len(fields) < 4 || strings.Join(fields, " ") != payload {
		return nil, errors.New("plan report gate dictionary payload is invalid")
	}
	return fields, nil
}

// encodePackedPlanGateRecords 保持 gate、profile、timing 与日志的完整证据，同时将其压入一条或少量记录。
func encodePackedPlanGateRecords(index int, result PlanGateExecution) ([]string, error) {
	profile, err := encodePackedExecutionProfile(result.ExecutionProfile)
	if err != nil {
		return nil, err
	}
	timings, err := encodePackedGateTimings(result.TestTimings)
	if err != nil {
		return nil, err
	}
	timingRecords, err := encodePackedPlanGateTimingRecords(index, timings)
	if err != nil {
		return nil, err
	}
	encodedLog, err := encodePlanLogText(result.Log)
	if err != nil {
		return nil, err
	}
	return encodePackedPlanGateRecordsWithLog(index, result, profile, timingRecords, encodedLog)
}

func encodePackedPlanGateRecordsWithLog(index int, result PlanGateExecution, profile string, timingRecords []string, encodedLog []byte) ([]string, error) {
	if len(encodedLog) == 0 {
		candidate := encodePackedPlanGateRecord(index, profile, len(timingRecords), result, 0, 0, "")
		if !planReportRecordPayloadFits(candidate) {
			return nil, errors.New("plan report packed gate metadata exceeds remote log line limit")
		}
		return append([]string{candidate}, timingRecords...), nil
	}
	if candidate := encodePackedPlanGateRecord(index, profile, len(timingRecords), result, 1, 1, string(encodedLog)); planReportRecordPayloadFits(candidate) {
		return append([]string{candidate}, timingRecords...), nil
	}
	return encodePackedPlanGateLogFragments(index, result, profile, timingRecords, encodedLog)
}

func encodePackedPlanGateLogFragments(index int, result PlanGateExecution, profile string, timingRecords []string, encodedLog []byte) ([]string, error) {
	fragments, err := splitPackedPlanGateLog(index, profile, len(timingRecords), result, encodedLog)
	if err != nil {
		return nil, err
	}
	if len(fragments) == 0 || len(fragments) > executorPlanMaxLogRecords {
		return nil, errors.New("plan report packed gate log record count is invalid")
	}
	records := make([]string, 0, len(fragments))
	records = append(records, encodePackedPlanGateRecord(index, profile, len(timingRecords), result, len(fragments), 1, fragments[0]))
	records = append(records, timingRecords...)
	for fragmentIndex, fragment := range fragments[1:] {
		records = append(records, encodePackedPlanGateLogRecord(index, fragmentIndex+2, len(fragments), fragment))
	}
	return records, nil
}

func splitPackedPlanGateLog(index int, profile string, timingRecordCount int, result PlanGateExecution, encodedLog []byte) ([]string, error) {
	first, err := takePackedPlanGateFragment(encodedLog, func(fragment string) string {
		return encodePackedPlanGateRecord(index, profile, timingRecordCount, result, executorPlanMaxLogRecords, 1, fragment)
	})
	if err != nil {
		return nil, err
	}
	fragments := []string{first}
	encodedLog = encodedLog[len(first):]
	for len(encodedLog) > 0 {
		fragment, takeErr := takePackedPlanGateFragment(encodedLog, func(value string) string {
			return encodePackedPlanGateLogRecord(index, executorPlanMaxLogRecords, executorPlanMaxLogRecords, value)
		})
		if takeErr != nil {
			return nil, takeErr
		}
		fragments = append(fragments, fragment)
		encodedLog = encodedLog[len(fragment):]
	}
	return fragments, nil
}

func takePackedPlanGateFragment(data []byte, candidate func(string) string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("plan report packed gate log fragment is empty")
	}
	end := min(len(data), executorPlanReportChunkBytes)
	for end > 0 {
		if utf8.Valid(data[:end]) && planReportRecordPayloadFits(candidate(string(data[:end]))) {
			return string(data[:end]), nil
		}
		end--
	}
	return "", errors.New("plan report packed gate log fragment exceeds remote log line limit")
}

func encodePackedPlanGateRecord(index int, profile string, timingRecordCount int, result PlanGateExecution, fragmentCount, fragmentIndex int, logFragment string) string {
	argvDigest := result.ArgvDigest
	if argvDigest == "" {
		argvDigest = "-"
	}
	return fmt.Sprintf("%s %06d %06d %s %d %s %s %s %s %d %06d %s %06d %06d %06d %s",
		planReportPackedGateRecord, index, index, result.Status, result.ExitCode,
		result.StartedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		result.CompletedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), argvDigest, result.LogDigest,
		len(result.Log), len(result.TestTimings), profile, timingRecordCount, fragmentCount, fragmentIndex, logFragment)
}

func encodePackedPlanGateLogRecord(index, fragmentIndex, fragmentCount int, fragment string) string {
	return fmt.Sprintf("%s %06d %06d %06d %s", planReportPackedGateLogRecord, index, fragmentIndex, fragmentCount, fragment)
}

func encodePackedPlanGateTimingRecord(index, fragmentIndex, fragmentCount int, fragment string) string {
	return fmt.Sprintf("%s %06d %06d %06d %s", planReportPackedGateTimingRecord, index, fragmentIndex, fragmentCount, fragment)
}

// encodePackedPlanGateTimingRecords 将 timing entries 按行界限切成有序续行。
func encodePackedPlanGateTimingRecords(index int, timings string) ([]string, error) {
	if timings == "" {
		return nil, nil
	}
	entries := strings.Split(timings, ";")
	fragments := make([]string, 0, len(entries))
	for start := 0; start < len(entries); {
		end := start
		for end < len(entries) {
			candidate := encodePackedPlanGateTimingRecord(index, 1, executorPlanMaxTimingRecords, strings.Join(entries[start:end+1], ";"))
			if !planReportRecordPayloadFits(candidate) {
				break
			}
			end++
		}
		if end == start {
			return nil, errors.New("plan report packed timing record exceeds remote log line limit")
		}
		fragments = append(fragments, strings.Join(entries[start:end], ";"))
		start = end
	}
	records := make([]string, len(fragments))
	for fragmentIndex, fragment := range fragments {
		records[fragmentIndex] = encodePackedPlanGateTimingRecord(index, fragmentIndex+1, len(fragments), fragment)
	}
	return records, nil
}

func encodePackedExecutionProfile(profile ExecutionProfile) (string, error) {
	if err := profile.Validate(); err != nil {
		return "", err
	}
	frontend, err := encodeFrontendExecutionProfile(profile.Frontend)
	if err != nil {
		return "", err
	}
	values := []string{hex.EncodeToString([]byte(profile.GoFlags)), profile.CacheSource, string(profile.CacheStatus), profile.CacheMeasurement,
		strconv.FormatUint(profile.PrivateHitCount, 10), strconv.FormatUint(profile.BaselineHitCount, 10),
		strconv.FormatUint(profile.CacheMissCount, 10), strconv.FormatUint(profile.CachePutCount, 10),
		strconv.FormatInt(profile.MaterializeMS, 10), strconv.FormatInt(profile.DownloadMS, 10),
		strconv.FormatInt(profile.VerifyMS, 10), strconv.FormatInt(profile.StartupMS, 10),
		strconv.FormatInt(profile.TestBodyMS, 10), strconv.FormatInt(profile.TotalMS, 10), frontend}
	return strings.Join(values, ","), nil
}

func decodePackedExecutionProfile(value string) (ExecutionProfile, error) {
	fields := strings.Split(value, ",")
	if len(fields) != 15 || strings.Join(fields, ",") != value {
		return ExecutionProfile{}, errors.New("plan report packed execution profile is invalid")
	}
	legacyFields := append([]string{"000001"}, fields[1:]...)
	durations, err := decodeExecutionProfileDurations(legacyFields)
	if err != nil {
		return ExecutionProfile{}, err
	}
	privateHits, baselineHits, misses, puts, err := decodeExecutionProfileCacheCounts(legacyFields)
	if err != nil {
		return ExecutionProfile{}, err
	}
	profile, err := buildExecutionProfile(legacyFields, durations, privateHits, baselineHits, misses, puts)
	if err != nil {
		return ExecutionProfile{}, err
	}
	goFlags, err := decodePackedGoFlags(fields[0])
	if err != nil {
		return ExecutionProfile{}, err
	}
	profile.GoFlags = goFlags
	if err := profile.Validate(); err != nil {
		return ExecutionProfile{}, err
	}
	return profile, nil
}

// decodePackedGoFlags restores the canonical profile from its whitespace-safe
// wire representation; packed fields are space-delimited, so raw GOFLAGS are
// never emitted directly.
func decodePackedGoFlags(value string) (string, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || !utf8.Valid(decoded) {
		return "", errors.New("plan report packed execution profile GoFlags is invalid")
	}
	goFlags := string(decoded)
	if err := ValidateCanonicalGoFlags(goFlags); err != nil {
		return "", fmt.Errorf("plan report packed execution profile GoFlags: %w", err)
	}
	return goFlags, nil
}

func encodePackedGateTimings(timings []GoTestTiming) (string, error) {
	if err := testtiming.ValidateList(timings, executorPlanMaxTimingRecords); err != nil {
		return "", err
	}
	entries := make([]string, len(timings))
	for index, timing := range timings {
		entries[index] = strings.Join([]string{hex.EncodeToString([]byte(timing.Name)), string(timing.Status), strconv.FormatInt(timing.DurationMS, 10)}, ",")
	}
	return strings.Join(entries, ";"), nil
}

type packedPlanGateHeader struct {
	result            PlanGateExecution
	logBytes          int
	timingCount       int
	timingRecordCount int
	fragmentCount     int
	fragmentIndex     int
	firstLog          string
}

// decodePackedPlanGate 严格恢复 dictionary-indexed gate 及其可能的日志续行。
func decodePackedPlanGate(records []planReportRecord, cursor, expectedIndex int, dictionary []GateID) (PlanGateExecution, int, error) {
	if cursor >= len(records) || records[cursor].kind != planReportPackedGateRecord {
		return PlanGateExecution{}, 0, errors.New("plan report packed gate record is missing")
	}
	fields, err := parsePackedPlanGateFields(records[cursor].payload)
	if err != nil {
		return PlanGateExecution{}, 0, err
	}
	header, err := decodePackedPlanGateHeader(fields, expectedIndex, dictionary)
	if err != nil {
		return PlanGateExecution{}, 0, err
	}
	timings, err := decodePackedPlanGateTimings(records, cursor, expectedIndex, header.timingCount, header.timingRecordCount)
	if err != nil {
		return PlanGateExecution{}, 0, err
	}
	encodedLog, err := decodePackedPlanGateLogs(records, cursor, expectedIndex, header.timingRecordCount, header.fragmentCount, header.firstLog)
	if err != nil {
		return PlanGateExecution{}, 0, err
	}
	log, err := decodePlanLogText(encodedLog)
	if err != nil || len(log) != header.logBytes || digestPlanLog(log) != header.result.LogDigest {
		return PlanGateExecution{}, 0, errors.New("plan report packed gate log is invalid")
	}
	header.result.Log = PlainTextLog(log)
	header.result.TestTimings = timings
	consumed := 1 + header.timingRecordCount + max(0, header.fragmentCount-1)
	return header.result, cursor + consumed, nil
}

func decodePackedPlanGateHeader(fields []string, expectedIndex int, dictionary []GateID) (packedPlanGateHeader, error) {
	result, err := decodePackedPlanGateResult(fields, expectedIndex, dictionary)
	if err != nil {
		return packedPlanGateHeader{}, err
	}
	logBytes, err := parsePackedPlanGateLogBytes(fields[8])
	if err != nil {
		return packedPlanGateHeader{}, err
	}
	timingCount, timingRecordCount, err := decodePackedPlanGateTimingMetadata(fields)
	if err != nil {
		return packedPlanGateHeader{}, err
	}
	fragmentCount, fragmentIndex, firstLog, err := decodePackedPlanGateLogMetadata(fields)
	if err != nil {
		return packedPlanGateHeader{}, err
	}
	return packedPlanGateHeader{result: result, logBytes: logBytes, timingCount: timingCount, timingRecordCount: timingRecordCount,
		fragmentCount: fragmentCount, fragmentIndex: fragmentIndex, firstLog: firstLog}, nil
}

// decodePackedPlanGateResult 解析 packed gate 的身份、状态、时间与 profile。
func decodePackedPlanGateResult(fields []string, expectedIndex int, dictionary []GateID) (PlanGateExecution, error) {
	if fields[0] != fmt.Sprintf("%06d", expectedIndex) {
		return PlanGateExecution{}, errors.New("plan report packed gate sequence is invalid")
	}
	dictionaryIndex, err := parseSixDigitCount(fields[1])
	if err != nil || dictionaryIndex > len(dictionary) {
		return PlanGateExecution{}, errors.New("plan report packed gate identity index is invalid")
	}
	exitCode, err := strconv.Atoi(fields[3])
	if err != nil {
		return PlanGateExecution{}, errors.New("plan report packed gate exit code is invalid")
	}
	startedAt, completedAt, err := parsePlanGateTimes(fields[4], fields[5])
	if err != nil {
		return PlanGateExecution{}, err
	}
	argvDigest, logDigest, err := parsePlanGateDigests(fields[6], fields[7])
	if err != nil {
		return PlanGateExecution{}, err
	}
	profile, err := decodePackedExecutionProfile(fields[10])
	if err != nil {
		return PlanGateExecution{}, err
	}
	return PlanGateExecution{GateID: dictionary[dictionaryIndex-1], Status: ResultStatus(fields[2]), ExitCode: exitCode,
		StartedAt: startedAt, CompletedAt: completedAt, ArgvDigest: argvDigest, LogDigest: logDigest,
		ExecutionProfile: profile}, nil
}

func parsePackedPlanGateLogBytes(value string) (int, error) {
	logBytes, err := strconv.Atoi(value)
	if err != nil || logBytes < 0 || logBytes > executorPlanMaxLogBytes {
		return 0, errors.New("plan report packed gate log byte count is invalid")
	}
	return logBytes, nil
}

// decodePackedPlanGateTimingMetadata 校验 timing 总数与续行总数的一致性。
func decodePackedPlanGateTimingMetadata(fields []string) (int, int, error) {
	timingCount, err := parseSixDigitCountAllowZero(fields[9])
	if err != nil || timingCount > executorPlanMaxTimingRecords {
		return 0, 0, errors.New("plan report packed gate timing count is invalid")
	}
	timingRecordCount, err := parseSixDigitCountAllowZero(fields[11])
	if err != nil || timingRecordCount > executorPlanMaxTimingRecords || (timingCount == 0) != (timingRecordCount == 0) || (timingRecordCount > timingCount && timingCount > 0) {
		return 0, 0, errors.New("plan report packed gate timing record count is invalid")
	}
	return timingCount, timingRecordCount, nil
}

// decodePackedPlanGateLogMetadata 校验日志首片段的数量和位置声明。
func decodePackedPlanGateLogMetadata(fields []string) (int, int, string, error) {
	fragmentCount, err := parseSixDigitCountAllowZero(fields[12])
	if err != nil || fragmentCount > executorPlanMaxLogRecords {
		return 0, 0, "", errors.New("plan report packed gate log fragment count is invalid")
	}
	fragmentIndex, err := parseSixDigitCountAllowZero(fields[13])
	if err != nil || (fragmentCount == 0 && fragmentIndex != 0) || (fragmentCount > 0 && fragmentIndex != 1) {
		return 0, 0, "", errors.New("plan report packed gate log fragment index is invalid")
	}
	firstLog := fields[14]
	if err := validatePackedPlanGateFirstLog(fragmentCount, firstLog); err != nil {
		return 0, 0, "", err
	}
	return fragmentCount, fragmentIndex, firstLog, nil
}

// validatePackedPlanGateFirstLog 要求日志片段数量与首片段内容严格配对。
func validatePackedPlanGateFirstLog(fragmentCount int, firstLog string) error {
	if fragmentCount == 0 && firstLog != "" {
		return errors.New("plan report packed gate empty log is invalid")
	}
	if fragmentCount > 0 && firstLog == "" {
		return errors.New("plan report packed gate first log fragment is missing")
	}
	return nil
}

// decodePackedPlanGateTimings 恢复并校验 packed gate 的全部 timing entries。
func decodePackedPlanGateTimings(records []planReportRecord, cursor, expectedIndex, timingCount, timingRecordCount int) ([]GoTestTiming, error) {
	timings := make([]GoTestTiming, 0, timingCount)
	for next := 1; next <= timingRecordCount; next++ {
		if cursor+next >= len(records) || records[cursor+next].kind != planReportPackedGateTimingRecord {
			return nil, errors.New("plan report packed gate timing continuation is missing")
		}
		fragment, err := parsePackedPlanGateTimingRecord(records[cursor+next].payload, expectedIndex, next, timingRecordCount)
		if err != nil {
			return nil, err
		}
		decoded, err := decodePackedGateTimingFragment(fragment)
		if err != nil {
			return nil, err
		}
		timings = append(timings, decoded...)
	}
	if len(timings) != timingCount || testtiming.ValidateList(timings, executorPlanMaxTimingRecords) != nil {
		return nil, errors.New("plan report packed gate timing set is invalid")
	}
	return timings, nil
}

func decodePackedPlanGateLogs(records []planReportRecord, cursor, expectedIndex, timingRecordCount, fragmentCount int, firstLog string) ([]byte, error) {
	encodedLog := []byte(firstLog)
	for next := 2; next <= fragmentCount; next++ {
		logCursor := cursor + timingRecordCount + next - 1
		if logCursor >= len(records) || records[logCursor].kind != planReportPackedGateLogRecord {
			return nil, errors.New("plan report packed gate log continuation is missing")
		}
		fragment, err := parsePackedPlanGateLogRecord(records[logCursor].payload, expectedIndex, next, fragmentCount)
		if err != nil {
			return nil, err
		}
		encodedLog = append(encodedLog, fragment...)
	}
	return encodedLog, nil
}

func parsePackedPlanGateFields(payload string) ([]string, error) {
	fields := strings.SplitN(payload, " ", 15)
	if len(fields) != 15 {
		return nil, errors.New("plan report packed gate payload is invalid")
	}
	return fields, nil
}

// parsePackedPlanGateLogRecord 校验日志续行的 gate、片段序号和总数。
func parsePackedPlanGateLogRecord(payload string, expectedIndex, expectedFragment, fragmentCount int) (string, error) {
	fields := strings.SplitN(payload, " ", 4)
	if len(fields) != 4 || fields[3] == "" {
		return "", errors.New("plan report packed gate log payload is invalid")
	}
	if fields[0] != fmt.Sprintf("%06d", expectedIndex) || fields[1] != fmt.Sprintf("%06d", expectedFragment) || fields[2] != fmt.Sprintf("%06d", fragmentCount) {
		return "", errors.New("plan report packed gate log sequence is invalid")
	}
	return fields[3], nil
}

// parsePackedPlanGateTimingRecord 校验 timing 续行的 gate、片段序号和总数。
func parsePackedPlanGateTimingRecord(payload string, expectedIndex, expectedFragment, fragmentCount int) (string, error) {
	fields := strings.SplitN(payload, " ", 4)
	if len(fields) != 4 || fields[3] == "" {
		return "", errors.New("plan report packed gate timing payload is invalid")
	}
	if fields[0] != fmt.Sprintf("%06d", expectedIndex) || fields[1] != fmt.Sprintf("%06d", expectedFragment) || fields[2] != fmt.Sprintf("%06d", fragmentCount) {
		return "", errors.New("plan report packed gate timing sequence is invalid")
	}
	return fields[3], nil
}

// decodePackedGateTimingFragment 解码单个 timing 续行并保留测试名称和状态。
func decodePackedGateTimingFragment(value string) ([]GoTestTiming, error) {
	entries := strings.Split(value, ";")
	timings := make([]GoTestTiming, len(entries))
	for index, entry := range entries {
		fields := strings.Split(entry, ",")
		if len(fields) != 3 {
			return nil, errors.New("plan report packed timing entry is invalid")
		}
		name, err := hex.DecodeString(fields[0])
		if err != nil || !utf8.Valid(name) {
			return nil, errors.New("plan report packed timing name is invalid")
		}
		duration, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, errors.New("plan report packed timing duration is invalid")
		}
		timings[index] = GoTestTiming{Name: string(name), Status: GoTestStatus(fields[1]), DurationMS: duration}
	}
	return timings, nil
}
