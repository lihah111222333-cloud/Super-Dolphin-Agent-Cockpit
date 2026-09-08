package claudewire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeStreamProtocolContract(t *testing.T) {
	raw, err := EncodeUser("session", "request", "hello")
	if err != nil {
		t.Fatal(err)
	}
	var user userMessage
	if err := json.Unmarshal(raw, &user); err != nil {
		t.Fatal(err)
	}
	if user.Type != "user" || user.SessionID != "session" || user.UUID != "request" || user.Message.Content[0].Text != "hello" {
		t.Fatalf("wrong user wire payload: %#v", user)
	}
	t.Run("result_identity", testClaudeResultIdentity)
	for _, invalid := range []string{"log output", "null", "{}", "[]"} {
		if _, err := DecodeEvent([]byte(invalid)); err == nil {
			t.Fatalf("invalid event accepted: %q", invalid)
		}
	}
}

func testClaudeResultIdentity(t *testing.T) {
	t.Helper()
	valid := `{"type":"result","subtype":"success","is_error":false,"session_id":"session","result":"done"}`
	event, err := DecodeEvent([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	testClaudeSystemInit(t)
	text, err := event.ResultText("session")
	if err != nil || text != "done" {
		t.Fatalf("result=%q err=%v", text, err)
	}
	if _, err := event.ResultText("foreign-session"); err == nil {
		t.Fatal("foreign session accepted")
	}
	parent := "child-tool"
	event.ParentToolUseID = &parent
	if _, err := event.ResultText("session"); err == nil {
		t.Fatal("child result accepted as root result")
	}
	event.ParentToolUseID, event.IsError = nil, true
	if _, err := event.ResultText("session"); err == nil {
		t.Fatal("error result accepted as success")
	}
}

func testClaudeSystemInit(t *testing.T) {
	t.Helper()
	initEvent, err := DecodeEvent([]byte(`{"type":"system","subtype":"init","session_id":"session","tools":["Read"],"mcp_servers":[{"name":"orch","status":"connected"}]}`))
	if err != nil || !initEvent.IsSystemInit("session") || initEvent.IsSystemInit("other") || len(initEvent.MCPServers) != 1 {
		t.Fatalf("system/init=%#v err=%v", initEvent, err)
	}
}

// TestClaudeDecodeErrorSafeShape 解码失败只输出字段/类型，不泄漏错误值或JSON正文。
func TestClaudeDecodeErrorSafeShape(t *testing.T) {
	for _, tc := range []struct{ raw, field, kind string }{
		{`{"type":"assistant","message":{"content":"PRIVATE_CONTENT"}}`, "message.content", "string"},
		{`{"type":"result","result":{"secret":"PRIVATE_CONTENT"}}`, "result", "object"},
		{`{"type":"result","session_id":987654321}`, "session_id", "number"},
	} {
		t.Run(tc.field+tc.kind, func(t *testing.T) {
			_, err := DecodeEvent([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), "field="+tc.field) || !strings.Contains(err.Error(), "json_type="+tc.kind) {
				t.Fatalf("wrong safe diagnostic: %v", err)
			}
			if strings.Contains(err.Error(), "PRIVATE_CONTENT") || strings.Contains(err.Error(), "987654321") {
				t.Fatalf("diagnostic exposed value: %v", err)
			}
		})
	}
}

// TestClaudeSystemTextMessage 保留真实system/message:string元数据，不能冒充模型报告或用户回执。
func TestClaudeSystemTextMessage(t *testing.T) {
	event, err := DecodeEvent([]byte(`{"type":"system","subtype":"permission_denied","session_id":"session","uuid":"metadata","message":"tool status metadata"}`))
	if err != nil {
		t.Fatalf("valid system message rejected: %v", err)
	}
	if event.Type != "system" || event.SessionID != "session" || event.UUID != "metadata" || event.SystemMessage != "tool status metadata" || len(event.Message.Content) != 0 || event.Result != "" || event.IsReplay {
		t.Fatalf("metadata changed model identity: %+v", event)
	}
	if _, err := event.ResultText("session"); err == nil {
		t.Fatal("metadata became root result")
	}
}

// TestClaudeMessageDiscriminatorRejectsInvalidTypes 字符串元数据仅属于system，非法帧继续阻断。
func TestClaudeMessageDiscriminatorRejectsInvalidTypes(t *testing.T) {
	for _, raw := range []string{
		`{"type":"user","isReplay":true,"session_id":"session","uuid":"request","message":"not-native-ack"}`,
		`{"type":"assistant","message":"not-assistant-blocks"}`,
		`{"type":"system","message":42}`,
		`{"type":"system","message":[]}`,
		`{"type":"result","result":{"text":"not-a-result"}}`,
		`{"type":"system","message":"metadata"} {}`,
	} {
		if _, err := DecodeEvent([]byte(raw)); err == nil {
			t.Fatalf("invalid message shape accepted: %s", raw)
		}
	}
}

// TestClaudeEventFieldsRoundTrip 动态填充生产Event字段，验证message分支拆分不遗漏其它身份/确认字段。
func TestClaudeEventFieldsRoundTrip(t *testing.T) {
	var expected Event
	fillClaudeEventFields(t, reflect.ValueOf(&expected).Elem())
	expected.Type = "assistant"
	expected.SystemMessage = ""
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := DecodeEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("event decoding lost a populated producer field")
	}
}

// fillClaudeEventFields 由类型动态构造非零字段值，未知类型不能静默遗漏。
func fillClaudeEventFields(t *testing.T, value reflect.Value) {
	t.Helper()
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			fillClaudeEventFields(t, value.Field(i))
		}
	case reflect.String:
		value.SetString("field-marker")
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Slice:
		value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		fillClaudeEventFields(t, value.Index(0))
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		fillClaudeEventFields(t, value.Elem())
	default:
		t.Fatalf("uncovered event field type %s", value.Type())
	}
}
