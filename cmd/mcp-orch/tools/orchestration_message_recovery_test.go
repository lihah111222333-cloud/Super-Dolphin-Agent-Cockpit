package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestSendMessageRecoveryEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name          string
		wait          bool
		submitErr     error
		seq           int64
		phase, status string
	}{
		{"ordinary unknown", false, context.DeadlineExceeded, 9, "submit", "unknown"},
		{"follow-up unknown", true, context.DeadlineExceeded, 9007199254740993, "submit", "unknown"},
		{"submitted waiting", true, nil, 3, "wait_report", "submitted"},
		{"real zero baseline", true, context.DeadlineExceeded, 0, "submit", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			submits := 0
			handler := handleSendMessageWithStub(&golden.OrchestrationStub{
				SnapshotFunc: func(context.Context, string) (contract.AgentSnapshot, error) {
					return contract.AgentSnapshot{ThreadID: "thread-b", State: "idle"}, nil
				},
				GetReportFunc: func(context.Context, string) (contract.AgentReportResult, error) {
					return contract.AgentReportResult{ReportSeq: tc.seq, Report: "old", State: "idle"}, nil
				},
				SubmitTurnFunc: func(context.Context, contract.TurnSubmission) error { submits++; return tc.submitErr },
			})
			_, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{"agent_id": "agent-b", "message": "follow-up", "wait_report": tc.wait, "timeout_ms": 10}))
			if err == nil || submits != 1 {
				t.Fatalf("error=%v submits=%d, want one submission and recovery error", err, submits)
			}
			var cursor *int64
			if tc.wait {
				cursor = &tc.seq
			}
			expected := sendMessageRecoveryMetadata{
				Phase: tc.phase, SubmissionStatus: tc.status, AgentID: "agent-b", ThreadID: "thread-b", PreviousReportSeq: cursor,
				Recovery: sendMessageRecoveryQuery{Tool: "get_agent_report", Arguments: sendMessageRecoveryArguments{AgentID: "agent-b", Wait: tc.wait, AfterReportSeq: cursor}},
			}
			assertSendMessageRecoveryEnvelope(t, err, expected)
		})
	}
}

// assertSendMessageRecoveryEnvelope 检查真实 MCP envelope 的恢复参数和字段完整性。
func assertSendMessageRecoveryEnvelope(t *testing.T, err error, expected sendMessageRecoveryMetadata) {
	t.Helper()
	envelope := mcpcommon.NewToolErrorEnvelope("send_message", err)
	if envelope.Retryable || !strings.Contains(envelope.Hint, "not") || !strings.Contains(envelope.Hint, "acknowledgement") {
		t.Fatalf("unsafe recovery envelope: %#v", envelope)
	}
	metadata := recoveryMetadataFromFinalResult(t, envelope, expected.Recovery)
	var actual sendMessageRecoveryMetadata
	if decodeErr := json.Unmarshal(metadata, &actual); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("recovery=%#v, want %#v", actual, expected)
	}
	assertRecoveryJSONFields(t, reflect.ValueOf(expected), metadata)
}

// recoveryMetadataFromFinalResult 覆盖公共 builder 后的结构化结果与文本查询两个消费端。
func recoveryMetadataFromFinalResult(t *testing.T, envelope mcpcommon.ToolErrorEnvelope, query sendMessageRecoveryQuery) json.RawMessage {
	t.Helper()
	result, buildErr := mcpcommon.BuildToolCallResult(envelope)
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var wire struct {
		IsError           bool `json:"isError"`
		StructuredContent struct {
			Meta map[string]json.RawMessage `json:"meta"`
		} `json:"structuredContent"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if decodeErr := json.Unmarshal(raw, &wire); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !wire.IsError {
		t.Fatal("final MCP result isError must be true")
	}
	expectedQuery, queryErr := json.Marshal(query)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if len(wire.Content) != 1 || !strings.Contains(wire.Content[0].Text, string(expectedQuery)) {
		t.Fatalf("text content lost typed recovery query: %s", raw)
	}
	metadata, exists := wire.StructuredContent.Meta["submission_recovery"]
	if !exists {
		t.Fatal("missing submission_recovery in final MCP envelope")
	}

	return metadata
}

// assertRecoveryJSONFields 以私有类型动态枚举字段，核对每层 JSON 的 missing、stale 与精确值。
func assertRecoveryJSONFields(t *testing.T, producer reflect.Value, raw json.RawMessage) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	expectedCount := 0
	for i := 0; i < producer.NumField(); i++ {
		field := producer.Type().Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")
		if tag[0] == "" || tag[0] == "-" {
			t.Fatalf("producer field %s lacks explicit JSON contract", field.Name)
		}
		value := producer.Field(i)
		if value.Kind() == reflect.Pointer && value.IsNil() {
			if _, exists := fields[tag[0]]; exists {
				t.Fatalf("absent cursor %s was fabricated", tag[0])
			}
			continue
		}
		expectedCount++
		encoded, exists := fields[tag[0]]
		if !exists {
			t.Fatalf("missing producer field %s", tag[0])
		}
		assertRecoveryJSONValue(t, value, encoded, tag[0])
	}
	if len(fields) != expectedCount {
		t.Fatalf("stale fields: got %d, want %d", len(fields), expectedCount)
	}
}

func assertRecoveryJSONValue(t *testing.T, value reflect.Value, raw json.RawMessage, name string) {
	t.Helper()
	if value.Kind() == reflect.Struct {
		assertRecoveryJSONFields(t, value, raw)
		return
	}
	expected, err := json.Marshal(value.Interface())
	if err != nil {
		t.Fatal(err)
	}
	if string(expected) != string(raw) {
		t.Fatalf("field %s=%s, want %s", name, raw, expected)
	}
}

func TestSendMessageRecoveryCopiesObservedCursor(t *testing.T) {
	seq := int64(7)
	err := sendMessageRecoveryError("submission_outcome_unknown", context.DeadlineExceeded, contract.TurnSubmission{AgentID: "agent-b", ThreadID: "thread-b"}, &seq)
	seq = 99
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) {
		t.Fatalf("missing coded error: %v", err)
	}
	metadata := coded.Meta["submission_recovery"].(sendMessageRecoveryMetadata)
	if *metadata.PreviousReportSeq != 7 || *metadata.Recovery.Arguments.AfterReportSeq != 7 {
		t.Fatalf("cursor aliases caller: %#v", metadata)
	}
}
