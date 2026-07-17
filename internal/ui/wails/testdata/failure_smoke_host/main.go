package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	smokeThreadID = "thread-failure-smoke"
	smokeTurnID   = "turn-failure-smoke"
	smokeItemID   = "assistant-stream-" + smokeTurnID
)

type triggerParams struct {
	CaseID string `json:"caseId"`
}

type frontendIngestParams struct {
	Events []map[string]any `json:"events"`
}

type preferenceGetParams struct {
	Key string `json:"key"`
	Cwd string `json:"cwd,omitempty"`
}

// main 启动只服务于真实 DOM failure smoke 的 Wails WebSocket 测试宿主。
func main() {
	addr := flag.String("addr", "127.0.0.1:4514", "failure smoke HTTP address")
	project := flag.String("project", ".", "fixture project path")
	flag.Parse()

	dispatcher := event.NewDispatcher()
	defer func() {
		if err := dispatcher.Close(); err != nil {
			slog.Error("close failure smoke dispatcher", "error", err)
		}
	}()
	server := platformrpc.NewServer(platformrpc.Params{
		Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"},
	})
	bridge := platformrpc.NewPushBridge(dispatcher, nil)
	cancels := eventsurface.Bind(dispatcher, nil, func(method string, payload any) {
		server.NotifyAll(context.Background(), bridge, method, payload)
	})
	defer func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
	}()
	server.Register(fixtureHandlers(dispatcher, *project))

	mux := http.NewServeMux()
	mux.Handle("/wails/ws", platformrpc.WSHandler(server, nil))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	httpServer := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	errCh := make(chan error, 1)
	safego.Go(ctx, nil, "wails.failure_smoke_host", func(context.Context) {
		slog.Info("failure smoke host listening", "addr", *addr)
		errCh <- httpServer.ListenAndServe()
	})

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("failure smoke host stopped", "error", err)
			os.Exit(1)
		}
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("failure smoke host shutdown", "error", err)
	}
}

// fixtureHandlers 组装启动快照和失败终态触发器的严格 RPC 契约。
func fixtureHandlers(dispatcher *event.Dispatcher, project string) handler.Map {
	handlers := handler.Map{}
	for method, response := range fixtureResponses(project) {
		current := response
		handlers[method] = handler.Func(func(context.Context, *jrpc2.Request) (any, error) {
			return current, nil
		})
	}
	handlers["observability/frontend/ingest"] = platformrpc.StrictHandler(
		func(_ context.Context, params frontendIngestParams) (any, error) {
			return map[string]any{"enabled": true, "recorded": len(params.Events), "dropped": 0}, nil
		},
	)
	handlers["ui/preferences/get"] = platformrpc.StrictHandler(
		func(_ context.Context, params preferenceGetParams) (any, error) {
			switch params.Key {
			case "settings.provider.active":
				return "codex", nil
			case "settings.provider.codex.model":
				return "gpt-5.5", nil
			case "settings.provider.codex.effort":
				return "xhigh", nil
			case "settings.provider.codex.codexModelProvider":
				return "openai", nil
			default:
				return nil, nil
			}
		},
	)
	handlers["failure-smoke/trigger"] = platformrpc.StrictHandler(func(_ context.Context, params triggerParams) (any, error) {
		if params.CaseID != "terminal-failed" {
			return nil, fmt.Errorf("unsupported failure smoke case %q", params.CaseID)
		}
		if err := publishTerminalFailure(dispatcher); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "caseId": params.CaseID}, nil
	})
	return handlers
}

