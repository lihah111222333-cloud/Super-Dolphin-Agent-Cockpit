package wails

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"strings"
	"testing"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// TestFrontendMethodIDsMatchBackendFQN verifies that the hardcoded Wails v3
// method IDs used by the frontend (Call.ByID) match the FNV-1a hashes that
// the Wails runtime computes from the fully-qualified Go method names.
//
// If this test fails after a package rename or method signature change,
// update METHOD_IDS in cmd/agent-terminal/frontend/vue-app/services/api.js
// and all e2e test files that reference method IDs.
func TestFrontendMethodIDsMatchBackendFQN(t *testing.T) {
	// These must stay in sync with the frontend METHOD_IDS constant.
	// See: cmd/agent-terminal/frontend/vue-app/services/api.js
	expect := map[string]uint32{
		"CallAPI":            2963398832,
		"GetBuildInfo":       2341363104,
		"SaveClipboardImage": 3733550318,
		"SelectFiles":        4126105303,
		"SelectProjectDir":   3694631468,
	}

	// Wails v3 computes IDs as FNV-1a("{pkgPath}.{type}.{method}")
	// where pkgPath comes from reflect.Type.PkgPath().
	// For this package that is the import path shown below.
	const pkgPath = "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
	const typeName = "App"

	for method, wantID := range expect {
		fqn := pkgPath + "." + typeName + "." + method
		h := fnv.New32a()
		h.Write([]byte(fqn))
		gotID := h.Sum32()
		if gotID != wantID {
			t.Errorf("method %s: FQN %q → got ID %d, want %d (frontend hardcoded); update METHOD_IDS in api.js",
				method, fqn, gotID, wantID)
		}
	}
}

func TestCallAPIPreservesFrontendMetaForUILog(t *testing.T) {
	var captured json.RawMessage
	app := &App{dispatch: func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "ui/log" {
			t.Fatalf("method = %q, want ui/log", method)
		}
		captured = append(json.RawMessage(nil), params...)
		return json.RawMessage(`{"ok":true}`), nil
	}}

	_, err := app.CallAPI("ui/log", json.RawMessage(`{"entries":[],"_aoClientKind":"desktop-wails","_aoClientRoute":"/chat"}`))
	if err != nil {
		t.Fatalf("CallAPI(ui/log) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("captured params are invalid JSON: %v", err)
	}
	if got["_aoClientKind"] != "desktop-wails" || got["_aoClientRoute"] != "/chat" {
		t.Fatalf("captured meta = %#v, want _aoClientKind/_aoClientRoute preserved", got)
	}
}

func TestCallAPIStripsFrontendMetaForStrictRoutes(t *testing.T) {
	var captured json.RawMessage
	app := &App{dispatch: func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		captured = append(json.RawMessage(nil), params...)
		return json.RawMessage(`{"ok":true}`), nil
	}}

	_, err := app.CallAPI("ui/selectFiles", json.RawMessage(`{"defaultPath":"/tmp","_aoClientKind":"desktop-wails","_aoClientRoute":"/chat"}`))
	if err != nil {
		t.Fatalf("CallAPI(ui/selectFiles) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("captured params are invalid JSON: %v", err)
	}
	if _, ok := got["_aoClientKind"]; ok {
		t.Fatalf("captured params still contain _aoClientKind: %#v", got)
	}
	if got["defaultPath"] != "/tmp" {
		t.Fatalf("captured params = %#v, want defaultPath preserved", got)
	}
}

func TestCallAPIInjectsTraceContextAndStripsFrontendTraceMeta(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const spanID = "00f067aa0ba902b7"
	traceparent := "00-" + traceID + "-" + spanID + "-01"
	var captured json.RawMessage
	app := &App{dispatch: func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "ui/selectFiles" {
			t.Fatalf("method = %q, want ui/selectFiles", method)
		}
		if got := pkglogger.TraceIDFromContext(ctx); got != traceID {
			t.Fatalf("trace_id = %q, want %q", got, traceID)
		}
		if got := pkglogger.SpanIDFromContext(ctx); got != spanID {
			t.Fatalf("span_id = %q, want %q", got, spanID)
		}
		captured = append(json.RawMessage(nil), params...)
		return json.RawMessage(`{"ok":true}`), nil
	}}

	_, err := app.CallAPI("ui/selectFiles", json.RawMessage(`{"defaultPath":"/tmp","_aoTraceparent":"`+traceparent+`","_aoTraceId":"`+traceID+`","_aoSpanId":"`+spanID+`","_aoClientKind":"desktop-wails"}`))
	if err != nil {
		t.Fatalf("CallAPI(ui/selectFiles) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("captured params are invalid JSON: %v", err)
	}
	for _, key := range []string{"_aoTraceparent", "_aoTraceId", "_aoSpanId", "_aoClientKind"} {
		if _, ok := got[key]; ok {
			t.Fatalf("captured params still contain %s: %#v", key, got)
		}
	}
	if got["defaultPath"] != "/tmp" {
		t.Fatalf("captured params = %#v, want defaultPath preserved", got)
	}
}

