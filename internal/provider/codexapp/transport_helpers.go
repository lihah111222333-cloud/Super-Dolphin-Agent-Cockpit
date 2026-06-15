package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/gorilla/websocket"
)

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type codexRolloutFrame struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type initializeRPCResult struct {
	CodexHome string `json:"codexHome"`
}

type RawMessage struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// ThreadID 处理线程ID。
func (m RawMessage) ThreadID() string {
	if len(m.Params) == 0 {
		return ""
	}
	return payloadThreadID(decodeEventPayload(m.Params))
}

var _ Responder = (*transport)(nil)

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type pendingCall struct {
	result json.RawMessage
	err    error
	done   chan struct{}
	once   sync.Once
}

func (p *pendingCall) resolve(result json.RawMessage, err error) {
	p.once.Do(func() {
		p.result = result
		p.err = err
		close(p.done)
	})
}

func normalizeServerURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
		return raw
	}
	return "ws://" + raw
}

func localSpawnListenURL() string { return "ws://127.0.0.1:0" }

func reserveServerURL() (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	addr := listener.Addr().(*net.TCPAddr)
	release := func() {
		_ = listener.Close()
	}
	return fmt.Sprintf("ws://127.0.0.1:%d", addr.Port), release, nil
}

func jsonRPCIDKey(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var num int64
	if err := json.Unmarshal(raw, &num); err == nil {
		return strconv.FormatInt(num, 10)
	}
	return strings.TrimSpace(string(raw))
}

func (t *transport) connectOnce(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		// Bypass local proxy for 127.0.0.1 connections. Without this,
		// HTTP_PROXY / HTTPS_PROXY env vars cause localhost WS dials
		// to route through the proxy and timeout or fail.
		Proxy: nil,
	}
	conn, _, err := dialer.DialContext(ctx, t.serverURL, nil)
	if err != nil {
		return err
	}
	t.setWS(conn)
	return nil
}

func (t *transport) initialize(ctx context.Context) error {
	ctx = shared.NonNilContext(ctx)
	ws, err := t.initializeSocket(ctx)
	if err != nil {
		return err
	}
	id, key, pc := t.registerInitializeCall()
	defer t.pending.Delete(key)
	if err := t.sendInitializeRequest(id); err != nil {
		return err
	}
	return t.awaitInitialize(ctx, ws, pc)
}

func (t *transport) initializeSocket(ctx context.Context) (*websocket.Conn, error) {
	if err := shared.CheckCtx(ctx); err != nil {
		return nil, err
	}
	return t.currentWSOrErr()
}

func (t *transport) registerInitializeCall() (int64, string, *pendingCall) {
	id := t.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	pc := &pendingCall{done: make(chan struct{})}
	t.pending.Store(key, pc)
	return id, key, pc
}

func (t *transport) sendInitializeRequest(id int64) error {
	return t.writeJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "initialize",
		Params:  initializeParams(),
	})
}

// awaitInitialize 等待 Codex app 初始化完成。
func (t *transport) awaitInitialize(ctx context.Context, ws *websocket.Conn, pc *pendingCall) error {
	defer func() { _ = ws.SetReadDeadline(time.Time{}) }()
	for {
		if done, err := initializeDone(pc); done {
			return err
		}
		if err := shared.CheckCtx(ctx); err != nil {
			return err
		}
		if err := t.readInitializeMessage(ctx, ws); err != nil {
			if ctxErr := shared.CheckCtx(ctx); ctxErr != nil {
				return ctxErr
			}
			return err
		}
	}
}

func initializeDone(pc *pendingCall) (bool, error) {
	select {
	case <-pc.done:
		return true, pc.err
	default:
		return false, nil
	}
}

func (t *transport) readInitializeMessage(ctx context.Context, ws *websocket.Conn) error {
	_ = ws.SetReadDeadline(initializeReadDeadline(ctx))
	_, data, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	t.captureInitializeCodexHome(data)
	// P15 debug: log the initialize response to verify experimentalApi accepted
	if len(data) < 2000 {
		pkglogger.Info("codexapp: initialize response", "data", string(data))
	} else {
		pkglogger.Info("codexapp: initialize response", "data_len", len(data), "preview", string(data[:500]))
	}
	t.dispatchReadMessage(ctx, data, nil)
	return nil
}

// captureInitializeCodexHome 处理captureinitializecodexhome。
func (t *transport) captureInitializeCodexHome(data []byte) {
	if t == nil || len(data) == 0 {
		return
	}
	var msg jsonRPCMessage
	if err := json.Unmarshal(data, &msg); err != nil || len(msg.Result) == 0 {
		return
	}
	var result initializeRPCResult
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return
	}
	home := strings.TrimSpace(result.CodexHome)
	if home == "" {
		return
	}
	t.codexHome.Store(home)
	pkglogger.Warn("codexapp: initialize codexHome captured",
		"server_url", t.serverURL,
		"codex_home", home)
}

func initializeReadDeadline(ctx context.Context) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return time.Now().Add(transportReadyTimeout)
}

func (t *transport) writeJSON(v any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	ws, err := t.currentWSOrErr()
	if err != nil {
		return err
	}
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return ws.WriteJSON(v)
}

