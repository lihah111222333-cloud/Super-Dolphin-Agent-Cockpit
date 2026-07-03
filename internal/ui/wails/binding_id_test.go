package wails

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// TestFrontendMethodIDsMatchBackendFQN verifies that the hardcoded Wails v3
// method IDs used by the frontend (Call.ByID) match the FNV-1a hashes that
// the Wails runtime computes from the fully-qualified Go method names.
//
// If this test fails after a package rename or method signature change,
// update METHOD_IDS in frontend-app/src/shared/api/wailsBridge.js
// and all e2e test files that reference method IDs.
func TestFrontendMethodIDsMatchBackendFQN(t *testing.T) {
	// These must stay in sync with the frontend METHOD_IDS constant.
	// See: frontend-app/src/shared/api/wailsBridge.js
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
		assertContextTraceChild(t, ctx, traceID, spanID)
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

func assertContextTraceChild(t *testing.T, ctx context.Context, traceID string, parentSpanID string) {
	t.Helper()
	if got := pkglogger.TraceIDFromContext(ctx); got != traceID {
		t.Fatalf("trace_id = %q, want %q", got, traceID)
	}
	spanID := pkglogger.SpanIDFromContext(ctx)
	if spanID == "" || spanID == parentSpanID {
		t.Fatalf("span_id = %q, want generated backend child span", spanID)
	}
	if got := pkglogger.ParentSpanIDFromContext(ctx); got != parentSpanID {
		t.Fatalf("parent_span_id = %q, want %q", got, parentSpanID)
	}
	trace, ok := observability.TraceFromContext(ctx)
	if !ok {
		t.Fatal("observability trace context missing")
	}
	if trace.TraceID != traceID || trace.SpanID != spanID || trace.ParentSpanID != parentSpanID {
		t.Fatalf("observability trace context = (%q,%q,%q), want (%q,%q,%q)", trace.TraceID, trace.SpanID, trace.ParentSpanID, traceID, spanID, parentSpanID)
	}
}

func assertWailsTraceEvent(t *testing.T, event observability.TraceEvent, traceID string, parentSpanID string) {
	t.Helper()
	if event.TraceID != traceID || event.ParentSpanID != parentSpanID || event.SpanID == "" || event.SpanID == parentSpanID {
		t.Fatalf("trace context = (%q,%q,%q), want child of frontend span", event.TraceID, event.SpanID, event.ParentSpanID)
	}
}

func assertTracePayloadExcludes(t *testing.T, event observability.TraceEvent, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal event: %v", err)
	}
	payload := string(encoded)
	for _, current := range forbidden {
		if strings.Contains(payload, current) {
			t.Fatalf("trace event leaked %q: %s", current, payload)
		}
	}
}

type failingTraceSink struct{ err error }

func (s failingTraceSink) Append(context.Context, observability.TraceEvent) error { return s.err }

func TestCallAPITraceWriteFailureDoesNotBlockDispatch(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "true"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	svc := observability.NewService(cfg, observability.WithSink(failingTraceSink{err: errors.New("trace sink unavailable")}))
	called := false
	app := &App{
		observability: svc,
		dispatch: func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			called = true
			assertContextTraceChild(t, ctx, "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7")
			return json.RawMessage(`{"ok":true}`), nil
		},
	}

	got, err := app.CallAPI("thread/start", json.RawMessage(`{"_aoTraceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}`))
	if err != nil {
		t.Fatalf("CallAPI() error = %v, want trace sink failure to be best-effort", err)
	}
	if !called || got == nil {
		t.Fatalf("dispatch called = %v, result = %#v", called, got)
	}
}

func TestCallAPIRecordsLifecycleEventsWithoutRawParams(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "true"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	svc := observability.NewService(cfg)
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const frontendSpanID = "00f067aa0ba902b7"
	app := &App{
		observability: svc,
		dispatch: func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			if method != "thread/start" {
				t.Fatalf("method = %q, want thread/start", method)
			}
			return json.RawMessage(`{"ok":true}`), nil
		},
	}

	_, err = app.CallAPI("thread/start", json.RawMessage(`{"baseInstructions":"secret prompt","_aoTraceparent":"00-`+traceID+`-`+frontendSpanID+`-01","_aoTraceId":"`+traceID+`","_aoSpanId":"`+frontendSpanID+`"}`))
	if err != nil {
		t.Fatalf("CallAPI(thread/start) error = %v", err)
	}
	events := svc.Query(context.Background(), observability.Query{TraceID: traceID}).Events
	if len(events) != 2 {
		t.Fatalf("trace event count = %d, want 2: %#v", len(events), events)
	}
	if events[0].Kind != "wails.call_api.start" || events[1].Kind != "wails.call_api.done" {
		t.Fatalf("event kinds = %q, %q; want wails start/done", events[0].Kind, events[1].Kind)
	}
	for _, event := range events {
		assertWailsTraceEvent(t, event, traceID, frontendSpanID)
		assertTracePayloadExcludes(t, event, "secret prompt", "params_preview", "ParamsPreview", "rpcParamPreview")
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
		assertContextTraceChild(t, ctx, traceID, spanID)
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
			name:  "strips known frontend meta fields",
			input: `{"method":"test","_aoClientKind":"desktop-wails","_aoClientRoute":"/"}`,
			want:  `{"method":"test"}`,
		},
		{
			name:  "preserves unknown _ao fields for strict handler rejection",
			input: `{"method":"test","_aoTypo":"leak"}`,
			want:  `{"method":"test","_aoTypo":"leak"}`,
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
