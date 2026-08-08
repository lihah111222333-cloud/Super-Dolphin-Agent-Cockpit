package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/claudecli"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	uiwails "github.com/lihah111222333-cloud/super-dolphin-agent/internal/ui/wails"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	smokeThreadID        = "thread-failure-smoke"
	smokeTurnID          = "turn-failure-smoke"
	smokeItemID          = "assistant-stream-" + smokeTurnID
	smokePromptHopTurnID = "turn-prompt-history-wails-hop"
	smokePromptHopText   = "prompt history production Wails hop"
	rawProviderSecret    = "Authorization: Bearer t03-raw-provider-secret-do-not-persist /private/provider/config.yaml\nstack: provider failure\n本次执行失败\nProvider 未能完成本次执行。"
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

type promptHistoryParams struct {
	CWD            string `json:"cwd"`
	ActiveThreadID string `json:"activeThreadId,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
	Nonce          string `json:"nonce,omitempty"`
	Limit          int    `json:"limit"`
}

type failureSmokeRuntimeConfig struct{}

// ReadRuntimeConfig 为 failure smoke 宿主返回空运行时配置。
func (failureSmokeRuntimeConfig) ReadRuntimeConfig(context.Context, string) (map[string]any, error) {
	return map[string]any{}, nil
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
	runtime, err := newProductionWailsRuntime(dispatcher, *project)
	if err != nil {
		slog.Error("create production Wails failure smoke runtime", "error", err)
		os.Exit(1)
	}
	defer runtime.stop()

	mux := http.NewServeMux()
	mux.Handle("/wails/ws", platformrpc.WSHandler(runtime.transport, nil))
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

type productionWailsRuntime struct {
	transport        *platformrpc.Server
	bridge           *uiwails.EventBridge
	eventUnsubs      []func()
	projectionCancel context.CancelFunc
	providerDispatch map[string]*unified.EventDispatcher
}

func (r *productionWailsRuntime) stop() {
	for _, unsubscribe := range r.eventUnsubs {
		unsubscribe()
	}
	if r.projectionCancel != nil {
		r.projectionCancel()
	}
	r.bridge.Stop()
}

// newProductionWailsRuntime 复用 production application、binding、lifecycle 与 EventBridge。
// fixture adapter 只负责浏览器 RPC 入站，以及把真实 Wails EventManager 事件转交给测试浏览器。
func newProductionWailsRuntime(dispatcher *event.Dispatcher, project string) (*productionWailsRuntime, error) {
	config := &platformconfig.Config{RPCAddr: "127.0.0.1:0"}
	backend := platformrpc.NewServer(platformrpc.Params{Config: config})
	providerDispatch, err := providerDispatchers(dispatcher)
	if err != nil {
		return nil, fmt.Errorf("create failure smoke provider adapters: %w", err)
	}
	terminalOutputReady := make(chan struct{}, 1)
	projection, _, err := uistate.NewService(
		pkglogger.NewRuntime(pkglogger.RuntimeConfig{}),
		slog.Default(),
		nil,
		nil,
		nil,
		nil,
		failureSmokeRuntimeConfig{},
	)
	if err != nil {
		return nil, fmt.Errorf("create production uistate projection: %w", err)
	}
	projectionCancel := uistate.NewUIStateSubscribers(projection).Spec.Register(dispatcher)
	if projectionCancel == nil {
		return nil, fmt.Errorf("register production uistate projection")
	}
	backendHandlers := fixtureHandlers(dispatcher, project, providerDispatch, terminalOutputReady)
	backend.Register(backendHandlers)

	applicationTransport := platformrpc.NewServer(platformrpc.Params{Config: config})
	browserTransport := platformrpc.NewServer(platformrpc.Params{Config: config})
	pushBridge := platformrpc.NewPushBridge(dispatcher, nil)
	binding := uiwails.NewApp(uiwails.AppParams{
		Dispatcher: backend,
		Config:     config,
		RPCServer:  applicationTransport,
		PushBridge: pushBridge,
	})
	lifecycle := uiwails.NewWailsLifecycle(
		uiwails.ActiveAgentCounterFunc(func(context.Context) (int, error) { return 0, nil }),
		slog.Default(),
	)
	readiness := uiwails.NewActivationReadiness()
	wailsApp, err := uiwails.NewWailsApplication(uiwails.ApplicationParams{
		Logger:    slog.Default(),
		Binding:   binding,
		Service:   uiwails.NewService(binding),
		Lifecycle: lifecycle,
		Readiness: readiness,
		Frontend:  uiwails.FrontendFS{FS: os.DirFS(filepath.Join(project, "frontend-app"))},
	})
	if err != nil {
		return nil, fmt.Errorf("create production Wails application: %w", err)
	}
	browserTransport.Register(bindingHandlers(binding, backendHandlers))
	eventUnsubs := adaptWailsEventsToBrowser(wailsApp, browserTransport, pushBridge, terminalOutputReady)

	bridge := uiwails.NewEventBridge(dispatcher, lifecycle, slog.Default())
	bridge.Start()
	return &productionWailsRuntime{
		transport: browserTransport, bridge: bridge, eventUnsubs: eventUnsubs, projectionCancel: projectionCancel, providerDispatch: providerDispatch,
	}, nil
}

// providerDispatchers keeps each provider's real adapter isolated: registering both
// adapters on one dispatcher would translate every raw event twice.
func providerDispatchers(dispatcher *event.Dispatcher) (map[string]*unified.EventDispatcher, error) {
	hooks, err := providershared.ConfigureRuntimeHooks(providershared.RuntimeHooks{
		Capture: func(_ providershared.ToolResultMeta, raw string) (providershared.ToolResultRecord, error) {
			return providershared.ToolResultRecord{
				Preview:      providershared.SafeToolArgumentsPreviewString(raw),
				OriginalSize: len(raw),
			}, nil
		},
		Reset: func(string, string) error { return nil },
	})
	if err != nil {
		return nil, fmt.Errorf("configure failure smoke provider hooks: %w", err)
	}
	claude := unified.NewEventDispatcher(dispatcher, slog.Default())
	claudecli.RegisterTranslators(claude, hooks)
	codex := unified.NewEventDispatcher(dispatcher, slog.Default())
	if err := codexapp.RegisterTranslators(codex, hooks); err != nil {
		return nil, fmt.Errorf("register failure smoke codex translators: %w", err)
	}
	return map[string]*unified.EventDispatcher{"claude": claude, "codex": codex}, nil
}

// adaptWailsEventsToBrowser 是 fixture adapter：只转发已由 production Wails EventManager 发出的事件。
func adaptWailsEventsToBrowser(
	wailsApp *application.App,
	transport *platformrpc.Server,
	pushBridge *platformrpc.PushBridge,
	terminalOutputReady chan<- struct{},
) []func() {
	eventNames := []string{"bridge-event", "agent-event"}
	unsubs := make([]func(), 0, len(eventNames))
	for _, eventName := range eventNames {
		eventName := eventName
		unsubs = append(unsubs, wailsApp.Event.On(eventName, func(wailsEvent *application.CustomEvent) {
			transport.NotifyAll(context.Background(), pushBridge, eventName, wailsEvent.Data)
			signalTerminalOutputReady(terminalOutputReady, eventName, wailsEvent.Data)
		}))
	}
	return unsubs
}

// signalTerminalOutputReady 在 Claude 部分响应通过 production EventBridge/Wails emitter 后释放终态发布屏障。
func signalTerminalOutputReady(ready chan<- struct{}, eventName string, data any) {
	if eventName != "bridge-event" {
		return
	}
	payload, ok := data.(map[string]any)
	if !ok || payload["type"] != eventsurface.MethodAgentMessageDelta {
		return
	}
	select {
	case ready <- struct{}{}:
	default:
	}
}

func bindingHandlers(binding *uiwails.App, methods handler.Map) handler.Map {
	result := make(handler.Map, len(methods))
	for method := range methods {
		method := method
		result[method] = handler.Func(func(_ context.Context, request *jrpc2.Request) (any, error) {
			return binding.CallAPI(method, json.RawMessage(request.ParamString()))
		})
	}
	return result
}

// fixtureHandlers 组装启动快照和失败终态触发器的严格 RPC 契约。
func fixtureHandlers(dispatcher *event.Dispatcher, project string, providers map[string]*unified.EventDispatcher, terminalOutputReady <-chan struct{}) handler.Map {
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
	handlers["thread/promptHistory"] = newPromptHistoryHandler(dispatcher, project)
	handlers["failure-smoke/trigger"] = platformrpc.StrictHandler(func(_ context.Context, params triggerParams) (any, error) {
		if params.CaseID != "terminal-failed" {
			return nil, fmt.Errorf("unsupported failure smoke case %q", params.CaseID)
		}
		if err := publishTerminalFailureAfterOutput(providers, terminalOutputReady); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "caseId": params.CaseID}, nil
	})
	return handlers
}

func newPromptHistoryHandler(dispatcher *event.Dispatcher, project string) handler.Func {
	var calls atomic.Int32
	return platformrpc.StrictHandler(func(_ context.Context, params promptHistoryParams) (any, error) {
		if params.CWD != project || params.ActiveThreadID != smokeThreadID || params.Limit != 50 {
			return nil, fmt.Errorf("invalid prompt history smoke request")
		}
		if calls.Add(1) == 1 {
			publishPromptHistoryHop(dispatcher)
			return nil, fmt.Errorf("prompt history private token=secret")
		}
		return map[string]any{
			"entries": []any{map[string]any{
				"threadId": smokeThreadID, "messageId": "prompt-history-retry",
				"text": "桌面 smoke 重试恢复", "createdAt": time.Now().UTC().Format(time.RFC3339Nano),
			}},
			"nextCursor": "", "hasMore": false, "nonce": "desktop-failure-smoke-nonce",
		}, nil
	})
}

// fixtureResponses 返回前端启动所需的最小 RPC 快照，避免 smoke 依赖真实用户数据。
func fixtureResponses(project string) map[string]any {
	thread, agent, tokenUsage, sidebar := fixtureSidebarState(project)
	emptyPreferences := map[string]any{"preferences": map[string]any{}}
	return map[string]any{
		"ui/log":       map[string]any{"ok": true},
		"ui/buildInfo": map[string]any{"version": "failure-smoke"},
		"config/read": map[string]any{
			"model": "", "modelProvider": nil, "cwd": project, "approvalPolicy": "", "sandbox": nil,
			"config": nil, "baseInstructions": nil, "developerInstructions": nil, "personality": nil,
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
		"ui/sidebar/get":              sidebar,
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

// fixtureSidebarState 同时构造旧 UI 状态和强类型 sidebar 快照，保证两条启动路径使用同一身份数据。
func fixtureSidebarState(project string) (map[string]any, map[string]any, map[string]any, uistate.Sidebar) {
	assignedAt := time.Date(2026, time.July, 28, 8, 0, 0, 0, time.UTC)
	assignment := &agentdto.Assignment{Title: "Failure smoke agent", Description: "Exercise failure smoke", AssignedAt: assignedAt}
	progress := agentdto.Progress{Status: string(agentdto.StateTurnRunning), UpdatedAt: assignedAt}
	thread := map[string]any{
		"id":              smokeThreadID,
		"name":            "Failure smoke thread",
		"agent_id":        "agent-failure-smoke",
		"lifecycleStatus": "running",
	}
	agent := map[string]any{
		"id": "agent-failure-smoke", "name": "Failure smoke agent", "thread_id": smokeThreadID,
		"parentAgentId": "", "assignment": assignment, "progress": progress, "outcome": nil,
		"state": "running", "provider": "codex", "model": "gpt-5.5", "cwd": project,
	}
	tokenUsage := map[string]any{
		"inputTokens": 0, "outputTokens": 0, "totalTokens": 0, "usedTokens": 0,
		"contextWindowTokens": 128000, "usedPercent": 0,
	}
	sidebar := uistate.Sidebar{
		Threads: []uistate.ThreadSummary{{
			ID: smokeThreadID, Name: "Failure smoke thread", AgentID: "agent-failure-smoke", LifecycleStatus: "running",
		}},
		Agents: []uistate.AgentSummary{{
			ID: "agent-failure-smoke", Name: "Failure smoke agent", ThreadID: smokeThreadID,
			Assignment: assignment, Progress: progress, Outcome: nil,
			State: "running", Provider: "codex", Model: "gpt-5.5", CWD: project,
		}},
		RecentTurns:    []uistate.TurnSummary{},
		Workspace:      uistate.WorkspacePanel{Runs: []uistate.WorkspaceRunSummary{}},
		TokenUsage:     uistate.TokenUsage{ContextWindowTokens: 128000},
		ActiveThreadID: smokeThreadID,
	}
	return thread, agent, tokenUsage, sidebar
}

// publishPromptHistoryHop 为 prompt reject case 发布一个可见 delta，证明 EventBridge/Wails emitter 被实际使用。
func publishPromptHistoryHop(dispatcher *event.Dispatcher) {
	now := time.Now().UTC()
	event.Publish(dispatcher, turndto.TurnOutputDelta{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{
					EventHeader: shareddto.EventHeader{Timestamp: now},
					ThreadID:    smokeThreadID,
				},
				AgentID: "agent-failure-smoke",
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: smokePromptHopTurnID},
		},
		Stream: "message",
		Delta:  smokePromptHopText,
	})
}

// publishTerminalFailure 通过 canonical DTO 发布部分响应和失败终态。
// publishTerminalFailure injects sensitive raw provider failures into both production
// adapters. Only their typed DTO output reaches EventBridge and the browser.
func publishTerminalFailure(providers map[string]*unified.EventDispatcher) error {
	return publishTerminalFailureWithWait(providers, func() error { return nil })
}

// publishTerminalFailureAfterOutput 等待 Claude 部分响应穿过真实事件桥后再发布失败终态。
func publishTerminalFailureAfterOutput(providers map[string]*unified.EventDispatcher, terminalOutputReady <-chan struct{}) error {
	return publishTerminalFailureWithWait(providers, func() error {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-terminalOutputReady:
			return nil
		case <-timer.C:
			return fmt.Errorf("failure smoke Claude output did not cross the Wails event bridge")
		}
	})
}

// publishTerminalFailureWithWait 发布 Claude 部分响应，并在调用方指定的屏障后发布失败终态。
func publishTerminalFailureWithWait(providers map[string]*unified.EventDispatcher, wait func() error) error {
	now := time.Now().UTC()
	claude := providers["claude"]
	codex := providers["codex"]
	if claude == nil || codex == nil {
		return fmt.Errorf("failure smoke provider adapters are required")
	}
	claude.Dispatch(dto.RawProviderEvent{EventType: "assistant:message_delta", Data: map[string]any{
		"timestamp": now.Format(time.RFC3339Nano), "thread_id": smokeThreadID, "agent_id": smokeThreadID,
		"turn_id": smokeTurnID, "stream": "message", "delta": "桌面 smoke 部分响应", "error": rawProviderSecret,
	}})
	if err := wait(); err != nil {
		return err
	}
	codex.Dispatch(dto.RawProviderEvent{EventType: "turn/failed", Data: map[string]any{
		"timestamp": now.Format(time.RFC3339Nano), "thread_id": smokeThreadID, "agent_id": smokeThreadID,
		"turn_id": smokeTurnID, "error": rawProviderSecret, "reason": "provider_failure",
		"accepted_partial_item_ids": []string{smokeItemID},
	}})
	return nil
}