func (t *transport) notifyDirect(method string, params any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	ws := t.currentWS()
	if ws == nil {
		return errors.New("codexapp: websocket not connected")
	}
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return ws.WriteJSON(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: sanitizeProviderPayload(method, params)})
}

// endReadLoop 处理endreadloop。
func (t *transport) endReadLoop(ctx context.Context, handler any, ws *websocket.Conn, err error, message string) bool {
	superseded := t.readSocketSuperseded(ws)
	closed := t.closed.Load()
	closing := t.closing.Load()
	ctxErr := shared.CheckCtx(ctx)
	expected := readLoopExpectedExit(closed, closing, superseded, ctxErr)
	t.logReadLoopEnd(expected, closed, closing, superseded, ctxErr, err, message)
	if err != nil && (!superseded || closed || closing) {
		t.failPending(err)
	}
	if handler != nil && !expected {
		invokeReadHandler(ctx, t, RawMessage{Method: "connection.dead", Params: mustJSON(map[string]any{"error": message})}, handler)
	}
	return false
}

func readLoopExpectedExit(closed, closing, superseded bool, ctxErr error) bool {
	return closed || closing || superseded || ctxErr != nil
}

func (t *transport) logReadLoopEnd(expected, closed, closing, superseded bool, ctxErr error, err error, message string) {
	if expected {
		pkglogger.Info("codexapp: transport read loop ending",
			"server_url", t.serverURL, "local", t.local, "closed", closed,
			"closing", closing, "superseded", superseded, "ctx_err", ctxErr,
			"error", err, "message", message)
		return
	}
	pkglogger.Warn("codexapp: transport read loop ending",
		"server_url", t.serverURL, "local", t.local, "closed", closed,
		"closing", closing, "superseded", superseded, "error", err, "message", message)
}

func (t *transport) dispatchReadMessage(ctx context.Context, data []byte, handler any) bool {
	var rpcMsg jsonRPCMessage
	if err := json.Unmarshal(data, &rpcMsg); err != nil {
		return true
	}
	if t.handleResponse(rpcMsg) {
		return true
	}
	t.failPendingConnectionDead(rpcMsg)
	msg := RawMessage{ID: rpcMsg.ID, Method: rpcMsg.Method, Params: rpcMsg.Params}
	if strings.TrimSpace(msg.Method) != "" {
		invokeReadHandler(ctx, t, msg, handler)
		return true
	}
	if msg, ok := decodeCodexRolloutFrame(data); ok {
		invokeReadHandler(ctx, t, msg, handler)
	}
	return true
}

func (t *transport) failPendingConnectionDead(msg jsonRPCMessage) {
	if strings.TrimSpace(msg.Method) != "connection.dead" {
		return
	}
	reason := shared.FirstNonEmpty(stringValue(decodeEventPayload(msg.Params), "error", "message"), "connection lost")
	t.failPending(fmt.Errorf("codexapp: connection dead: %s", reason))
}

func decodeCodexRolloutFrame(data []byte) (RawMessage, bool) {
	var frame codexRolloutFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return RawMessage{}, false
	}
	method := strings.TrimSpace(frame.Type)
	if method == "" || len(frame.Payload) == 0 {
		return RawMessage{}, false
	}
	if strings.TrimSpace(string(frame.Payload)) == "" {
		return RawMessage{}, false
	}
	return RawMessage{Method: method, Params: frame.Payload}, true
}

func invokeReadHandler(ctx context.Context, resp Responder, msg RawMessage, handler any) {
	switch h := handler.(type) {
	case func(context.Context, Responder, RawMessage):
		h(ctx, resp, msg)
	case func(string, json.RawMessage):
		if strings.TrimSpace(msg.Method) != "" {
			h(msg.Method, msg.Params)
		}
	}
}

// RespondWithID 处理带ID的respond。
func (t *transport) RespondWithID(id json.RawMessage, result any, callErr error) error {
	if len(id) == 0 {
		return errors.New("codexapp: response id is required")
	}
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	if callErr != nil {
		code, message := -32000, strings.ToLower(strings.TrimSpace(callErr.Error()))
		if strings.Contains(message, "method not supported") || strings.Contains(message, "method not found") {
			code = -32601
		}
		delete(payload, "result")
		payload["error"] = jsonRPCError{Code: code, Message: callErr.Error()}
	}
	return t.writeJSON(payload)
}

func (t *transport) handleResponse(msg jsonRPCMessage) bool {
	if strings.TrimSpace(msg.Method) != "" || len(msg.ID) == 0 {
		return false
	}
	key := jsonRPCIDKey(msg.ID)
	value, ok := t.pending.Load(key)
	if !ok {
		return true
	}
	pc := value.(*pendingCall)
	if msg.Error != nil {
		pc.resolve(nil, fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message))
		return true
	}
	pc.resolve(msg.Result, nil)
	return true
}

func (t *transport) failPending(err error) {
	if err == nil {
		err = errors.New("codexapp: transport unavailable")
	}
	t.pending.Range(func(_, value any) bool {
		value.(*pendingCall).resolve(nil, err)
		return true
	})
}

