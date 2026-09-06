package claudewire

import (
	"encoding/json"
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
