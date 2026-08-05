package acpnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

type initializeResponse struct {
	ProtocolVersion   int                        `json:"protocolVersion"`
	AgentCapabilities map[string]json.RawMessage `json:"agentCapabilities"`
}

type promptResponse struct {
	StopReason string `json:"stopReason"`
}

// newSessionReservation owns exactly one local admission slot until it is
// released or committed. The once guard makes every exit path idempotent.
type newSessionReservation struct {
	client *Client
	once   sync.Once
}

// Initialize 执行协议版本握手，并只接受官方字段名 agentCapabilities。
func (c *Client) Initialize(ctx context.Context, clientInfo any) error {
	if err := c.beginInitialization(); err != nil {
		return err
	}
	params, err := initializeParamsBounded(clientInfo, c.cfg.MaxMessage)
	if err != nil {
		c.resetInitialization()
		return err
	}
	raw, err := c.requestRaw(ctx, "initialize", params, c.cfg.StartupTimeout)
	if err != nil {
		c.resetInitialization()
		return err
	}
	result, err := decodeInitializeResponse(raw)
	if err != nil {
		c.fail(err)
		return err
	}
	return c.commitInitialize(result)
}

func (c *Client) beginInitialization() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.terminated {
		return ErrClientClosed
	}
	if c.initialized || c.initializing {
		return fmt.Errorf("acp: already initialized")
	}
	c.initializing = true
	return nil
}

func (c *Client) resetInitialization() {
	c.mu.Lock()
	c.initializing = false
	c.mu.Unlock()
}

func initializeParamsBounded(clientInfo any, max int) (json.RawMessage, error) {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientInfo":      clientInfo,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	}
	raw, err := mustJSONBounded(params, max)
	if err != nil {
		return nil, fmt.Errorf("acp: marshal initialize params: %w", err)
	}
	return raw, nil
}

// decodeInitializeResponse 严格解码官方初始化响应并拒绝兼容别名。
func decodeInitializeResponse(raw json.RawMessage) (initializeResponse, error) {
	if err := requireObjectValue(raw, "initialize response"); err != nil {
		return initializeResponse{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return initializeResponse{}, fmt.Errorf("acp: decode initialize response fields: %w", err)
	}
	capabilitiesRaw, ok := fields["agentCapabilities"]
	if !ok {
		return initializeResponse{}, fmt.Errorf("acp: initialize response missing agentCapabilities")
	}
	if err := requireObjectValue(capabilitiesRaw, "agentCapabilities"); err != nil {
		return initializeResponse{}, err
	}
	var result initializeResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return initializeResponse{}, fmt.Errorf("acp: decode initialize response: %w", err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		return initializeResponse{}, fmt.Errorf("acp: unsupported protocol version %d", result.ProtocolVersion)
	}
	return result, nil
}

func (c *Client) commitInitialize(result initializeResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.terminated {
		return ErrClientClosed
	}
	c.initialized = true
	c.initializing = false
	c.caps = CapabilitySnapshot{ProtocolVersion: result.ProtocolVersion, Capabilities: cloneRawMap(result.AgentCapabilities)}
	return nil
}

// Capabilities 返回当前初始化能力的深拷贝，避免调用方修改协议状态。
func (c *Client) Capabilities() CapabilitySnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CapabilitySnapshot{ProtocolVersion: c.caps.ProtocolVersion, Capabilities: cloneRawMap(c.caps.Capabilities)}
}

// NewSession 创建并登记一个尚未进入活动轮次的本地会话。
func (c *Client) NewSession(ctx context.Context, p any) (string, error) {
	if err := c.requireInitialized(); err != nil {
		return "", err
	}
	paramsRaw, err := mustJSONBounded(p, c.cfg.MaxMessage)
	if err != nil {
		return "", fmt.Errorf("acp: marshal session/new params: %w", err)
	}
	if _, err := methodParamsObject(paramsRaw, "session/new"); err != nil {
		return "", err
	}
	reservation, err := c.reserveNewSession()
	if err != nil {
		return "", err
	}
	defer reservation.release()
	raw, err := c.requestRaw(ctx, "session/new", paramsRaw, c.cfg.RequestTimeout)
	if err != nil {
		return "", err
	}
	if err := requireObjectValue(raw, "session/new response"); err != nil {
		c.fail(err)
		return "", err
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		err = fmt.Errorf("acp: decode new session response: %w", err)
		c.fail(err)
		return "", err
	}
	if result.SessionID == "" {
		err := fmt.Errorf("acp: missing sessionId")
		c.fail(err)
		return "", err
	}
	if err := reservation.commit(result.SessionID); err != nil {
		return "", errors.Join(err, c.compensatingCloseSession(result.SessionID))
	}
	return result.SessionID, nil
}

