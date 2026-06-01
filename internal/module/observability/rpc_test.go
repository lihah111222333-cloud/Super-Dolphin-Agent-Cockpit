package observability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"
)

type recordingSink struct{ events []platformobs.TraceEvent }

func (s *recordingSink) Append(_ context.Context, event platformobs.TraceEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestNewHandlersReturnsRPCHandlerMapResult(t *testing.T) {
	svc := platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit"})
	result := NewHandlers(svc)
	if _, ok := result.Handlers["observability/status"]; !ok {
		t.Fatalf("observability/status not registered")
	}
	if _, ok := result.Handlers["observability/frontend/ingest"]; !ok {
		t.Fatalf("observability/frontend/ingest not registered")
	}
}

func TestModuleRegistersHandlersThroughRPCGroup(t *testing.T) {
	var maps []handler.Map
	app := fxtest.New(t,
		Module,
		fx.Supply(platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit"})),
		fx.Populate(fx.Annotate(&maps, fx.ParamTags(`group:"rpc_handlers"`))),
	)
	app.RequireStart().RequireStop()
	if len(maps) != 1 {
		t.Fatalf("handler maps = %d, want 1", len(maps))
	}
	if _, ok := maps[0]["observability/status"]; !ok {
		t.Fatalf("observability/status missing from rpc_handlers group")
	}
}

func TestStatusRPCReportsDisabledService(t *testing.T) {
	server := newTestRPCServer(t, platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit disabled"}))
	raw, err := server.Dispatch(t.Context(), "observability/status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch status: %v", err)
	}
	var got platformobs.ServiceStatus
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if got.Enabled || got.DisabledReason != "unit disabled" {
		t.Fatalf("status = %+v", got)
	}
}

func TestFrontendIngestSanitizesAllowlistedFields(t *testing.T) {
	sink := &recordingSink{}
	server := newTestRPCServer(t, newRecordingService(sink))
	resp := dispatchIngest(t, server, json.RawMessage(`{"events":[{"kind":"ui/log","trace_id":"trace token=secret","method":"Authorization: Bearer abc.def","status":"error","metadata":{"token":"secret","safe":"ok"}}]}`))
	if !resp.Enabled || resp.Recorded != 1 || resp.Dropped != 0 {
		t.Fatalf("response = %+v", resp)
	}
	assertSanitizedFrontendEvent(t, sink)
}

func assertSanitizedFrontendEvent(t *testing.T, sink *recordingSink) {
	t.Helper()
	if len(sink.events) != 1 {
		t.Fatalf("sink events = %d, want 1", len(sink.events))
	}
	event := sink.events[0]
	if event.Kind != "frontend" {
		t.Fatalf("Kind = %q, want frontend", event.Kind)
	}
	if strings.Contains(event.TraceID, "secret") || strings.Contains(event.Method, "abc.def") {
		t.Fatalf("event was not sanitized: %+v", event)
	}
	if event.Metadata["token"] != "[REDACTED]" {
		t.Fatalf("metadata token = %#v", event.Metadata["token"])
	}
}

func TestFrontendIngestRejectsUnknownFields(t *testing.T) {
	server := newTestRPCServer(t, platformobs.NewService(platformobs.Config{MetadataMaxBytes: 4096, StringMaxBytes: 64}))
	_, err := server.Dispatch(t.Context(), "observability/frontend/ingest", json.RawMessage(`{"events":[{"message":"raw ui log"}]}`))
	if err == nil {
		t.Fatalf("ingest accepted unknown raw ui/log field")
	}
}

func TestFrontendIngestTrimsOversizedBatch(t *testing.T) {
	sink := &recordingSink{}
	svc := platformobs.NewService(platformobs.Config{MetadataMaxBytes: 4096, StringMaxBytes: 64}, platformobs.WithSink(sink), platformobs.WithSampler(platformobs.NewSampler(platformobs.SamplerConfig{HighFrequencyKeepEvery: 1})))
	server := newTestRPCServer(t, svc)
	events := make([]map[string]any, maxFrontendIngestEvents+3)
	for i := range events {
		events[i] = map[string]any{"trace_id": "trace", "status": "ok"}
	}
	payload, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	raw, err := server.Dispatch(t.Context(), "observability/frontend/ingest", payload)
	if err != nil {
		t.Fatalf("Dispatch ingest: %v", err)
	}
	var resp frontendIngestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal ingest: %v", err)
	}
	if resp.Recorded != maxFrontendIngestEvents || resp.Dropped != 3 {
		t.Fatalf("response = %+v", resp)
	}
	if len(sink.events) != maxFrontendIngestEvents {
		t.Fatalf("recorded events = %d", len(sink.events))
	}
}

func TestFrontendIngestDisabledServiceDropsWithoutRecording(t *testing.T) {
	server := newTestRPCServer(t, platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit disabled"}))
	raw, err := server.Dispatch(t.Context(), "observability/frontend/ingest", json.RawMessage(`{"events":[{"trace_id":"trace"},{"trace_id":"trace2"}]}`))
	if err != nil {
		t.Fatalf("Dispatch ingest disabled: %v", err)
	}
	var resp frontendIngestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal ingest: %v", err)
	}
	if resp.Enabled || resp.Recorded != 0 || resp.Dropped != 2 || resp.DisabledReason != "unit disabled" {
		t.Fatalf("response = %+v", resp)
	}
}

func newRecordingService(sink *recordingSink) *platformobs.Service {
	return platformobs.NewService(platformobs.Config{MetadataMaxBytes: 4096, StringMaxBytes: 64}, platformobs.WithSink(sink), platformobs.WithSampler(platformobs.NewSampler(platformobs.SamplerConfig{HighFrequencyKeepEvery: 1})))
}

func dispatchIngest(t *testing.T, server *platformrpc.Server, payload json.RawMessage) frontendIngestResponse {
	t.Helper()
	raw, err := server.Dispatch(t.Context(), "observability/frontend/ingest", payload)
	if err != nil {
		t.Fatalf("Dispatch ingest: %v", err)
	}
	var resp frontendIngestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal ingest: %v", err)
	}
	return resp
}

func newTestRPCServer(t *testing.T, svc *platformobs.Service) *platformrpc.Server {
	t.Helper()
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}
