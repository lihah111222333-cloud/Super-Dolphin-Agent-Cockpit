package codexapp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
)

type dynamicToolDetailExpectation struct {
	name         string
	description  string
	inputSchema  json.RawMessage
	outputSchema json.RawMessage
}

func TestToolBridgeStartSessionAdvertisesDynamicToolDetails(t *testing.T) {
	recorder := &toolBridgeRPCRecorder{}
	serverURL := startToolBridgeRPCServer(t, recorder)
	manager := &ServerManager{}
	want := echoDynamicToolDetail()
	driver := requireToolBridgeDriver(t, newDriver(nil, nil, nil, nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return []codexprotocol.DynamicToolSchema{{Name: want.name, Description: want.description, InputSchema: want.inputSchema, OutputSchema: want.outputSchema}}, nil
	}))

	sessionAny, err := driver.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	defer closeCodexTestSession(t, requireCodexSession(t, sessionAny, "StartSession"))

	assertThreadStartDynamicToolDetails(t, recorder.threadStartParamsSnapshot(), want)
}

func TestToolBridgeStartSessionAdvertisesScopedSurfaceToolDetails(t *testing.T) {
	recorder := &toolBridgeRPCRecorder{}
	serverURL := startToolBridgeRPCServer(t, recorder)
	want := grepDynamicToolDetail()
	driver := requireToolBridgeDriver(t, newDriver(nil, nil, nil, nil, &ServerManager{}, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, nil))
	driver.prepareTools = func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
		return []codexprotocol.DynamicToolSchema{{Name: want.name, Description: want.description, InputSchema: want.inputSchema, OutputSchema: want.outputSchema}}, nil
	}
	driver.bindTools = func(contract.CodexToolSurfaceScope) error { return nil }

	sessionAny, err := driver.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	defer closeCodexTestSession(t, requireCodexSession(t, sessionAny, "StartSession"))

	assertThreadStartDynamicToolDetails(t, recorder.threadStartParamsSnapshot(), want)
}

func echoDynamicToolDetail() dynamicToolDetailExpectation {
	return dynamicToolDetailExpectation{
		name:         "tool.echo",
		description:  "echo payload",
		inputSchema:  json.RawMessage(`{"type":"object","properties":{"payload":{"type":"string","description":"echo text"}}}`),
		outputSchema: json.RawMessage(`{"type":"object","properties":{"echoed":{"type":"string","description":"echo result"}}}`),
	}
}

func grepDynamicToolDetail() dynamicToolDetailExpectation {
	return dynamicToolDetailExpectation{
		name:         "grep",
		description:  "grep source",
		inputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"search query"}}}`),
		outputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"object","description":"matches by file"}}}`),
	}
}

func assertThreadStartDynamicToolDetails(t *testing.T, params map[string]any, want dynamicToolDetailExpectation) {
	t.Helper()
	tool := requireSingleDynamicTool(t, params)
	if tool["name"] != want.name || tool["description"] != want.description {
		t.Fatalf("dynamic tool = %#v, want name=%q description=%q", tool, want.name, want.description)
	}
	if _, ok := tool["deferLoading"]; ok {
		t.Fatalf("dynamic tool = %#v, must not emit unsupported deferLoading", tool)
	}
	assertJSONAnyEqual(t, "inputSchema", tool["inputSchema"], want.inputSchema)
	assertJSONAnyEqual(t, "outputSchema", tool["outputSchema"], want.outputSchema)
}

func requireSingleDynamicTool(t *testing.T, params map[string]any) map[string]any {
	t.Helper()
	tools, ok := params["dynamicTools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("dynamicTools = %#v, want one tool", params["dynamicTools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("dynamicTools[0] = %#v, want object", tools[0])
	}
	return tool
}

func assertJSONAnyEqual(t *testing.T, label string, got any, wantRaw json.RawMessage) {
	t.Helper()
	var want any
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("%s expected JSON invalid: %v", label, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", label, got, want)
	}
}
