package wails

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2/handler"
)

const frontendMethodIDsDeclaration = "const METHOD_IDS = Object.freeze({"

var frontendMethodNames = map[string]string{
	"CALL_API":             "CallAPI",
	"GET_BUILD_INFO":       "GetBuildInfo",
	"SAVE_CLIPBOARD_IMAGE": "SaveClipboardImage",
	"SELECT_FILES":         "SelectFiles",
	"SELECT_PROJECT_DIR":   "SelectProjectDir",
}

func TestParseFrontendMethodIDs(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		want    map[string]uint32
		wantErr string
	}{
		{
			name: "valid",
			source: `const METHOD_IDS = Object.freeze({
  CALL_API: 1,
  SELECT_FILES: 2,
});`,
			want: map[string]uint32{"CALL_API": 1, "SELECT_FILES": 2},
		},
		{
			name:    "missing declaration",
			source:  "const OTHER_IDS = Object.freeze({});",
			wantErr: "METHOD_IDS declaration",
		},
		{
			name: "duplicate entry",
			source: `const METHOD_IDS = Object.freeze({
  CALL_API: 1,
  CALL_API: 2,
});`,
			wantErr: "duplicate METHOD_IDS entry",
		},
		{
			name: "malformed entry",
			source: `const METHOD_IDS = Object.freeze({
  CALL_API: "1",
});`,
			wantErr: "malformed METHOD_IDS entry",
		},
		{
			name: "overflow",
			source: `const METHOD_IDS = Object.freeze({
  CALL_API: 4294967296,
});`,
			wantErr: "invalid METHOD_IDS value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFrontendMethodIDs(tt.source)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseFrontendMethodIDs() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFrontendMethodIDs() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseFrontendMethodIDs() = %#v, want %#v", got, tt.want)
			}
			for key, wantID := range tt.want {
				if got[key] != wantID {
					t.Errorf("parseFrontendMethodIDs()[%q] = %d, want %d", key, got[key], wantID)
				}
			}
		})
	}
}