// compensatingCloseSession bounds cleanup when the remote session exists but
// the local session commit was rejected.
func (c *Client) compensatingCloseSession(id string) error {
	params, err := mustJSONBounded(map[string]any{"sessionId": id}, c.cfg.MaxMessage)
	if err != nil {
		return fmt.Errorf("acp: marshal compensating session close: %w", err)
	}
	ctx, cancel := boundedContext(context.Background(), c.cfg.ShutdownTimeout)
	defer cancel()
	responseRaw, err := c.requestRaw(ctx, "session/close", params, c.cfg.ShutdownTimeout)
	if err != nil {
		return fmt.Errorf("acp: compensating session close: %w", err)
	}
	if err := requireObjectValue(responseRaw, "compensating session/close response"); err != nil {
		return err
	}
	return nil
}

// LoadSession 在能力已声明时加载一个未关闭的远端会话。
func (c *Client) LoadSession(ctx context.Context, p any) (string, error) {
	return c.loadOrResumeSession(ctx, "session/load", p, true)
}

// ResumeSession 恢复一个未关闭的远端会话并保留本地代际信息。
func (c *Client) ResumeSession(ctx context.Context, p any) (string, error) {
	return c.loadOrResumeSession(ctx, "session/resume", p, false)
}

func (c *Client) loadOrResumeSession(ctx context.Context, method string, p any, loaded bool) (string, error) {
	if err := c.requireInitialized(); err != nil {
		return "", err
	}
	raw, id, err := prepareExistingSession(method, p, loaded, c)
	if err != nil {
		return "", err
	}
	if err := c.reserveExistingSession(id, loaded); err != nil {
		return "", err
	}
	return c.completeExistingSession(ctx, method, raw, id, loaded)
}

// prepareExistingSession 校验加载或恢复参数，并在发送前检查能力与会话身份。
func prepareExistingSession(method string, p any, loaded bool, c *Client) (json.RawMessage, string, error) {
	raw, err := mustJSONBounded(p, c.cfg.MaxMessage)
	if err != nil {
		return nil, "", fmt.Errorf("acp: marshal %s params: %w", method, err)
	}
	params, err := methodParamsObject(raw, method)
	if err != nil {
		return nil, "", err
	}
	id, err := requiredString(params, "sessionId")
	if err != nil {
		return nil, "", err
	}
	if loaded && !c.capabilityEnabled("loadSession") {
		return nil, "", fmt.Errorf("acp: loadSession capability is not advertised")
	}
	if !loaded {
		if err := c.requireResumeCapability(); err != nil {
			return nil, "", err
		}
	}
	return raw, id, nil
}

func (c *Client) completeExistingSession(ctx context.Context, method string, raw json.RawMessage, id string, loaded bool) (string, error) {
	responseRaw, err := c.requestRaw(ctx, method, raw, c.cfg.RequestTimeout)
	if err != nil {
		c.removeSetupSession(id)
		return "", err
	}
	if err := requireObjectValue(responseRaw, method+" response"); err != nil {
		c.removeSetupSession(id)
		c.fail(err)
		return "", err
	}
	c.mu.Lock()
	if session, ok := c.sessions[id]; ok {
		session.loaded = loaded
		session.setupPending = false
	}
	c.mu.Unlock()
	return id, nil
}

// Prompt 串行执行一个会话轮次，并把取消结果映射为稳定终态。
func (c *Client) Prompt(ctx context.Context, sessionID string, p any) (json.RawMessage, error) {
	if err := c.requireInitialized(); err != nil {
		return nil, err
	}
	turn, err := c.beginTurn(sessionID)
	if err != nil {
		return nil, err
	}
	rawParams, err := buildPromptParamsBounded(c.cfg.MaxMessage, sessionID, p)
	if err != nil {
		c.finishTurn(sessionID, turn, "error")
		return nil, err
	}
	raw, err := c.requestRaw(ctx, "session/prompt", rawParams, c.cfg.RequestTimeout)
	if err != nil {
		c.finishTurn(sessionID, turn, promptErrorReason(err))
		return nil, err
	}
	result, err := decodePromptResponse(raw)
	if err != nil {
		c.finishTurn(sessionID, turn, "error")
		c.fail(err)
		return nil, err
	}
	cancelled, terminalReason := promptTerminal(turn, result.StopReason)
	c.finishTurn(sessionID, turn, terminalReason)
	if !cancelled {
		return raw, nil
	}
	return cancelledPromptResponse(raw, c.cfg.MaxMessage)
}

