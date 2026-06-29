package turn

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTurnStartParamsRejectsUnknownField(t *testing.T) {
	t.Parallel()

	var params turnStartParams
	err := json.Unmarshal([]byte(`{"threadId":"thread-1","cwd":"/repo/app","prompt":"hi","surprise":true}`), &params)
	if err == nil {
		t.Fatal("json.Unmarshal(turnStartParams) error = nil, want unknown-field rejection")
	}
	if !strings.Contains(err.Error(), `turn/start: unknown field "surprise"`) {
		t.Fatalf("json.Unmarshal(turnStartParams) error = %q", err)
	}
}

func TestTurnSteerParamsRejectsUnknownField(t *testing.T) {
	t.Parallel()

	var params turnSteerParams
	err := json.Unmarshal([]byte(`{"threadId":"thread-1","expectedTurnId":"turn-1","prompt":"hi","surprise":true}`), &params)
	if err == nil {
		t.Fatal("json.Unmarshal(turnSteerParams) error = nil, want unknown-field rejection")
	}
	if !strings.Contains(err.Error(), `turn/steer: unknown field "surprise"`) {
		t.Fatalf("json.Unmarshal(turnSteerParams) error = %q", err)
	}
}
