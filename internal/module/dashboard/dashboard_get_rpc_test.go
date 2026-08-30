package dashboard

import (
	"context"
	"encoding/json"
	"testing"
)

// TestUIDashboardGetAcceptsProjectCWDCompatibilityField 验证通用 dashboard 请求仍接受项目 cwd 字段。
func TestUIDashboardGetAcceptsProjectCWDCompatibilityField(t *testing.T) {
	t.Parallel()

	server := newDashboardTestServer(t, &service{})
	result, err := server.Dispatch(context.Background(), "ui/dashboard/get", json.RawMessage(`{
		"cwd":"/repo/app",
		"page":"commands"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/dashboard/get) error = %v", err)
	}
	var page DashboardPage
	if err := json.Unmarshal(result, &page); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if page.CommandCards == nil {
		t.Fatalf("Dispatch(ui/dashboard/get) returned nil commandCards: %#v", page)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil {
		t.Fatalf("json.Unmarshal(fields) error = %v", err)
	}
	if _, ok := fields["prompts"]; ok {
		t.Fatalf("Dispatch(ui/dashboard/get) retained retired prompts field: %s", result)
	}
}