func buildPromptParamsBounded(max int, sessionID string, p any) (json.RawMessage, error) {
	rawParams, err := mustJSONBounded(map[string]any{"sessionId": sessionID, "prompt": p}, max)
	if err != nil {
		return nil, fmt.Errorf("acp: marshal prompt params: %w", err)
	}
	promptParams, err := methodParamsObject(rawParams, "session/prompt")
	if err != nil {
		return nil, err
	}
	promptRaw, ok := promptParams["prompt"]
	if !ok {
		return nil, fmt.Errorf("acp: missing prompt")
	}
	if err := requireArrayValue(promptRaw, "prompt"); err != nil {
		return nil, err
	}
	return rawParams, nil
}

func promptErrorReason(err error) string {
	if errors.Is(err, ErrRequestTimeout) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return "error"
}

func decodePromptResponse(raw json.RawMessage) (promptResponse, error) {
	if err := requireObjectValue(raw, "session/prompt response"); err != nil {
		return promptResponse{}, err
	}
	var result promptResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return promptResponse{}, fmt.Errorf("acp: decode prompt response: %w", err)
	}
	if !validStopReason(result.StopReason) {
		return promptResponse{}, fmt.Errorf("acp: invalid stopReason %q", result.StopReason)
	}
	return result, nil
}

func promptTerminal(turn *turnState, reason string) (bool, string) {
	turn.mu.Lock()
	cancelled := turn.cancelRequested
	turn.mu.Unlock()
	if cancelled {
		return true, "cancelled"
	}
	return false, reason
}

func cancelledPromptResponse(raw json.RawMessage, max int) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("acp: decode prompt response for cancellation: %w", err)
	}
	fields["stopReason"] = json.RawMessage(`"cancelled"`)
	canonical, err := mustJSONBounded(fields, max)
	if err != nil {
		return nil, fmt.Errorf("acp: marshal cancelled prompt response: %w", err)
	}
	return canonical, nil
}