func TestValidateFrontendMethodIDs(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]uint32)
		wantErr string
	}{
		{name: "valid", mutate: func(map[string]uint32) {}},
		{
			name: "missing entry",
			mutate: func(ids map[string]uint32) {
				delete(ids, "CALL_API")
			},
			wantErr: "missing METHOD_IDS entry",
		},
		{
			name: "unknown entry",
			mutate: func(ids map[string]uint32) {
				ids["UNKNOWN"] = 1
			},
			wantErr: "unknown METHOD_IDS entry",
		},
		{
			name: "changed entry",
			mutate: func(ids map[string]uint32) {
				ids["CALL_API"]++
			},
			wantErr: "changed METHOD_IDS entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, err := expectedFrontendMethodIDs(frontendMethodNames)
			if err != nil {
				t.Fatalf("expectedFrontendMethodIDs() error = %v", err)
			}
			tt.mutate(ids)
			err = validateFrontendMethodIDs(ids)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateFrontendMethodIDs() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateFrontendMethodIDs() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestExpectedFrontendMethodIDsRejectsUnknownBackendMethod(t *testing.T) {
	_, err := expectedFrontendMethodIDs(map[string]string{"UNKNOWN": "MissingMethod"})
	if err == nil || !strings.Contains(err.Error(), "MissingMethod") {
		t.Fatalf("expectedFrontendMethodIDs() error = %v, want missing backend method", err)
	}
}

func parseFrontendMethodIDs(source string) (map[string]uint32, error) {
	code, err := javascriptCodeMask(source)
	if err != nil {
		return nil, err
	}
	declarations := frontendMethodIDDeclarationStarts(code)
	if len(declarations) != 1 {
		return nil, fmt.Errorf("expected exactly one METHOD_IDS declaration, found %d", len(declarations))
	}

	declarationStart := declarations[0]
	bodyStart := declarationStart + len(frontendMethodIDsDeclaration)
	bodyEnd := strings.Index(code[bodyStart:], "});")
	if bodyEnd < 0 {
		return nil, errors.New(`METHOD_IDS declaration is missing closing "});"`)
	}

	body := strings.TrimSpace(source[bodyStart : bodyStart+bodyEnd])
	if body == "" {
		return nil, errors.New("METHOD_IDS declaration has no entries")
	}

	ids := make(map[string]uint32)
	for lineIndex, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		key, parsedID, err := parseFrontendMethodIDEntry(line, lineIndex+1)
		if err != nil {
			return nil, err
		}
		if _, duplicate := ids[key]; duplicate {
			return nil, fmt.Errorf("duplicate METHOD_IDS entry %q", key)
		}
		ids[key] = parsedID
	}
	return ids, nil
}

func parseFrontendMethodIDEntry(line string, lineNumber int) (string, uint32, error) {
	if !strings.HasSuffix(line, ",") {
		return "", 0, fmt.Errorf("malformed METHOD_IDS entry on line %d: %q", lineNumber, line)
	}

	entry := strings.TrimSpace(strings.TrimSuffix(line, ","))
	key, rawID, ok := strings.Cut(entry, ":")
	key = strings.TrimSpace(key)
	rawID = strings.TrimSpace(rawID)
	if !ok || strings.Contains(rawID, ":") || !isFrontendMethodIDKey(key) || !isASCIIDecimal(rawID) {
		return "", 0, fmt.Errorf("malformed METHOD_IDS entry on line %d: %q", lineNumber, line)
	}

	parsedID, err := strconv.ParseUint(rawID, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid METHOD_IDS value for %q: %w", key, err)
	}
	return key, uint32(parsedID), nil
}

func isFrontendMethodIDKey(key string) bool {
	if key == "" || key[0] < 'A' || key[0] > 'Z' {
		return false
	}
	for index := 1; index < len(key); index++ {
		char := key[index]
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func isASCIIDecimal(value string) bool {
	if value == "" {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func expectedFrontendMethodIDs(methodNames map[string]string) (map[string]uint32, error) {
	expected := make(map[string]uint32, len(methodNames))
	for key, methodName := range methodNames {
		fqn, err := backendAppMethodFQN(methodName)
		if err != nil {
			return nil, fmt.Errorf("METHOD_IDS entry %q: %w", key, err)
		}
		expected[key] = frontendMethodID(fqn)
	}
	return expected, nil
}

func backendAppMethodFQN(methodName string) (string, error) {
	appType := reflect.TypeFor[App]()
	if _, exists := reflect.PointerTo(appType).MethodByName(methodName); !exists {
		return "", fmt.Errorf("backend %s method %q does not exist", appType.Name(), methodName)
	}
	return appType.PkgPath() + "." + appType.Name() + "." + methodName, nil
}

func frontendMethodID(fqn string) uint32 {
	const offsetBasis = uint32(2166136261)
	const prime = uint32(16777619)

	id := offsetBasis
	for index := range fqn {
		id ^= uint32(fqn[index])
		id *= prime
	}
	return id
}

func validateFrontendMethodIDs(ids map[string]uint32) error {
	expected, err := expectedFrontendMethodIDs(frontendMethodNames)
	if err != nil {
		return err
	}
	for key := range ids {
		if _, known := expected[key]; !known {
			return fmt.Errorf("unknown METHOD_IDS entry %q", key)
		}
	}
	for key, wantID := range expected {
		gotID, present := ids[key]
		if !present {
			return fmt.Errorf("missing METHOD_IDS entry %q", key)
		}
		if gotID != wantID {
			fqn, err := backendAppMethodFQN(frontendMethodNames[key])
			if err != nil {
				return err
			}
			return fmt.Errorf(
				"changed METHOD_IDS entry %q: production source has %d, backend FQN %q hashes to %d",
				key,
				gotID,
				fqn,
				wantID,
			)
		}
	}
	return nil
}

// TestFrontendMethodIDsMatchBackendFQN verifies that the production frontend
// METHOD_IDS object matches the FNV-1a hashes that Wails computes from the
// fully-qualified Go method names.
func TestFrontendMethodIDsMatchBackendFQN(t *testing.T) {
	sourcePath := filepath.Join(
		"..", "..", "..", "frontend-app", "src", "shared", "api", "wails", "wailsBridgeConstants.js",
	)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read production frontend METHOD_IDS from %s: %v", sourcePath, err)
	}

	ids, err := parseFrontendMethodIDs(string(source))
	if err != nil {
		t.Fatalf("parse production frontend METHOD_IDS from %s: %v", sourcePath, err)
	}
	if err := validateFrontendMethodIDs(ids); err != nil {
		t.Fatalf("validate production frontend METHOD_IDS from %s: %v", sourcePath, err)
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

func TestCallAPIStripsRequestIDBeforeStrictDispatch(t *testing.T) {
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	called := false
	server.Register(handler.Map{"ui/selectFiles": platformrpc.StrictHandler(func(_ context.Context, req struct {
		DefaultPath string `json:"defaultPath"`
	}) (map[string]string, error) {
		called = true
		return map[string]string{"defaultPath": req.DefaultPath}, nil
	})})
	app := &App{dispatch: server.Dispatch}

	_, err := app.CallAPI("ui/selectFiles", json.RawMessage(`{"defaultPath":"/tmp","_aoRequestId":42}`))
	if err != nil {
		t.Errorf("CallAPI(ui/selectFiles) error = %v, want nil", err)
	}
	if !called {
		t.Error("strict handler body was not called")
	}
}

func TestCallAPIStrictRouteRejectsUnknownFrontendMetaThroughDispatch(t *testing.T) {
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	var captured json.RawMessage
	called := false
	server.Register(handler.Map{"ui/selectFiles": platformrpc.StrictHandler(func(_ context.Context, req struct {
		DefaultPath string `json:"defaultPath"`
	}) (map[string]string, error) {
		called = true
		return map[string]string{"defaultPath": req.DefaultPath}, nil
	})})
	app := &App{dispatch: func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		captured = append(json.RawMessage(nil), params...)
		return server.Dispatch(ctx, method, params)
	}}

	_, err := app.CallAPI("ui/selectFiles", json.RawMessage(`{"defaultPath":"/tmp","_aoClientKind":"desktop-wails","_aoTypo":"leak"}`))
	if err == nil {
		t.Fatal("CallAPI(ui/selectFiles) error = nil, want strict handler unknown-field rejection")
	}
	if called {
		t.Fatal("strict handler body was called despite unknown _ao field")
	}
	if !strings.Contains(err.Error(), "invalid parameters") {
		t.Fatalf("CallAPI(ui/selectFiles) error = %v, want invalid parameters", err)
	}
	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("captured params are invalid JSON: %v", err)
	}
	if got["_aoTypo"] != "leak" {
		t.Fatalf("captured params = %#v, want unknown _aoTypo preserved for strict rejection", got)
	}
	if _, ok := got["_aoClientKind"]; ok {
		t.Fatalf("captured params still contain stripped _aoClientKind: %#v", got)
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

	_, err := app.CallAPI("ui/log", json.RawMessage(`{"entries":[],"_aoClientKind":"desktop-wails","_aoClientRoute":"/chat","_aoRequestId":42,"_aoTraceparent":"00-`+traceID+`-`+spanID+`-01","_aoTraceId":"`+traceID+`","_aoSpanId":"`+spanID+`"}`))
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
	for _, key := range []string{"_aoRequestId", "_aoTraceparent", "_aoTraceId", "_aoSpanId"} {
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
