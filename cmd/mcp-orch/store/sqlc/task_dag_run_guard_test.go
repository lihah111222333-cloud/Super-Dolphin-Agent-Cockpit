package sqlc

import (
	"strings"
	"testing"
)

func TestTaskDagRunEventAppendQueriesUseGoComputedEvents(t *testing.T) {
	if !strings.Contains(loadTaskDagRunEventsForAppend, "SELECT run_key, CAST(events AS BLOB) AS events") {
		t.Fatalf("loadTaskDagRunEventsForAppend must load current events for Go append; got:\n%s", loadTaskDagRunEventsForAppend)
	}
	if !strings.Contains(updateTaskDagRunEventsAfterAppend, "SET events = ?1") {
		t.Fatalf("updateTaskDagRunEventsAfterAppend must persist Go-computed events; got:\n%s", updateTaskDagRunEventsAfterAppend)
	}
	combined := loadTaskDagRunEventsForAppend + updateTaskDagRunEventsAfterAppend
	for _, forbidden := range []string{"jsonb_build_array", "::jsonb", " || $"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("run event append queries still contain PG JSON fragment %q; got:\n%s\n%s", forbidden, loadTaskDagRunEventsForAppend, updateTaskDagRunEventsAfterAppend)
		}
	}
}