// Cancel 先成功发送取消通知，再提交本地 cancelRequested 状态。
func (c *Client) Cancel(ctx context.Context, sessionID string) error {
	if ctx == nil {
		return fmt.Errorf("acp: nil cancel context")
	}
	if err := c.requireInitialized(); err != nil {
		return err
	}
	turn, err := c.activeTurn(sessionID)
	if err != nil {
		return err
	}
	turn.mu.Lock()
	if turn.terminal {
		turn.mu.Unlock()
		return nil
	}
	turn.mu.Unlock()
	params, err := mustJSONBounded(map[string]any{"sessionId": sessionID}, c.cfg.MaxMessage)
	if err != nil {
		return fmt.Errorf("acp: marshal cancel params: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 取消调用方的上下文不应把半条通知留在协议流中；写入仍由关闭时限约束。
	writeCtx, cancelWrite := boundedContext(context.Background(), c.cfg.ShutdownTimeout)
	defer cancelWrite()
	if err := c.sendContext(writeCtx, Message{JSONRPC: "2.0", Method: "session/cancel", Params: params}, c.cfg.ShutdownTimeout); err != nil {
		c.fail(err)
		return err
	}
	turn.mu.Lock()
	if !turn.terminal {
		turn.cancelRequested = true
	}
	turn.mu.Unlock()
	return nil
}

func (c *Client) activeTurn(sessionID string) (*turnState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("acp: unknown session %q", sessionID)
	}
	if session.generation != c.generation {
		return nil, fmt.Errorf("acp: stale session %q", sessionID)
	}
	if session.active == nil {
		return nil, fmt.Errorf("acp: no active turn for session %q", sessionID)
	}
	return session.active, nil
}

// Updates 返回有界会话更新队列，关闭后不会再接收新通知。
func (c *Client) Updates() <-chan Update { return c.updates }

// CloseSession 串行化关闭请求，并在成功后永久阻止该会话再次加载。
func (c *Client) CloseSession(ctx context.Context, id string) error {
	if err := c.requireInitialized(); err != nil {
		return err
	}
	if err := c.markSessionClosing(id); err != nil {
		return err
	}
	params, err := mustJSONBounded(map[string]any{"sessionId": id}, c.cfg.MaxMessage)
	if err != nil {
		c.rollbackSessionClosing(id)
		return fmt.Errorf("acp: marshal close session params: %w", err)
	}
	responseRaw, err := c.requestRaw(ctx, "session/close", params, c.cfg.RequestTimeout)
	if err != nil {
		c.rollbackSessionClosing(id)
		return err
	}
	if err := requireObjectValue(responseRaw, "session/close response"); err != nil {
		c.fail(err)
		return err
	}
	c.finishSessionClose(id)
	return nil
}

// validateClosableSession 检查 setup、closing 和活动轮次的互斥状态。
func (c *Client) validateClosableSession(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[id]
	if !ok || session.generation != c.generation {
		return fmt.Errorf("acp: unknown session %q", id)
	}
	if session.setupPending {
		return fmt.Errorf("acp: session %q setup is pending", id)
	}
	if session.closing {
		return fmt.Errorf("acp: session %q is closing", id)
	}
	if session.active != nil {
		return fmt.Errorf("acp: session %q has an active turn", id)
	}
	return nil
}

// markSessionClosing 原子预留关闭状态，阻止并发 setup 或新的轮次。
func (c *Client) markSessionClosing(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[id]
	if !ok || session.generation != c.generation {
		return fmt.Errorf("acp: unknown session %q", id)
	}
	if session.setupPending {
		return fmt.Errorf("acp: session %q setup is pending", id)
	}
	if session.closing {
		return fmt.Errorf("acp: session %q is closing", id)
	}
	if session.active != nil {
		return fmt.Errorf("acp: session %q has an active turn", id)
	}
	session.closing = true
	return nil
}

func (c *Client) rollbackSessionClosing(id string) {
	c.mu.Lock()
	if session, ok := c.sessions[id]; ok {
		session.closing = false
	}
	c.mu.Unlock()
}

func (c *Client) finishSessionClose(id string) {
	c.mu.Lock()
	delete(c.sessions, id)
	c.closedSessions[id] = struct{}{}
	c.mu.Unlock()
}

// beginTurn 为会话取得唯一活动轮次，并拒绝已关闭或正在关闭的会话。
func (c *Client) beginTurn(sessionID string) (*turnState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[sessionID]
	if !ok || session.generation != c.generation || session.setupPending || session.closing {
		return nil, fmt.Errorf("acp: unknown session %q", sessionID)
	}
	if session.active != nil {
		return nil, fmt.Errorf("acp: session %q already has an active turn", sessionID)
	}
	turn := &turnState{}
	session.active = turn
	return turn, nil
}

func (c *Client) finishTurn(sessionID string, turn *turnState, reason string) {
	turn.mu.Lock()
	if turn.terminal {
		turn.mu.Unlock()
		return
	}
	turn.terminal = true
	turn.terminalReason = reason
	turn.mu.Unlock()
	c.mu.Lock()
	if session, ok := c.sessions[sessionID]; ok && session.active == turn {
		session.active = nil
		session.lastTerminal = reason
	}
	c.mu.Unlock()
}

func (c *Client) reserveNewSession() (*newSessionReservation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.terminated {
		return nil, ErrClientClosed
	}
	if len(c.sessions)+c.sessionReservations >= MaxSessions {
		return nil, ErrSessionLimit
	}
	c.sessionReservations++
	return &newSessionReservation{client: c}, nil
}

func (r *newSessionReservation) release() {
	if r == nil || r.client == nil {
		return
	}
	r.once.Do(func() {
		r.client.releaseOwnedNewSessionReservation()
	})
}

func (r *newSessionReservation) commit(id string) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("acp: nil session reservation")
	}
	var err error
	committed := false
	r.once.Do(func() {
		committed = true
		err = r.client.commitNewSession(id)
	})
	if !committed {
		return fmt.Errorf("acp: session reservation already resolved")
	}
	return err
}

func (c *Client) releaseOwnedNewSessionReservation() {
	c.mu.Lock()
	if c.sessionReservations > 0 {
		c.sessionReservations--
	}
	c.mu.Unlock()
}

