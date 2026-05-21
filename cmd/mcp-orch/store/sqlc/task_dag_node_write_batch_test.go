package sqlc

import (
	"strings"
	"testing"
)

func TestBatchUpsertTaskDagNodesTargetsTemplatePartialUniqueIndex(t *testing.T) {
	want := "ON CONFLICT (dag_key, node_key) WHERE run_id IS NULL DO UPDATE"
	if !strings.Contains(batchUpsertTaskDagNodes, want) {
		t.Fatalf("BatchUpsertTaskDagNodes conflict target must match template node partial unique index %q; query:\n%s", want, batchUpsertTaskDagNodes)
	}
}
