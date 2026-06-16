package observability

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	TraceDiagnosisDefaultLimit       = 80
	TraceDiagnosisMaxLimit           = 200
	TraceDiagnosisMaxSlowSummaries   = 20
	TraceDiagnosisMaxErrorSummaries  = 20
	TraceDiagnosisMaxPanicSummaries  = 10
	TraceDiagnosisMaxTailWarnings    = 20
	TraceDiagnosisMaxStackFrames     = 24
	TraceDiagnosisMaxRelatedIDs      = 50
	TraceDiagnosisMaxStringBytes     = 4096
	TraceDiagnosisMaxSerializedBytes = 256 * 1024
	TraceDiagnosisSourceNone         = TraceDiagnosisSource("none")
	TraceDiagnosisSourceMemory       = TraceDiagnosisSource("memory")
	TraceDiagnosisSourceTail         = TraceDiagnosisSource("tail")
	TraceDiagnosisSourceMixed        = TraceDiagnosisSource("mixed")
)

var (
	ErrTraceDiagnosisMissingTraceID     = errors.New("observability trace diagnosis requires trace_id")
	ErrTraceDiagnosisServiceUnavailable = errors.New("observability trace diagnosis service unavailable")
)

const redactedPath = "[REDACTED_PATH]"

var (
	diagnosisEmailPattern    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	diagnosisPhonePattern    = regexp.MustCompile(`\+?\d[\d .()\-]{7,}\d`)
	diagnosisIPv4Pattern     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	diagnosisHostnamePattern = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:ai|app|biz|cloud|cn|co|com|corp|dev|edu|f666|gov|info|internal|io|lan|local|mil|net|org|test)\b`)
	diagnosisUnixPathPattern = regexp.MustCompile(`/(?:[^\s,;]+/)+[^\s,;]+`)
	diagnosisHomePathPattern = regexp.MustCompile(`~[/\\][^\s,;]+`)
	diagnosisWinPathPattern  = regexp.MustCompile(`(?i)\b[a-z]:[/\\][^\s,;]+`)
	diagnosisUNCPathPattern  = regexp.MustCompile(`\\\\[^\s,;]+`)
)

type TraceDiagnosisSource string

type TraceDiagnosisRequest struct {
	TraceID       string `json:"trace_id"`
	Limit         int    `json:"limit,omitempty"`
	ForceRefresh  bool   `json:"force_refresh,omitempty"`
	IncludeStack  bool   `json:"include_stack,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
}

type TraceDiagnosis struct {
	TraceID          string                       `json:"trace_id"`
	Limit            int                          `json:"limit"`
	Source           TraceDiagnosisSource         `json:"source"`
	Timeline         []TraceDiagnosisTimelineItem `json:"timeline,omitempty"`
	SlowSummaries    []TraceDiagnosisSpanSummary  `json:"slow_summaries,omitempty"`
	ErrorSummaries   []TraceDiagnosisErrorSummary `json:"error_summaries,omitempty"`
	PanicSummaries   []TraceDiagnosisErrorSummary `json:"panic_summaries,omitempty"`
	RelatedIDs       TraceDiagnosisRelatedIDs     `json:"related_ids,omitzero"`
	Truncated        bool                         `json:"truncated,omitempty"`
	TailAttempted    bool                         `json:"tail_attempted"`
	TailFresh        bool                         `json:"tail_fresh"`
	Degraded         bool                         `json:"degraded"`
	TailError        string                       `json:"tail_error,omitempty"`
	TailWarnings     []string                     `json:"tail_warnings,omitempty"`
	DecodeErrorCount int                          `json:"decode_error_count,omitempty"`
	TailFilesScanned int                          `json:"tail_files_scanned,omitempty"`
	TailBytesRead    int                          `json:"tail_bytes_read,omitempty"`
	TailDurationMS   int64                        `json:"tail_duration_ms,omitempty"`
	TailTimedOut     bool                         `json:"tail_timed_out,omitempty"`
	TailTruncated    bool                         `json:"tail_truncated,omitempty"`
}

