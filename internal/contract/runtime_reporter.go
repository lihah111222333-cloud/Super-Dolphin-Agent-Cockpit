package contract

import "context"

// RuntimeReport mirrors the fields accepted by orchestration.reportRuntime.
type RuntimeReport struct {
	AgentID  string
	Port     int
	Provider string
}

// RuntimeReporter lets in-process providers publish runtime metadata without
// importing the orchestration module directly.
type RuntimeReporter interface {
	ReportRuntime(ctx context.Context, report RuntimeReport) error
}
