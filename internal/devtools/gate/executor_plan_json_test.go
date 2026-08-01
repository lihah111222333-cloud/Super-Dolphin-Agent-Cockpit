package gate

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPlanGateExecutionJSONUsesPlainTextLog(t *testing.T) {
	t.Parallel()
	want := []byte("first line\n最终失败行\n")
	encoded, err := json.Marshal(PlanGateExecution{
		GateID:    GateIDBackendTestWithGuard,
		Status:    ResultStatusFailed,
		ExitCode:  1,
		Log:       want,
		LogDigest: digestPlanLog(want),
	})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Log string `json:"log"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(wire.Log), want) {
		t.Fatalf("JSON log = %q, want plain text %q", wire.Log, want)
	}

	var decoded PlanGateExecution
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Log, want) {
		t.Fatalf("round-trip log = %q, want %q", decoded.Log, want)
	}
}
