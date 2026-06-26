package orchestration

// report RPC 方法名和 report event 类型常量。
const (
	ReportMethodReportEvent            = "agent/reportEvent"
	ReportMethodRememberReportRequest  = "agent/rememberReportRequest"
	ReportEventTypeThreadStatusChanged = "thread/status/changed"
)