func TestCallAPIRejectsInvalidTraceparent(t *testing.T) {
	app := &App{dispatch: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Fatal("dispatch should not be called for invalid traceparent")
		return nil, nil
	}}

	_, err := app.CallAPI("thread/list", json.RawMessage(`{"_aoTraceparent":"not-a-traceparent"}`))
	if err == nil {
		t.Fatal("CallAPI(thread/list) error = nil, want invalid traceparent error")
	}
	if !strings.Contains(err.Error(), "invalid _aoTraceparent") {
		t.Fatalf("CallAPI(thread/list) error = %v, want invalid _aoTraceparent", err)
	}
}

func TestCallAPIRejectsMismatchedTraceMetadata(t *testing.T) {
	app := &App{dispatch: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Fatal("dispatch should not be called for mismatched trace metadata")
		return nil, nil
	}}

	_, err := app.CallAPI("thread/list", json.RawMessage(`{"_aoTraceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","_aoTraceId":"11111111111111111111111111111111","_aoSpanId":"00f067aa0ba902b7"}`))
	if err == nil {
		t.Fatal("CallAPI(thread/list) error = nil, want mismatched trace metadata error")
	}
	if !strings.Contains(err.Error(), "mismatched _aoTraceId") {
		t.Fatalf("CallAPI(thread/list) error = %v, want mismatched _aoTraceId", err)
	}
}

func TestCallAPIPreservesUILogClientMetaButConsumesTraceMeta(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const spanID = "00f067aa0ba902b7"
	var captured json.RawMessage
	app := &App{dispatch: func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "ui/log" {
			t.Fatalf("method = %q, want ui/log", method)
		}
		if got := pkglogger.TraceIDFromContext(ctx); got != traceID {
			t.Fatalf("trace_id = %q, want %q", got, traceID)
		}
		if got := pkglogger.SpanIDFromContext(ctx); got != spanID {
			t.Fatalf("span_id = %q, want %q", got, spanID)
		}
		captured = append(json.RawMessage(nil), params...)
		return json.RawMessage(`{"ok":true}`), nil
	}}

	_, err := app.CallAPI("ui/log", json.RawMessage(`{"entries":[],"_aoClientKind":"desktop-wails","_aoClientRoute":"/chat","_aoTraceparent":"00-`+traceID+`-`+spanID+`-01","_aoTraceId":"`+traceID+`","_aoSpanId":"`+spanID+`"}`))
	if err != nil {
		t.Fatalf("CallAPI(ui/log) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("captured params are invalid JSON: %v", err)
	}
	if got["_aoClientKind"] != "desktop-wails" || got["_aoClientRoute"] != "/chat" {
		t.Fatalf("captured meta = %#v, want client meta preserved", got)
	}
	for _, key := range []string{"_aoTraceparent", "_aoTraceId", "_aoSpanId"} {
		if _, ok := got[key]; ok {
			t.Fatalf("captured params still contain %s: %#v", key, got)
		}
	}
}

func TestStripFrontendMeta(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strips _ao prefixed fields",
			input: `{"method":"test","_aoClientKind":"desktop-wails","_aoClientRoute":"/"}`,
			want:  `{"method":"test"}`,
		},
		{
			name:  "no _ao fields passes through",
			input: `{"method":"test","value":42}`,
			want:  `{"method":"test","value":42}`,
		},
		{
			name:  "empty object",
			input: `{}`,
			want:  `{}`,
		},
		{
			name:  "non-object passes through",
			input: `"hello"`,
			want:  `"hello"`,
		},
		{
			name:  "only _ao fields results in empty object",
			input: `{"_aoClientKind":"web","_aoClientRoute":"/chat"}`,
			want:  `{}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripFrontendMeta(json.RawMessage(tt.input))
			// Compare as unmarshalled values to ignore key ordering
			var gotVal, wantVal any
			if err := json.Unmarshal(got, &gotVal); err != nil {
				gotVal = string(got)
			}
			if err := json.Unmarshal([]byte(tt.want), &wantVal); err != nil {
				wantVal = tt.want
			}
			gotJSON, _ := json.Marshal(gotVal)
			wantJSON, _ := json.Marshal(wantVal)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("stripFrontendMeta(%s) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}
