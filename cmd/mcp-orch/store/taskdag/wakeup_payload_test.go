package taskdag

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func canonicalDownstreamPayload(agentID string) json.RawMessage {
	return json.RawMessage(`{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":` + strconv.Quote(agentID) + `}`)
}

func TestDecodeWakeupPayloadAcceptsCanonicalDownstream(t *testing.T) {
	raw, err := json.Marshal(DownstreamWakeupPayload{AgentID: "agent-a"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	decoded, err := DecodeWakeupPayload(raw)
	if err != nil {
		t.Fatalf("DecodeWakeupPayload() error = %v", err)
	}
	if decoded.Downstream == nil || decoded.Legacy != nil {
		t.Fatalf("decoded union = %+v, want downstream-only", decoded)
	}
}

func TestDecodeWakeupPayloadRejectsInvalidAndMixedShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "missing discriminator", raw: `{"agent_id":"agent-a"}`},
		{name: "missing schema", raw: `{"wakeup_payload_kind":"dag_downstream","agent_id":"agent-a"}`},
		{name: "unknown discriminator", raw: `{"wakeup_payload_kind":"future","schema_version":1,"agent_id":"agent-a"}`},
		{name: "unknown field", raw: `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"agent-a","promt":"typo"}`},
		{name: "mixed union", raw: `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"agent-a","prompt":"legacy"}`},
		{name: "missing agent", raw: `{"wakeup_payload_kind":"dag_downstream","schema_version":1}`},
		{name: "duplicate discriminator", raw: `{"wakeup_payload_kind":"dag_downstream","wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"agent-a"}`},
		{name: "array root", raw: `[]`},
		{name: "agent object", raw: `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":{},"upstream_outputs":[]}`},
		{name: "null outputs", raw: `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"agent-a","upstream_outputs":null}`},
		{name: "null optional legacy field", raw: `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"agent-a","cwd":null}`},
		{name: "null nested legacy field", raw: `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"agent-a","command":[null]}`},
		{name: "agent too long", raw: `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"` + string(make([]byte, maxWakeupAgentIDBytes+1)) + `"}`},
		{name: "malformed", raw: `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeWakeupPayload([]byte(tc.raw)); !errors.Is(err, ErrInvalidWakeupPayload) {
				t.Fatalf("DecodeWakeupPayload() error = %v, want ErrInvalidWakeupPayload", err)
			}
		})
	}
}

func TestDecodeWakeupPayloadAcceptsExplicitLegacyOnly(t *testing.T) {
	raw := []byte(`{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"agent-a","prompt":"hello"}`)
	decoded, err := DecodeWakeupPayload(raw)
	if err != nil {
		t.Fatalf("DecodeWakeupPayload() error = %v", err)
	}
	if decoded.Legacy == nil || decoded.Downstream != nil || decoded.Legacy.Prompt != "hello" {
		t.Fatalf("decoded legacy = %+v, want legacy prompt", decoded)
	}
}

func TestDecodeWakeupPayloadRejectsNonCanonicalAgentIdentity(t *testing.T) {
	for _, raw := range []string{
		`{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":" agent-a"}`,
		`{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"agent-a "}`,
		`{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":" agent-a","prompt":"hello"}`,
	} {
		if _, err := DecodeWakeupPayload([]byte(raw)); !errors.Is(err, ErrInvalidWakeupPayload) {
			t.Fatalf("DecodeWakeupPayload(%s) error = %v, want canonical agent identity rejection", raw, err)
		}
	}
}

func TestValidateWakeupPayloadBranchRejectsSurroundingAgentWhitespace(t *testing.T) {
	cases := []struct {
		name         string
		targetAgent  string
		payloadAgent string
	}{
		{name: "target leading", targetAgent: " agent-a", payloadAgent: "agent-a"},
		{name: "target trailing", targetAgent: "agent-a ", payloadAgent: "agent-a"},
		{name: "payload leading", targetAgent: "agent-a", payloadAgent: " agent-a"},
		{name: "payload trailing", targetAgent: "agent-a", payloadAgent: "agent-a "},
		{name: "both surrounding", targetAgent: " agent-a ", payloadAgent: " agent-a "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateWakeupPayloadBranch(canonicalDownstreamPayload(tc.payloadAgent), tc.targetAgent, WakeupPayloadKindDownstream); !errors.Is(err, ErrInvalidWakeupPayload) {
				t.Fatalf("validateWakeupPayloadBranch() error = %v, want surrounding whitespace rejection", err)
			}
		})
	}
	if err := validateWakeupPayloadBranch(canonicalDownstreamPayload("agent-a"), "agent-a", WakeupPayloadKindDownstream); err != nil {
		t.Fatalf("canonical agent identity rejected: %v", err)
	}
}

