package nodeexec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDAGPatch_UnmarshalStrict_UnknownFieldRejected(t *testing.T) {
	t.Parallel()

	var ops Ops
	err := json.Unmarshal([]byte(`[{"op":"update_dag","patch":{"title":"x","status":"running"}}]`), &ops)
	if err == nil {
		t.Fatal("unknown DAG patch field: want err, got nil")
	}
	if !errors.Is(err, ErrDAGPatchUnknownField) {
		t.Fatalf("err = %v, want errors.Is ErrDAGPatchUnknownField", err)
	}
	if !strings.Contains(err.Error(), "status") {
		t.Fatalf("err = %v, want mention status", err)
	}
}

func TestDAGPatch_UnmarshalStrict_EmptyOk(t *testing.T) {
	t.Parallel()

	var patch DAGPatch
	if err := json.Unmarshal([]byte(`{}`), &patch); err != nil {
		t.Fatalf("empty DAG patch: err = %v, want nil", err)
	}
}

func TestOpUpdateDAG_UnmarshalRejectsMissingPatch(t *testing.T) {
	t.Parallel()

	var ops Ops
	err := json.Unmarshal([]byte(`[{"op":"update_dag"}]`), &ops)
	if err == nil {
		t.Fatal("missing patch: want err, got nil")
	}
	if !errors.Is(err, ErrUpdateDAGPayloadInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrUpdateDAGPayloadInvalid", err)
	}
	if !strings.Contains(err.Error(), "patch") {
		t.Fatalf("err = %v, want mention patch", err)
	}
}

func TestOpUpdateDAG_UnmarshalRejectsUnknownWrapperField(t *testing.T) {
	t.Parallel()

	var ops Ops
	err := json.Unmarshal([]byte(`[{"op":"update_dag","patchh":{"title":"x"}}]`), &ops)
	if err == nil {
		t.Fatal("unknown wrapper field: want err, got nil")
	}
	if !errors.Is(err, ErrUpdateDAGPayloadInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrUpdateDAGPayloadInvalid", err)
	}
	if !strings.Contains(err.Error(), "patchh") {
		t.Fatalf("err = %v, want mention patchh", err)
	}
}