// commitNewSession 一次性消费 reservation，并在 admission lock 内检查关闭墓碑与上限。
func (c *Client) commitNewSession(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionReservations <= 0 {
		return fmt.Errorf("acp: session reservation is not owned")
	}
	c.sessionReservations--
	if c.closed || c.terminated {
		return ErrClientClosed
	}
	if _, closed := c.closedSessions[id]; closed {
		return ErrSessionClosed
	}
	if _, exists := c.sessions[id]; exists {
		return fmt.Errorf("acp: duplicate session %q", id)
	}
	if len(c.sessions) >= MaxSessions {
		return ErrSessionLimit
	}
	c.sessions[id] = &sessionState{id: id, generation: c.generation}
	return nil
}

// reserveExistingSession 预登记 load/resume，防止删除后的会话重新出现。
func (c *Client) reserveExistingSession(id string, loaded bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.terminated {
		return ErrClientClosed
	}
	if _, closed := c.closedSessions[id]; closed {
		return ErrSessionClosed
	}
	if _, exists := c.sessions[id]; exists {
		return fmt.Errorf("acp: session %q already exists", id)
	}
	if len(c.sessions) >= MaxSessions {
		return ErrSessionLimit
	}
	c.sessions[id] = &sessionState{id: id, generation: c.generation, loaded: loaded, setupPending: true}
	return nil
}

func (c *Client) removeSetupSession(id string) {
	c.mu.Lock()
	if session, ok := c.sessions[id]; ok && session.setupPending {
		delete(c.sessions, id)
	}
	c.mu.Unlock()
}

func (c *Client) requireInitialized() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.terminated {
		return ErrClientClosed
	}
	if !c.initialized {
		return fmt.Errorf("acp: initialize required")
	}
	return nil
}

func (c *Client) capabilityEnabled(name string) bool {
	c.mu.Lock()
	raw := cloneRaw(c.caps.Capabilities[name])
	c.mu.Unlock()
	if raw == nil {
		return false
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err == nil {
		return enabled
	}
	return false
}

// requireResumeCapability 严格要求官方 sessionCapabilities.resume=true，且在本地预留或发线前失败。
func (c *Client) requireResumeCapability() error {
	c.mu.Lock()
	raw := cloneRaw(c.caps.Capabilities["sessionCapabilities"])
	c.mu.Unlock()
	if len(raw) == 0 {
		return fmt.Errorf("acp: sessionCapabilities.resume capability is required")
	}
	var capabilities map[string]json.RawMessage
	if err := json.Unmarshal(raw, &capabilities); err != nil || capabilities == nil {
		if err != nil {
			return fmt.Errorf("acp: malformed sessionCapabilities.resume capability: %w", err)
		}
		return fmt.Errorf("acp: sessionCapabilities.resume capability must be an object")
	}
	resumeRaw, ok := capabilities["resume"]
	if !ok {
		return fmt.Errorf("acp: sessionCapabilities.resume capability is required")
	}
	var enabled bool
	if err := json.Unmarshal(resumeRaw, &enabled); err != nil {
		return fmt.Errorf("acp: malformed sessionCapabilities.resume: %w", err)
	}
	if !enabled {
		return fmt.Errorf("acp: sessionCapabilities.resume capability is not advertised")
	}
	return nil
}

func validStopReason(reason string) bool {
	switch reason {
	case "end_turn", "max_tokens", "max_turn_requests", "refusal", "cancelled":
		return true
	default:
		return false
	}
}

func requireObjectValue(raw json.RawMessage, label string) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		if err != nil {
			return fmt.Errorf("acp: %s must be an object: %w", label, err)
		}
		return fmt.Errorf("acp: %s must be an object", label)
	}
	return nil
}

func requireArrayValue(raw json.RawMessage, label string) error {
	var value []json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		if err != nil {
			return fmt.Errorf("acp: %s must be an array: %w", label, err)
		}
		return fmt.Errorf("acp: %s must be an array", label)
	}
	return nil
}

// Session 返回指定会话的不可变快照及当前取消标记。
func (c *Client) Session(id string) (SessionSnapshot, bool) {
	c.mu.Lock()
	session, ok := c.sessions[id]
	if !ok {
		c.mu.Unlock()
		return SessionSnapshot{}, false
	}
	active := session.active != nil
	turn := session.active
	snapshot := SessionSnapshot{ID: session.id, Generation: session.generation, Loaded: session.loaded, SetupPending: session.setupPending, ActiveTurn: active, LastTerminal: session.lastTerminal}
	c.mu.Unlock()
	cancelled := false
	if active {
		turn.mu.Lock()
		cancelled = turn.cancelRequested
		turn.mu.Unlock()
	}
	snapshot.CancelRequested = cancelled
	return snapshot, true
}