func TestValidateEnqueueWakeupPayloadRejectsUntaggedAndMixed(t *testing.T) {
	for _, raw := range []string{`{}`, `{"agent_id":"agent-a"}`, `{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"agent-a","prompt":"mixed"}`} {
		if err := validateEnqueueWakeupPayload("node_start", "agent-a", []byte(raw)); !errors.Is(err, ErrInvalidWakeupPayload) {
			t.Fatalf("validateEnqueueWakeupPayload(%s) error = %v, want ErrInvalidWakeupPayload", raw, err)
		}
	}
	for _, kind := range []string{"turn_complete_retry", "terminal_failure_compensation"} {
		payload := []byte(`{"summary":"internal"}`)
		if kind == "terminal_failure_compensation" {
			payload = []byte(`{"reason":"x","store_error":"y","fail_fast":false}`)
		}
		if err := validateEnqueueWakeupPayload(kind, "mcp-orch", payload); err != nil {
			t.Fatalf("validateEnqueueWakeupPayload(%s) error = %v, valid internal payload rejected", kind, err)
		}
	}
}

func TestValidateEnqueueWakeupPayloadRejectsMalformedInternal(t *testing.T) {
	if err := validateEnqueueWakeupPayload("terminal_failure_compensation", "mcp-orch", []byte(`{"reason":"x"}`)); !errors.Is(err, ErrInvalidWakeupPayload) {
		t.Fatalf("terminal compensation error = %v, want missing-field rejection", err)
	}
	if err := validateEnqueueWakeupPayload("turn_complete_retry", "mcp-orch", []byte(`[]`)); !errors.Is(err, ErrInvalidWakeupPayload) {
		t.Fatalf("turn completion error = %v, want object rejection", err)
	}
}

func TestValidateEnqueueWakeupPayloadRequiresExactInternalTarget(t *testing.T) {
	for _, target := range []string{" mcp-orch", "mcp-orch ", "MCP-ORCH"} {
		if err := validateEnqueueWakeupPayload("turn_complete_retry", target, []byte(`{"summary":"internal"}`)); !errors.Is(err, ErrInvalidWakeupPayload) {
			t.Fatalf("turn_complete_retry target %q error = %v, want exact mcp-orch rejection", target, err)
		}
	}
	if err := validateEnqueueWakeupPayload("turn_complete_retry", "mcp-orch", []byte(`{"summary":"internal"}`)); err != nil {
		t.Fatalf("exact mcp-orch target rejected: %v", err)
	}
}

func TestValidateEnqueueWakeupPayloadRejectsOversizedInternal(t *testing.T) {
	oversized := []byte(`{"summary":"` + strings.Repeat("x", maxWakeupPayloadBytes) + `"}`)
	if err := validateEnqueueWakeupPayload("turn_complete_retry", "mcp-orch", oversized); !errors.Is(err, ErrInvalidWakeupPayload) {
		t.Fatalf("oversized turn completion error = %v, want size rejection", err)
	}
	terminal := []byte(`{"reason":"` + strings.Repeat("x", maxWakeupInternalTextBytes+1) + `","store_error":"","fail_fast":false}`)
	if err := validateEnqueueWakeupPayload("terminal_failure_compensation", "mcp-orch", terminal); !errors.Is(err, ErrInvalidWakeupPayload) {
		t.Fatalf("oversized terminal compensation error = %v, want text bound rejection", err)
	}
}

func TestWakeupIdentityInputsRejectEmptyOrNonCanonicalValues(t *testing.T) {
	for _, value := range []string{"", " ", " key ", "\x00key"} {
		if err := validateWakeupIdempotencyKey(value); err == nil {
			t.Fatalf("validateWakeupIdempotencyKey(%q) unexpectedly succeeded", value)
		}
	}
	for _, value := range []string{"", " ", " worker ", "\x00worker"} {
		if err := validateWakeupWorkerID(value); err == nil {
			t.Fatalf("validateWakeupWorkerID(%q) unexpectedly succeeded", value)
		}
	}
	if err := validateWakeupIdempotencyKey("key-1"); err != nil {
		t.Fatalf("valid idempotency key rejected: %v", err)
	}
	if err := validateWakeupWorkerID("worker-1"); err != nil {
		t.Fatalf("valid worker id rejected: %v", err)
	}
}