// fixtureResponses 返回前端启动所需的最小 RPC 快照，避免 smoke 依赖真实用户数据。
func fixtureResponses(project string) map[string]any {
	thread := map[string]any{
		"id":              smokeThreadID,
		"name":            "Failure smoke thread",
		"agent_id":        "agent-failure-smoke",
		"lifecycleStatus": "running",
	}
	agent := map[string]any{
		"id":        "agent-failure-smoke",
		"name":      "Failure smoke agent",
		"thread_id": smokeThreadID,
		"state":     "running",
		"provider":  "codex",
		"model":     "gpt-5.5",
		"cwd":       project,
	}
	tokenUsage := map[string]any{
		"inputTokens": 0, "outputTokens": 0, "totalTokens": 0, "usedTokens": 0,
		"contextWindowTokens": 128000, "usedPercent": 0,
	}
	emptyPreferences := map[string]any{"preferences": map[string]any{}}
	return map[string]any{
		"ui/log":       map[string]any{"ok": true},
		"ui/buildInfo": map[string]any{"version": "failure-smoke"},
		"config/read": map[string]any{
			"model":                 "",
			"modelProvider":         nil,
			"cwd":                   project,
			"approvalPolicy":        "",
			"sandbox":               nil,
			"config":                nil,
			"baseInstructions":      nil,
			"developerInstructions": nil,
			"personality":           nil,
			"toolRouting": map[string]any{
				"mode": "", "routerModel": "", "routerProvider": "", "routerBaseURL": "",
				"routerHasAPIKey": false, "confidenceThreshold": 0.5, "timeoutSec": 30,
			},
		},
		"ui/windowBootstrap/get":      map[string]any{"snapshot": map[string]any{}},
		"ui/preferences/getAll":       emptyPreferences,
		"ui/preferences/set":          map[string]any{"ok": true},
		"ui/projects/get":             map[string]any{"projects": []string{project}, "active": project},
		"ui/projects/add":             map[string]any{"projects": []string{project}, "active": project},
		"ui/projects/setActive":       map[string]any{"projects": []string{project}, "active": project},
		"ui/projects/remove":          map[string]any{"projects": []string{project}, "active": project},
		"ui/sidebar/get":              map[string]any{"activeThreadId": smokeThreadID, "threads": []any{thread}, "tokenUsageByThread": map[string]any{}},
		"ui/state/get":                map[string]any{"activeThreadId": smokeThreadID, "threads": []any{thread}, "agents": []any{agent}, "token_usage": tokenUsage, "timelinesByThread": map[string]any{smokeThreadID: []any{}}, "diffTextByThread": map[string]any{}},
		"thread/messages":             map[string]any{"messages": []any{}},
		"thread/config/get":           map[string]any{"threadId": smokeThreadID, "provider": "codex", "supportsThreadOverride": true, "override": map[string]any{}, "effective": map[string]any{}},
		"app/update/check":            map[string]any{"enabled": false, "available": false},
		"ui/dashboard/get":            map[string]any{},
		"ui/memory/get":               map[string]any{"overview": map[string]any{}, "private": map[string]any{"entries": []any{}}, "team": map[string]any{"entries": []any{}}, "finalOutputRefs": []any{}},
		"dashboard/sharedFiles":       map[string]any{"files": []any{}, "finalOutputRefs": []any{}},
		"observability/status":        map[string]any{"enabled": true, "schema_version": 1, "index_trace_keys": 1, "sink_events_written": 1, "sink_write_errors": 0},
		"observability/recent/list":   map[string]any{"events": []any{}, "slowest_events": []any{}, "errors": []any{}, "truncated": false},
		"prompt-assets/list":          map[string]any{"prompts": []any{}},
		"dashboard/prompts":           map[string]any{"prompts": []any{}},
		"prompt-sections/list":        map[string]any{"sections": []any{}},
		"personalization/profile/get": map[string]any{"profile": map[string]any{}},
		"modelProviders/list":         map[string]any{"providers": []any{}},
		"ui/video/getApiKey":          map[string]any{"configured": false, "masked": ""},
		"config/lspPromptHint/read":   map[string]any{"hint": "", "defaultHint": "", "overrideHint": "", "usingDefault": true},
		"config/builtinTools/read":    map[string]any{"tools": []any{}},
		"dashboard/dags":              map[string]any{"dags": []any{}},
		"dashboard/dagRuns":           map[string]any{"runs": []any{}},
		"workflowTemplates/list":      map[string]any{"templates": []any{}},
		"cronjob/list":                map[string]any{"jobs": []any{}},
		"skills/resolution_list":      map[string]any{"items": []any{}},
		"skills/tools/list":           map[string]any{"tools": []any{}},
		"mcpServer/list":              map[string]any{"mcpServers": map[string]any{}},
		"datasourceV2/list":           map[string]any{"documents": []any{}},
	}
}

// publishTerminalFailure 通过 canonical DTO 发布部分响应和失败终态。
func publishTerminalFailure(dispatcher *event.Dispatcher) error {
	now := time.Now().UTC()
	header := shareddto.TurnHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{
				EventHeader: shareddto.EventHeader{Timestamp: now},
				ThreadID:    smokeThreadID,
			},
			AgentID: "agent-failure-smoke",
		},
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: smokeTurnID},
	}
	event.Publish(dispatcher, turndto.TurnOutputDelta{
		TurnHeader: header,
		Stream:     "message",
		Delta:      "桌面 smoke 部分响应",
	})
	terminal := turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "desktop-failure-smoke-terminal",
		ThreadID:      smokeThreadID,
		TurnID:        smokeTurnID,
		Outcome:       "failed",
		PublicError: &turndto.PublicErrorV1{
			Code:            "PROVIDER_FAILED",
			Title:           "运行失败",
			Message:         "提供方未能完成本轮响应",
			DiagnosticID:    "desktop-failure-smoke",
			Retryable:       true,
			RecoveryActions: []string{"retry"},
		},
		PartialItemIDs: []string{smokeItemID},
		OccurredAt:     now.Format(time.RFC3339Nano),
	}
	projected, err := eventsurface.ProjectRemoteTurnTerminal(terminal, "agent-failure-smoke")
	if err != nil {
		return fmt.Errorf("project failure smoke terminal: %w", err)
	}
	event.Publish(dispatcher, projected)
	return nil
}