type TraceDiagnosisTimelineItem struct {
	Timestamp    time.Time  `json:"ts,omitzero"`
	Status       Status     `json:"status,omitempty"`
	Method       string     `json:"method,omitempty"`
	DurationMS   int64      `json:"duration_ms,omitempty"`
	SpanID       string     `json:"span_id,omitempty"`
	ParentSpanID string     `json:"parent_span_id,omitempty"`
	ThreadID     string     `json:"thread_id,omitempty"`
	AgentID      string     `json:"agent_id,omitempty"`
	TurnID       string     `json:"turn_id,omitempty"`
	CallID       string     `json:"call_id,omitempty"`
	ToolName     string     `json:"tool_name,omitempty"`
	Code         CodeAnchor `json:"code,omitzero"`
}

type TraceDiagnosisSpanSummary struct {
	SpanID     string `json:"span_id,omitempty"`
	Method     string `json:"method,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type TraceDiagnosisErrorSummary struct {
	SpanID     string       `json:"span_id,omitempty"`
	Method     string       `json:"method,omitempty"`
	Status     Status       `json:"status,omitempty"`
	Error      string       `json:"error,omitempty"`
	DurationMS int64        `json:"duration_ms,omitempty"`
	Stack      []StackFrame `json:"stack,omitempty"`
}

type TraceDiagnosisRelatedIDs struct {
	ThreadIDs []string `json:"thread_ids,omitempty"`
	AgentIDs  []string `json:"agent_ids,omitempty"`
	TurnIDs   []string `json:"turn_ids,omitempty"`
	CallIDs   []string `json:"call_ids,omitempty"`
	ToolNames []string `json:"tool_names,omitempty"`
}

// DiagnoseTrace 按 trace ID 生成诊断结果。
func (s *Service) DiagnoseTrace(ctx context.Context, req TraceDiagnosisRequest) (TraceDiagnosis, error) {
	rawTraceID := strings.TrimSpace(req.TraceID)
	if rawTraceID == "" {
		return TraceDiagnosis{}, ErrTraceDiagnosisMissingTraceID
	}
	projector := newTraceDiagnosisProjector(req)
	traceID := projector.string(rawTraceID)
	limit := normalizeTraceDiagnosisLimit(req.Limit)
	diagnosis := TraceDiagnosis{TraceID: traceID, Limit: limit, Source: TraceDiagnosisSourceNone}
	if s == nil {
		return TraceDiagnosis{}, ErrTraceDiagnosisServiceUnavailable
	}
	if !s.Enabled() {
		diagnosis.Degraded = true
		diagnosis.TailError = projector.string(s.disabledReason)
		return enforceTraceDiagnosisPayloadLimit(diagnosis), nil
	}
	queryTraceID := s.sanitizer.String(rawTraceID)
	query := Query{TraceID: queryTraceID, Limit: limit}
	result := s.diagnosisQuery(ctx, query, req.ForceRefresh, &diagnosis, projector)
	return enforceTraceDiagnosisPayloadLimit(s.diagnosisFromQueryResult(diagnosis, result, req, projector)), nil
}

func normalizeTraceDiagnosisLimit(limit int) int {
	if limit <= 0 {
		return TraceDiagnosisDefaultLimit
	}
	if limit > TraceDiagnosisMaxLimit {
		return TraceDiagnosisMaxLimit
	}
	return limit
}

func (s *Service) diagnosisFromQueryResult(diagnosis TraceDiagnosis, result QueryResult, req TraceDiagnosisRequest, projector traceDiagnosisProjector) TraceDiagnosis {
	diagnosis.Source = traceDiagnosisSourceFromQuery(result)
	diagnosis.Truncated = result.Truncated
	for _, event := range result.Events {
		diagnosis.addEventSummary(event, req.IncludeStack, projector)
	}
	return diagnosis
}

func (s *Service) diagnosisQuery(ctx context.Context, query Query, forceRefresh bool, diagnosis *TraceDiagnosis, projector traceDiagnosisProjector) QueryResult {
	memory := s.index.Query(query)
	if s.tail == nil {
		return memory
	}
	tail, err := s.diagnosisTailQuery(ctx, query, forceRefresh)
	diagnosis.addTailStatus(tail, err, projector)
	if err != nil || len(tail.Events) == 0 {
		return memory
	}
	if len(memory.Events) == 0 {
		tail.Source = QuerySourceJSONLTail
		return tail
	}
	return mergeQueryResults(memory, tail, query.Limit)
}

func (s *Service) diagnosisTailQuery(ctx context.Context, query Query, forceRefresh bool) (QueryResult, error) {
	if forceRefresh {
		return s.queryTailFresh(ctx, query)
	}
	return s.queryTail(ctx, query)
}

func (d *TraceDiagnosis) addTailStatus(result QueryResult, err error, projector traceDiagnosisProjector) {
	d.TailAttempted = true
	d.TailFresh = err == nil
	d.TailFilesScanned = result.TailFilesScanned
	d.TailBytesRead = result.TailBytesRead
	d.TailDurationMS = result.TailDurationMS
	d.TailTimedOut = result.TailTimedOut || errors.Is(err, context.DeadlineExceeded)
	d.TailTruncated = result.TailTruncated
	d.DecodeErrorCount = len(result.TailDecodeErrors)
	d.TailWarnings = tailDecodeWarnings(result.TailDecodeErrors, projector)
	if err != nil {
		d.Degraded = true
		d.TailError = projector.string(err.Error())
	}
	if len(result.TailDecodeErrors) > 0 {
		d.Degraded = true
	}
}

func tailDecodeWarnings(errors []TailDecodeError, projector traceDiagnosisProjector) []string {
	limit := len(errors)
	if limit > TraceDiagnosisMaxTailWarnings {
		limit = TraceDiagnosisMaxTailWarnings
	}
	warnings := make([]string, 0, limit)
	for _, decodeError := range errors[:limit] {
		warnings = append(warnings, tailDecodeWarning(decodeError, projector))
	}
	return warnings
}

func tailDecodeWarning(decodeError TailDecodeError, projector traceDiagnosisProjector) string {
	parts := []string{"jsonl decode error"}
	if file := projector.path(decodeError.File); file != "" {
		parts = append(parts, "file="+file)
	}
	if decodeError.Line > 0 {
		parts = append(parts, "line="+strconv.Itoa(decodeError.Line))
	}
	if decodeError.Trailing {
		parts = append(parts, "trailing=true")
	}
	if decodeError.Error != "" {
		parts = append(parts, "error="+projector.string(decodeError.Error))
	}
	return projector.string(strings.Join(parts, " "))
}

func traceDiagnosisSourceFromQuery(result QueryResult) TraceDiagnosisSource {
	if len(result.Events) == 0 {
		return TraceDiagnosisSourceNone
	}
	switch result.Source {
	case QuerySourceMemory:
		return TraceDiagnosisSourceMemory
	case QuerySourceJSONLTail:
		return TraceDiagnosisSourceTail
	case QuerySourceMixed:
		return TraceDiagnosisSourceMixed
	default:
		return TraceDiagnosisSourceNone
	}
}

// addEventSummary 添加事件摘要。
func (d *TraceDiagnosis) addEventSummary(event TraceEvent, includeStack bool, projector traceDiagnosisProjector) {
	d.Timeline = append(d.Timeline, timelineItemFromEvent(event, projector))
	d.RelatedIDs.addEvent(event, projector)
	if event.Status == StatusSlow && len(d.SlowSummaries) < TraceDiagnosisMaxSlowSummaries {
		d.SlowSummaries = append(d.SlowSummaries, spanSummaryFromEvent(event, projector))
	}
	if event.Status == StatusError && len(d.ErrorSummaries) < TraceDiagnosisMaxErrorSummaries {
		d.ErrorSummaries = append(d.ErrorSummaries, errorSummaryFromEvent(event, includeStack, projector))
	}
	if event.Status == StatusPanic && len(d.PanicSummaries) < TraceDiagnosisMaxPanicSummaries {
		d.PanicSummaries = append(d.PanicSummaries, errorSummaryFromEvent(event, includeStack, projector))
	}
}

func timelineItemFromEvent(event TraceEvent, projector traceDiagnosisProjector) TraceDiagnosisTimelineItem {
	return TraceDiagnosisTimelineItem{
		Timestamp:    event.Timestamp,
		Status:       projector.status(event.Status),
		Method:       projector.string(event.Method),
		DurationMS:   event.DurationMS,
		SpanID:       projector.string(event.SpanID),
		ParentSpanID: projector.string(event.ParentSpanID),
		ThreadID:     projector.string(event.ThreadID),
		AgentID:      projector.string(event.AgentID),
		TurnID:       projector.string(event.TurnID),
		CallID:       projector.string(event.CallID),
		ToolName:     projector.string(event.ToolName),
		Code:         projector.codeAnchor(event.Code),
	}
}

func spanSummaryFromEvent(event TraceEvent, projector traceDiagnosisProjector) TraceDiagnosisSpanSummary {
	return TraceDiagnosisSpanSummary{SpanID: projector.string(event.SpanID), Method: projector.string(event.Method), DurationMS: event.DurationMS}
}

func errorSummaryFromEvent(event TraceEvent, includeStack bool, projector traceDiagnosisProjector) TraceDiagnosisErrorSummary {
	summary := TraceDiagnosisErrorSummary{SpanID: projector.string(event.SpanID), Method: projector.string(event.Method), Status: projector.status(event.Status), Error: projector.string(event.Error), DurationMS: event.DurationMS}
	if includeStack {
		summary.Stack = projector.stack(event.Stack)
	}
	return summary
}

type traceDiagnosisProjector struct{ roots []string }

func newTraceDiagnosisProjector(req TraceDiagnosisRequest) traceDiagnosisProjector {
	return traceDiagnosisProjector{roots: diagnosisRoots(req.WorkspaceRoot, req.CWD)}
}

func (p traceDiagnosisProjector) codeAnchor(anchor CodeAnchor) CodeAnchor {
	return CodeAnchor{File: p.path(anchor.File), Function: p.string(anchor.Function), Line: anchor.Line}
}

func (p traceDiagnosisProjector) stack(frames []StackFrame) []StackFrame {
	limit := len(frames)
	if limit > TraceDiagnosisMaxStackFrames {
		limit = TraceDiagnosisMaxStackFrames
	}
	out := make([]StackFrame, 0, limit)
	for _, frame := range frames[:limit] {
		out = append(out, StackFrame{File: p.path(frame.File), Function: p.string(frame.Function), Line: frame.Line})
	}
	return out
}

func (p traceDiagnosisProjector) status(status Status) Status {
	return Status(p.string(string(status)))
}

func (p traceDiagnosisProjector) string(value string) string {
	value = normalizeMultiline(value)
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "$1"+redacted)
	}
	value = diagnosisEmailPattern.ReplaceAllString(value, redacted)
	value = diagnosisPhonePattern.ReplaceAllString(value, redacted)
	value = diagnosisIPv4Pattern.ReplaceAllString(value, redacted)
	value = diagnosisHostnamePattern.ReplaceAllString(value, redacted)
	value = diagnosisWinPathPattern.ReplaceAllString(value, redactedPath)
	value = diagnosisUNCPathPattern.ReplaceAllString(value, redactedPath)
	value = diagnosisHomePathPattern.ReplaceAllString(value, redactedPath)
	value = diagnosisUnixPathPattern.ReplaceAllString(value, redactedPath)
	return truncateUTF8(value, TraceDiagnosisMaxStringBytes)
}

// path 处理路径。
func (p traceDiagnosisProjector) path(value string) string {
	value = strings.TrimSpace(normalizeMultiline(value))
	if value == "" {
		return ""
	}
	if diagnosisSlashAbs(value) {
		candidate := path.Clean(value)
		for _, root := range p.roots {
			if rel, ok := diagnosisSlashRelativePath(root, candidate); ok {
				return p.string(rel)
			}
		}
		return redactedPath
	}
	if filepath.IsAbs(value) {
		candidate := filepath.Clean(value)
		for _, root := range p.roots {
			if rel, ok := diagnosisRelativePath(root, candidate); ok {
				return p.string(filepath.ToSlash(rel))
			}
		}
		return redactedPath
	}
	if diagnosisForeignPathLike(value) {
		return redactedPath
	}
	if !filepath.IsAbs(value) {
		return p.string(value)
	}
	return redactedPath
}

func diagnosisForeignPathLike(value string) bool {
	return diagnosisWinPathPattern.MatchString(value) || diagnosisUNCPathPattern.MatchString(value) || diagnosisHomePathPattern.MatchString(value) || strings.HasPrefix(value, "/")
}

// diagnosisRoots 处理诊断根目录。
func diagnosisRoots(values ...string) []string {
	roots := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		switch {
		case diagnosisSlashAbs(value):
			cleaned := path.Clean(value)
			if cleaned != "/" {
				roots = append(roots, cleaned)
			}
		default:
			cleaned := filepath.Clean(value)
			if cleaned != "." && filepath.IsAbs(cleaned) {
				roots = append(roots, cleaned)
			}
		}
	}
	return roots
}

func diagnosisSlashAbs(value string) bool {
	return strings.HasPrefix(value, "/")
}

// diagnosisSlashRelativePath 处理诊断斜杠相对路径。
func diagnosisSlashRelativePath(root string, candidate string) (string, bool) {
	if !diagnosisSlashAbs(root) || !diagnosisSlashAbs(candidate) {
		return "", false
	}
	root = strings.TrimSuffix(path.Clean(root), "/")
	candidate = path.Clean(candidate)
	if root == "" || root == candidate {
		return "", false
	}
	prefix := root + "/"
	if !strings.HasPrefix(candidate, prefix) {
		return "", false
	}
	rel := strings.TrimPrefix(candidate, prefix)
	if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// diagnosisRelativePath 处理诊断相对路径。
func diagnosisRelativePath(root string, candidate string) (string, bool) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func (ids *TraceDiagnosisRelatedIDs) addEvent(event TraceEvent, projector traceDiagnosisProjector) {
	ids.ThreadIDs = appendUniqueBounded(ids.ThreadIDs, event.ThreadID, projector)
	ids.AgentIDs = appendUniqueBounded(ids.AgentIDs, event.AgentID, projector)
	ids.TurnIDs = appendUniqueBounded(ids.TurnIDs, event.TurnID, projector)
	ids.CallIDs = appendUniqueBounded(ids.CallIDs, event.CallID, projector)
	ids.ToolNames = appendUniqueBounded(ids.ToolNames, event.ToolName, projector)
}

func appendUniqueBounded(values []string, value string, projector traceDiagnosisProjector) []string {
	value = projector.string(value)
	if value == "" || len(values) >= TraceDiagnosisMaxRelatedIDs {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func enforceTraceDiagnosisPayloadLimit(diagnosis TraceDiagnosis) TraceDiagnosis {
	for traceDiagnosisPayloadSize(diagnosis) > TraceDiagnosisMaxSerializedBytes {
		if !trimTraceDiagnosisPayload(&diagnosis) {
			return diagnosis
		}
		diagnosis.Truncated = true
	}
	return diagnosis
}

func traceDiagnosisPayloadSize(diagnosis TraceDiagnosis) int {
	data, err := json.Marshal(diagnosis)
	if err != nil {
		return TraceDiagnosisMaxSerializedBytes + 1
	}
	return len(data)
}

// trimTraceDiagnosisPayload 处理裁剪trace诊断载荷。
func trimTraceDiagnosisPayload(diagnosis *TraceDiagnosis) bool {
	switch {
	case len(diagnosis.Timeline) > 0:
		diagnosis.Timeline = diagnosis.Timeline[1:]
	case len(diagnosis.SlowSummaries) > 0:
		diagnosis.SlowSummaries = diagnosis.SlowSummaries[1:]
	case len(diagnosis.ErrorSummaries) > 0:
		diagnosis.ErrorSummaries = diagnosis.ErrorSummaries[1:]
	case len(diagnosis.PanicSummaries) > 0:
		diagnosis.PanicSummaries = diagnosis.PanicSummaries[1:]
	case diagnosis.RelatedIDs.trim():
	case len(diagnosis.TailWarnings) > 0:
		diagnosis.TailWarnings = diagnosis.TailWarnings[1:]
	default:
		return false
	}
	return true
}

// trim 处理裁剪。
func (ids *TraceDiagnosisRelatedIDs) trim() bool {
	switch {
	case len(ids.ThreadIDs) > 0:
		ids.ThreadIDs = ids.ThreadIDs[1:]
	case len(ids.AgentIDs) > 0:
		ids.AgentIDs = ids.AgentIDs[1:]
	case len(ids.TurnIDs) > 0:
		ids.TurnIDs = ids.TurnIDs[1:]
	case len(ids.CallIDs) > 0:
		ids.CallIDs = ids.CallIDs[1:]
	case len(ids.ToolNames) > 0:
		ids.ToolNames = ids.ToolNames[1:]
	default:
		return false
	}
	return true
}
