package sqlc

import (
	"strings"
	"testing"
)

func TestAppendTaskDagRunEvent_UsesSQLiteJSONAppend(t *testing.T) {
	if !strings.Contains(appendTaskDagRunEvent, "json_insert(COALESCE(events, '[]'), '$[#]', json(?1))") {
		t.Fatalf("appendTaskDagRunEvent must use SQLite JSON array append semantics; got:\n%s", appendTaskDagRunEvent)
	}
	for _, forbidden := range []string{"jsonb_build_array", "::jsonb", " || $"} {
		if strings.Contains(appendTaskDagRunEvent, forbidden) {
			t.Fatalf("appendTaskDagRunEvent still contains PG JSON fragment %q; got:\n%s", forbidden, appendTaskDagRunEvent)
		}
	}
}