func (t *transport) currentWS() *websocket.Conn {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.ws
}

func (t *transport) readSocketSuperseded(ws *websocket.Conn) bool {
	if ws == nil {
		return false
	}
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.ws != ws
}

func (t *transport) setWS(ws *websocket.Conn) {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.ws != nil && t.ws != ws {
		_ = t.ws.Close()
	}
	t.ws = ws
}

func (t *transport) closeSocket() {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.ws != nil {
		_ = t.ws.Close()
	}
	t.ws = nil
}

// codexReleaseAPIRequestURL 处理codexreleaseAPI请求URL。
func codexReleaseAPIRequestURL() (string, error) {
	rawURL := strings.TrimSpace(os.Getenv(codexReleaseAPIURLEnv))
	if rawURL == "" {
		rawURL = codexReleaseAPIURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Codex release API URL %q", rawURL)
	}
	if isOfficialCodexReleaseAPIURL(parsed) {
		return rawURL, nil
	}
	if !trustedCodexReleaseMirror() {
		return "", fmt.Errorf("untrusted Codex release API URL %q; use %s only for explicitly trusted mirrors", rawURL, codexTrustedReleaseMirrorEnv)
	}
	if err := validateTrustedCodexMirrorURL(parsed, "Codex release API URL"); err != nil {
		return "", err
	}
	return rawURL, nil
}

func isOfficialCodexReleaseAPIURL(parsed *url.URL) bool {
	return parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Hostname(), "api.github.com") &&
		strings.HasPrefix(parsed.EscapedPath(), "/repos/openai/codex/releases/")
}

func trustedCodexReleaseMirror() bool {
	return strings.TrimSpace(os.Getenv(codexTrustedReleaseMirrorEnv)) == "1"
}

func validateTrustedCodexMirrorURL(parsed *url.URL, label string) error {
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("%s %q must use HTTPS unless it is an explicit loopback test mirror", label, parsed.String())
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	return host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1"
}

type codexProviderHomeSelection struct {
	homeRequest            string
	mirrorHomeRequest      string
	useAppManagedHome      bool
	explicitAppManagedHome bool
}

func ensureResolvedCodexProviderHome(selection codexProviderHomeSelection) (home, mirrorHome string, err error) {
	if selection.useAppManagedHome {
		home, err = providershared.EnsureAppManagedProviderHome(providershared.ProviderCodex)
		if err != nil {
			return "", "", err
		}
		return home, home, nil
	}
	home, err = providershared.EnsureProviderHome(providershared.ProviderCodex, selection.homeRequest)
	if err != nil {
		return "", "", err
	}
	return home, normalizedExplicitProviderHome(selection.mirrorHomeRequest, home), nil
}

// validateAppManagedRelayLaunchEnv 校验appmanagedrelay启动env。
func validateAppManagedRelayLaunchEnv() error {
	if strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY")) != "" {
		return errors.New("app-managed Codex relay config: SUPER_DOLPHIN_CODEX_RELAY_API_KEY is privileged and must not be inherited by app-managed launches")
	}
	baseURL := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL"))
	bootstrapToken := strings.TrimSpace(os.Getenv(codexRelayBootstrapTokenEnv))
	if baseURL == "" && bootstrapToken == "" {
		return nil
	}
	var problems []error
	if baseURL == "" {
		problems = append(problems, errors.New("app-managed Codex relay config: SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is required when SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is set"))
	}
	if bootstrapToken == "" {
		problems = append(problems, errors.New("app-managed Codex relay config: SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is required when SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is set"))
	}
	return errors.Join(problems...)
}

// selectCodexProviderHome 选择codexproviderhome。
func selectCodexProviderHome(rawHome string) (codexProviderHomeSelection, error) {
	packaged, err := providershared.PackagedRuntimeFromEnv()
	if err != nil {
		return codexProviderHomeSelection{}, err
	}
	if strings.TrimSpace(rawHome) == "" {
		return codexProviderHomeSelection{useAppManagedHome: packaged, explicitAppManagedHome: packaged}, nil
	}
	requested, err := comparableCodexHomePath(rawHome)
	if err != nil {
		return codexProviderHomeSelection{}, err
	}
	useAppManaged, err := requestedCodexHomeIsAppManaged(packaged, requested)
	if err != nil {
		return codexProviderHomeSelection{}, err
	}
	if useAppManaged {
		return codexProviderHomeSelection{useAppManagedHome: true, explicitAppManagedHome: true}, nil
	}
	if matchesDefaultCodexCLIHome(requested) {
		return codexProviderHomeSelection{}, nil
	}
	return codexProviderHomeSelection{homeRequest: rawHome, mirrorHomeRequest: rawHome}, nil
}

func requestedCodexHomeIsAppManaged(packaged bool, requested string) (bool, error) {
	if !packaged {
		return false, nil
	}
	if matchesAppManagedCodexHome(requested) {
		return true, nil
	}
	legacy, err := legacyAppManagedCodexHome()
	if err != nil {
		return false, err
	}
	return filepath.Clean(requested) == filepath.Clean(legacy), nil
}
