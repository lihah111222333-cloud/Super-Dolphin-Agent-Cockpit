package rpc

import (
	"strings"
	"testing"
)

func TestRejectUnknownJSONFieldsDerivesFieldsFromTags(t *testing.T) {
	t.Parallel()

	type baseWire struct {
		ThreadID string `json:"thread_id"`
	}
	type currentWire struct {
		baseWire
		Model string `json:"model,omitempty"`
		Name  string `json:",omitempty"`
		Skip  string `json:"-"`
	}
	type legacyWire struct {
		ThreadID string `json:"threadId"`
	}

	if err := RejectUnknownJSONFields([]byte(`{"thread_id":"t1","threadId":"t1","model":"m","Name":"n"}`), "demo", currentWire{}, legacyWire{}); err != nil {
		t.Fatalf("RejectUnknownJSONFields() error = %v", err)
	}

	err := RejectUnknownJSONFields([]byte(`{"thread_id":"t1","Skip":"hidden"}`), "demo", currentWire{}, legacyWire{})
	if err == nil || !strings.Contains(err.Error(), `demo: unknown field "Skip"`) {
		t.Fatalf("RejectUnknownJSONFields() error = %v, want Skip rejection", err)
	}
}
