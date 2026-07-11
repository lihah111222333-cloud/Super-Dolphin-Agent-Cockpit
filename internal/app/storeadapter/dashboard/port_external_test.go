package dashboardadapter_test

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/dashboard"
)

type externalDashboardPorts struct{}

func (externalDashboardPorts) List(context.Context, string) ([]dashboard.AgentStatus, error) {
	return nil, nil
}
func (externalDashboardPorts) ListByCategory(context.Context, string, string, int32) ([]dashboard.AILog, error) {
	return nil, nil
}
func (externalDashboardPorts) CountByStatus(context.Context) ([]dashboard.AILogStatusCount, error) {
	return nil, nil
}
func (externalDashboardPorts) ListRecent(context.Context, int32) ([]dashboard.AILog, error) {
	return nil, nil
}
func (externalDashboardPorts) Query(context.Context, string, ...any) ([]map[string]any, error) {
	return nil, nil
}
func (externalDashboardPorts) Get(context.Context, string) (*dashboard.SharedFile, error) {
	return nil, nil
}

type externalDashboardAILogPort struct{ externalDashboardPorts }

func (externalDashboardAILogPort) List(context.Context, dashboard.AILogFilter) ([]dashboard.AILog, error) {
	return nil, nil
}

type externalDashboardAuditPort struct{ externalDashboardPorts }

func (externalDashboardAuditPort) List(context.Context, dashboard.AuditLogFilter) ([]dashboard.AuditEvent, error) {
	return nil, nil
}

type externalDashboardBusPort struct{ externalDashboardPorts }

func (externalDashboardBusPort) List(context.Context, dashboard.BusLogFilter) ([]dashboard.BusExceptionLog, error) {
	return nil, nil
}
func (externalDashboardBusPort) Get(context.Context, int64) (dashboard.BusExceptionLog, error) {
	return dashboard.BusExceptionLog{}, nil
}

type externalDashboardSystemPort struct{ externalDashboardPorts }

func (externalDashboardSystemPort) List(context.Context, dashboard.SystemLogFilter) ([]dashboard.SystemLog, error) {
	return nil, nil
}

type externalDashboardCommandPort struct{ externalDashboardPorts }

func (externalDashboardCommandPort) List(context.Context, dashboard.CommandCardFilter) ([]dashboard.CommandCard, error) {
	return nil, nil
}

type externalDashboardPromptPort struct{ externalDashboardPorts }

func (externalDashboardPromptPort) List(context.Context, dashboard.PromptTemplateFilter) ([]dashboard.PromptTemplate, error) {
	return nil, nil
}

type externalDashboardSharedPort struct{ externalDashboardPorts }

func (externalDashboardSharedPort) List(context.Context, dashboard.SharedFileFilter) ([]dashboard.SharedFile, error) {
	return nil, nil
}

// TestDashboardPortsAreExternallyImplementable 固定 App 可跨包实现全部九个 dashboard 读端口。
func TestDashboardPortsAreExternallyImplementable(t *testing.T) {
	t.Parallel()
	var _ dashboard.AgentStatusReader = externalDashboardPorts{}
	var _ dashboard.AILogReader = externalDashboardAILogPort{}
	var _ dashboard.AuditLogReader = externalDashboardAuditPort{}
	var _ dashboard.BusLogReader = externalDashboardBusPort{}
	var _ dashboard.SystemLogReader = externalDashboardSystemPort{}
	var _ dashboard.DBQueryExecutor = externalDashboardPorts{}
	var _ dashboard.CommandCardReader = externalDashboardCommandPort{}
	var _ dashboard.PromptTemplateReader = externalDashboardPromptPort{}
	var _ dashboard.SharedFileReader = externalDashboardSharedPort{}
}
