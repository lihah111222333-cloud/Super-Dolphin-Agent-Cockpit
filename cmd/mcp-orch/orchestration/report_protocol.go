package orchestration

// Protocol contract for the orchestration report pipeline. Collapses
// the previously inline method names, terminal event-type / thread-status
// tables, and payload scan-ordering literals into a single authoritative
// definition so P4 §64 / §122 / §283 ("agent.reportEvent /
// agent.rememberReportRequest 的协议壳和 payload 规则已显式化，不再散落
// 在子包里") becomes concrete and guardable.
//
// Consumers (rpc.go, report.go) reference the symbols here instead of
// embedding raw string literals. The archtest in
// internal/archtest/orchestration_report_protocol_guard_test.go pins
// the method names + the "thread/status/changed" special case so
// accidental re-introduction of those literals outside this file fails
// the build.

// RPC method names registered by NewOrchestrationHandlers (→
// ProvideRPCFacade).
const (
	// ReportMethodReportEvent accepts a report event payload and
	// applies it to the agent's report state. See (*service).HandleReportEvent.
	ReportMethodReportEvent = "agent.reportEvent"
	// ReportMethodRememberReportRequest records that a peer has
	// subscribed to the next terminal report for an agent. See
	// (*service).RememberReportRequest.
	ReportMethodRememberReportRequest = "agent.rememberReportRequest"
)

// Special event type whose terminality is decided by the event
// payload's `status` field rather than the event-type name.
const ReportEventTypeThreadStatusChanged = "thread/status/changed"

// terminalReportEventTypesList is the authoritative set of event-type
// strings whose arrival drains any pending requester subscriptions.
// The order of this slice is not load-bearing; the runtime map
// (terminalReportEventTypes) is derived from it so lookups stay O(1).
var terminalReportEventTypesList = []string{
	"agent/event/task_complete",
	"completed",
	"completion",
	"connection.dead",
	"connection_dead",
	"error",
	"idle",
	"shutdown.complete",
	"shutdown_complete",
	"stream.error",
	"stream_error",
	"turn.completed",
	"turn/completed",
	"turn.aborted",
	"turn_aborted",
	"turn_complete",
}

// terminalThreadStatusesList is the authoritative set of
// thread-status strings that count as terminal when a
// ReportEventTypeThreadStatusChanged event lands. Values are compared
// case-insensitively via strings.ToLower before lookup.
var terminalThreadStatusesList = []string{
	"error",
	"idle",
	"not_loaded",
	"notloaded",
	"system_error",
	"systemerror",
}

// reportTextPayloadKeys is the ordered precedence used by
// reportTextFromPayload when extracting a human-readable report string
// from an event_data map. First non-empty wins; ordering is load-
// bearing and changing it alters observable behavior, so new keys
// must land here explicitly.
var reportTextPayloadKeys = []string{
	"report",
	"summary",
	"uiText",
	"text",
	"message",
	"output",
	"result",
}

// reportTextNestedKeys lists the sub-object keys the payload scanner
// recurses into when no top-level text key matched. The recursion is
// single-level per key; nesting deeper requires a new entry here.
var reportTextNestedKeys = []string{
	"item",
	"payload",
}

// terminalReportEventTypes materializes terminalReportEventTypesList
// as a set for O(1) lookup in isTerminalReportEvent.
var terminalReportEventTypes = buildStringSet(terminalReportEventTypesList)

// terminalThreadStatuses materializes terminalThreadStatusesList as a
// set for O(1) lookup in isTerminalThreadStatus.
var terminalThreadStatuses = buildStringSet(terminalThreadStatusesList)

func buildStringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}
