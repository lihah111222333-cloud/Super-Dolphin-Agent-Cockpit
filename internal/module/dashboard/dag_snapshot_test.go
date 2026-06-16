package dashboard

import (
	"strings"
	"testing"
	"time"
)

// TestDashboardDAGSummaryFromRowReturnsJSONMappingError verifies malformed JSON fields fail row mapping.
func TestDashboardDAGSummaryFromRowReturnsJSONMappingError(t *testing.T) {
	t.Parallel()

	now := time.Unix(1, 0).UTC()
	row := map[string]any{
		"id":         int64(1),
		"dag_key":    "daily",
		"version":    int64(1),
		"metadata":   func() {},
		"created_at": now,
		"updated_at": now,
	}
	_, err := dashboardDAGSummaryFromRow(row)
	if err == nil {
		t.Fatal("dashboardDAGSummaryFromRow() error = nil, want JSON mapping error")
	}
	if !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("dashboardDAGSummaryFromRow() error = %v, want metadata error", err)
	}
}
