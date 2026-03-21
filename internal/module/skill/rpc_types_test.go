package skill

import (
	"encoding/json"
	"testing"
)

func TestExecParamsUnmarshalV3Shape(t *testing.T) {
	t.Parallel()

	var got execParams
	if err := json.Unmarshal([]byte(`{"command":"git","args":["status"],"cwd":"/tmp"}`), &got); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got.Command != "git" || len(got.Args) != 1 || got.Args[0] != "status" || got.CWD != "/tmp" {
		t.Fatalf("unexpected exec params: %+v", got)
	}
	if got.Env != nil {
		t.Fatalf("unexpected env: %#v", got.Env)
	}
}

func TestExecParamsUnmarshalLegacyArgvEnvShape(t *testing.T) {
	t.Parallel()

	var got execParams
	if err := json.Unmarshal([]byte(`{"argv":["git","status"],"cwd":"/tmp","env":{"TEST_E2E_X":"1"}}`), &got); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got.Command != "git" || len(got.Args) != 1 || got.Args[0] != "status" {
		t.Fatalf("legacy argv not mapped: %+v", got)
	}
	if got.CWD != "/tmp" {
		t.Fatalf("cwd mismatch: %q", got.CWD)
	}
	if got.Env["TEST_E2E_X"] != "1" || len(got.Env) != 1 {
		t.Fatalf("env mismatch: %#v", got.Env)
	}
}
