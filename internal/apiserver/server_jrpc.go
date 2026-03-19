package apiserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
)

// Server is the V3 API server built on jrpc2.
// All RPC methods are registered as typed Go functions via handler.ServiceMap.
type Server struct {
	mu     sync.RWMutex
	logger *slog.Logger

	// jrpc2 infrastructure
	bridge jhttp.Bridge

	// Service dependencies (injected)
	// threadSvc    *ThreadService
	// turnSvc      *TurnService
	// skillSvc     *SkillService
	// workspaceSvc *WorkspaceService
	// configSvc    *ConfigService
	// uiSvc        *UIService

	// SSE notification bridge
	sse *SSEBridge
}

// ServerConfig holds configuration for creating a new Server.
type ServerConfig struct {
	Logger *slog.Logger
}

// NewServer creates a V3 API server with jrpc2 framework.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	s := &Server{
		logger: cfg.Logger,
		sse:    NewSSEBridge(),
	}

	// Build the service map — each service is a plain Go struct,
	// each method is a plain Go function. Zero boilerplate.
	serviceMux := s.buildServiceMap()

	// Wrap with middleware chain
	finalMux := LoggingMiddleware(serviceMux, cfg.Logger)

	// Create jhttp bridge for HTTP transport
	s.bridge = jhttp.NewBridge(finalMux, &jhttp.BridgeOptions{
		Server: &jrpc2.ServerOptions{
			AllowPush: true,
		},
	})

	return s
}

// buildServiceMap registers all RPC methods.
// Each service maps to a namespace: "thread/start" → ServiceMap["thread"]["start"]
//
// V2 comparison: this replaces registerMethods() + 8 registerXxxMethods() functions
// + all bindRaw/bindTyped wrappers. Total savings: ~200 lines.
func (s *Server) buildServiceMap() handler.ServiceMap {
	return handler.ServiceMap{
		// Phase P5d: each line below replaces an entire methods_*.go file
		// "thread":    s.threadHandlers(),
		// "turn":      s.turnHandlers(),
		// "skills":    s.skillHandlers(),
		// "config":    s.configHandlers(),
		// "workspace": s.workspaceHandlers(),
		// "ui":        s.uiHandlers(),
		// "lsp":       s.lspHandlers(),
		// "command":   s.commandHandlers(),
		// "model":     s.modelHandlers(),

		// Placeholder: health check
		"system": handler.Map{
			"ping": handler.New(s.Ping),
		},
	}
}

// Ping is a health check RPC method.
// Demonstrates the jrpc2 handler pattern: just a regular Go function.
func (s *Server) Ping(ctx context.Context) string {
	return "pong"
}

// HTTPHandler returns the HTTP handler for the RPC server.
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/rpc", s.bridge)
	mux.HandleFunc("/events", s.sse.ServeHTTP)
	return mux
}

// Close shuts down the server.
func (s *Server) Close() error {
	return s.bridge.Close()
}

// Notify sends a server-to-client notification via SSE.
func (s *Server) Notify(method string, params any) {
	s.sse.Broadcast(method, params)
}

// ---- Example: how a full service registration looks ----

// ThreadStartReq is the typed request for thread/start.
// V2 equivalent: threadStartParams struct + typedHandler wrapper + map[string]any response
// V3: just this struct + the handler function. jrpc2 handles everything else.
type ThreadStartReq struct {
	Model            string `json:"model,omitempty"`
	ModelProvider    string `json:"modelProvider,omitempty"`
	Cwd              string `json:"cwd,omitempty"`
	ApprovalPolicy   string `json:"approvalPolicy,omitempty"`
	BaseInstructions string `json:"baseInstructions,omitempty"`
}

// ThreadStartResp is the typed response for thread/start.
// V2 equivalent: a manually-constructed map[string]any{} — no type safety.
// V3: typed struct, auto-serialized by jrpc2.
type ThreadStartResp struct {
	Thread struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"thread"`
	Model          string `json:"model"`
	ModelProvider  string `json:"modelProvider"`
	Cwd            string `json:"cwd"`
	ApprovalPolicy string `json:"approvalPolicy"`
}

// NotifyPayload is used for SSE notification serialization.
type NotifyPayload struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// MarshalNotification creates a JSON-RPC notification payload.
func MarshalNotification(method string, params any) ([]byte, error) {
	return json.Marshal(NotifyPayload{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}
