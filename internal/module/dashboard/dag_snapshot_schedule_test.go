package dashboard

import (
	"testing"
	"time"
)

func TestDashboardDAGSummaryFromRowDerivesScheduleEnabled(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		update func(map[string]any)
		want   bool
	}{
		{
			name: "scheduled dag with cron and next run is enabled",
			want: true,
		},
		{
			name: "manual trigger is paused",
			update: func(row map[string]any) {
				row["trigger"] = "manual"
			},
		},
		{
			name: "missing cron expression is paused",
			update: func(row map[string]any) {
				row["cron_expr"] = " "
			},
		},
		{
			name: "missing next run is paused",
			update: func(row map[string]any) {
				row["next_run_at"] = nil
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			row := dashboardDAGRow(now)
			if tt.update != nil {
				tt.update(row)
			}
			got, err := dashboardDAGSummaryFromRow(row)
			if err != nil {
				t.Fatalf("dashboardDAGSummaryFromRow() error = %v", err)
			}
			if got.ScheduleEnabled != tt.want {
				t.Fatalf("ScheduleEnabled = %v, want %v for trigger=%q cron=%q next_run_at=%#v",
					got.ScheduleEnabled, tt.want, row["trigger"], row["cron_expr"], row["next_run_at"])
			}
		})
	}
}
