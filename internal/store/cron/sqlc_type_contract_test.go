package cron

import (
	"strings"
	"testing"
)

func TestCronQueryPinsAmbiguousParametersToText(t *testing.T) {
	t.Parallel()
	sql := readCronQuerySQL(t, "cron_job.sql")
	for fragment, wantCount := range map[string]int{
		"CAST(sqlc.arg(cursor) AS TEXT)":    2,
		"CAST(sqlc.arg(thread_id) AS TEXT)": 2,
		"CAST(sqlc.arg(agent_id) AS TEXT)":  2,
	} {
		if got := strings.Count(sql, fragment); got != wantCount {
			t.Fatalf("cron_job.sql contains %q %d times, want %d", fragment, got, wantCount)
		}
	}
	for _, forbidden := range []string{
		"NULLIF(sqlc.arg(thread_id), '')",
		"NULLIF(sqlc.arg(agent_id), '')",
		"sqlc.arg(cursor) = ''",
		"id > sqlc.arg(cursor)",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("cron_job.sql contains untyped parameter expression %q", forbidden)
		}
	}
}
