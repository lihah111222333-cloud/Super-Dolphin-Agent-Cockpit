package acpnode

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// handleCancelRequest 同时取消出站待决调用和入站反向处理器。
func (c *Client) handleCancelRequest(raw json.RawMessage) error {
	params, err := methodParamsObject(raw, "$/cancel_request")
	if err != nil {
		return err
	}
	idRaw, ok := params["requestId"]
	if !ok {
		return fmt.Errorf("acp: missing requestId")
	}
	id, err := decodeID(idRaw)
	if err != nil {
		return err
	}
	key := id.key
	c.mu.Lock()
	call, exists := c.pending[key]
	if exists {
		delete(c.pending, key)
		c.addTombstoneLocked(key)
	}
	reverseCancel := c.reverseCancels[key]
	c.mu.Unlock()
	if exists {
		call.result <- pendingResult{err: context.Canceled}
	}
	if reverseCancel != nil {
		reverseCancel()
	}
	return nil
}

func (c *Client) handleSessionUpdate(raw json.RawMessage) error {
	sessionID, update, err := parseSessionUpdate(raw)
	if err != nil {
		return err
	}
	return c.publishSessionUpdate(sessionID, raw, update)
}

// parseSessionUpdate 在发布前验证会话标识和更新对象的结构边界。
func parseSessionUpdate(raw json.RawMessage) (string, json.RawMessage, error) {
	params, err := methodParamsObject(raw, "session/update")
	if err != nil {
		return "", nil, err
	}
	sessionID, err := requiredString(params, "sessionId")
	if err != nil {
		return "", nil, err
	}
	update, ok := params["update"]
	if !ok || len(update) == 0 {
		return "", nil, fmt.Errorf("acp: missing update")
	}
	if err := validateJSONValue(update, MaxJSONDepth, MaxMembers); err != nil {
		return "", nil, fmt.Errorf("acp: invalid sessionUpdate: %w", err)
	}
	if err := requireObjectValue(update, "sessionUpdate"); err != nil {
		return "", nil, err
	}
	return sessionID, update, nil
}

// publishSessionUpdate 只向当前代际的已知会话投递有界通知。
func (c *Client) publishSessionUpdate(sessionID string, raw, _ json.RawMessage) error {
	c.mu.Lock()
	session, known := c.sessions[sessionID]
	if c.closed || c.terminated || c.updatesClosed {
		c.mu.Unlock()
		return nil
	}
	if !known {
		c.mu.Unlock()
		return fmt.Errorf("acp: unknown session update %q", sessionID)
	}
	if session.generation != c.generation {
		c.mu.Unlock()
		return fmt.Errorf("acp: stale session update %q", sessionID)
	}
	if len(c.updates) >= MaxUpdates {
		c.mu.Unlock()
		return ErrUpdateOverflow
	}
	c.updates <- Update{SessionID: sessionID, Method: "session/update", Params: cloneRaw(raw), Generation: c.generation}
	c.mu.Unlock()
	return nil
}

func (c *Client) sendRPCError(id json.RawMessage, code int, message string) error {
	return c.sendContext(context.Background(), Message{JSONRPC: "2.0", ID: cloneRaw(id), Error: &RPCError{Code: code, Message: message}}, c.cfg.ShutdownTimeout)
}

func (c *Client) sendContext(ctx context.Context, m Message, timeout time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("acp: nil write context")
	}
	if c.cfg.ShutdownTimeout > timeout {
		timeout = c.cfg.ShutdownTimeout
	}
	payload, err := marshalMessageBounded(m, c.cfg.MaxMessage)
	if err != nil {
		return err
	}
	return c.sendPayloadContext(ctx, appendWireLine(payload), timeout)
}

// sendPayloadContext 串行执行已预检 payload 的有界协议写入并登记 Write owner。
func (c *Client) sendPayloadContext(ctx context.Context, payload []byte, timeout time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("acp: nil write context")
	}
	if len(payload) == 0 || len(payload) > c.cfg.MaxMessage+1 || payload[len(payload)-1] != '\n' {
		return fmt.Errorf("acp: invalid preflighted protocol payload")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	admission, err := c.beginWriteAdmission()
	if err != nil {
		return err
	}
	defer admission.release()
	stdin := c.p.Stdin()
	return writeBytesBoundedContextTracked(ctx, stdin, payload, timeout, c.closeStdin, func(owner *writeOwner) {
		c.trackWriteOwner(owner)
		admission.release()
	})
}

type writeAdmission struct {
	client *Client
	once   sync.Once
}

func (c *Client) beginWriteAdmission() (*writeAdmission, error) {
	c.writeAdmissionMu.Lock()
	defer c.writeAdmissionMu.Unlock()
	if c.writeAdmissionClosed {
		return nil, ErrClientClosed
	}
	c.mu.Lock()
	closed := c.closed || c.terminated
	c.mu.Unlock()
	if closed {
		return nil, ErrClientClosed
	}
	if c.writeAdmissionActive == 0 {
		c.writeAdmissionDone = make(chan struct{})
	}
	c.writeAdmissionActive++
	return &writeAdmission{client: c}, nil
}

func (a *writeAdmission) release() {
	if a == nil || a.client == nil {
		return
	}
	a.once.Do(func() {
		c := a.client
		c.writeAdmissionMu.Lock()
		c.writeAdmissionActive--
		if c.writeAdmissionActive == 0 {
			close(c.writeAdmissionDone)
		}
		c.writeAdmissionMu.Unlock()
	})
}

func (c *Client) closeWriteAdmission() {
	c.writeAdmissionMu.Lock()
	c.writeAdmissionClosed = true
	done := c.writeAdmissionDone
	c.writeAdmissionMu.Unlock()
	<-done
}

func (c *Client) trackWriteOwner(owner *writeOwner) {
	if owner == nil {
		return
	}
	c.writeOwnersMu.Lock()
	c.writeOwners[owner] = struct{}{}
	c.writeOwnersMu.Unlock()
	c.registerOwner(owner, func() {
		c.writeOwnersMu.Lock()
		delete(c.writeOwners, owner)
		c.writeOwnersMu.Unlock()
	})
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		return nil, fmt.Errorf("acp: nil request context")
	}
	raw, err := mustJSONBounded(params, c.cfg.MaxMessage)
	if err != nil {
		return nil, fmt.Errorf("acp: marshal %s params: %w", method, err)
	}
	return c.requestRaw(ctx, method, raw, c.cfg.RequestTimeout)
}

// requestRaw 在 request ID/pending reservation 前完成完整 envelope 预检。
func (c *Client) requestRaw(ctx context.Context, method string, params json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	if err := validateRequestCall(ctx, method, params, timeout); err != nil {
		return nil, err
	}
	id, payload, err := c.preflightAndAllocateRequest(method, params)
	if err != nil {
		return nil, err
	}
	key, call, err := c.reservePendingForID(id)
	if err != nil {
		return nil, err
	}
	// 请求取消只影响响应等待，协议写入使用独立的有界生命周期，避免已读完的管线写入被竞态截断。
	writeCtx, cancelWrite := boundedContext(context.Background(), c.cfg.ShutdownTimeout)
	defer cancelWrite()
	if err := c.sendPayloadContext(writeCtx, appendWireLine(payload), c.cfg.ShutdownTimeout); err != nil {
		c.finishPending(key, pendingResult{err: err})
		c.fail(err)
		return nil, err
	}
	return c.awaitPending(ctx, key, call, timeout)
}

func (c *Client) preflightAndAllocateRequest(method string, params json.RawMessage) (json.RawMessage, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.terminated {
		return nil, nil, ErrClientClosed
	}
	if c.next == ^uint64(0) {
		return nil, nil, fmt.Errorf("acp: request id exhausted")
	}
	next := c.next + 1
	id := json.RawMessage(fmt.Sprintf("%d", next))
	request := Message{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	payload, err := marshalMessageBounded(request, c.cfg.MaxMessage)
	if err != nil {
		return nil, nil, err
	}
	c.next = next
	return cloneRaw(id), payload, nil
}

// validateRequestCall 校验出站调用上下文、方法和 object/array params 契约。
func validateRequestCall(ctx context.Context, method string, params json.RawMessage, timeout time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("acp: nil request context")
	}
	if method == "" {
		return fmt.Errorf("acp: request method is empty")
	}
	if timeout <= 0 {
		return fmt.Errorf("acp: request timeout must be positive")
	}
	if err := validateJSONValue(params, MaxJSONDepth, MaxMembers); err != nil {
		return fmt.Errorf("acp: invalid %s params: %w", method, err)
	}
	if err := validateParamsShape(params, method); err != nil {
		return err
	}
	return ctx.Err()
}

func (c *Client) reservePendingForID(id json.RawMessage) (string, *pendingCall, error) {
	key, err := semanticIDKey(id)
	if err != nil {
		return "", nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.terminated {
		return "", nil, ErrClientClosed
	}
	if len(c.pending) >= MaxPending {
		return "", nil, ErrPendingLimit
	}
	call := &pendingCall{generation: c.generation, result: make(chan pendingResult, 1)}
	c.pending[key] = call
	return key, call, nil
}

// awaitPending 在响应、调用取消和超时之间只完成一次待决条目。
func (c *Client) awaitPending(ctx context.Context, key string, call *pendingCall, timeout time.Duration) (json.RawMessage, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-call.result:
		return decodePendingResult(result)
	case <-ctx.Done():
		err := ctx.Err()
		if c.finishPending(key, pendingResult{err: err}) {
			return nil, err
		}
		return decodePendingResult(<-call.result)
	case <-timer.C:
		if c.finishPending(key, pendingResult{err: ErrRequestTimeout}) {
			return nil, ErrRequestTimeout
		}
		return decodePendingResult(<-call.result)
	}
}

func decodePendingResult(result pendingResult) (json.RawMessage, error) {
	if result.err != nil {
		return nil, result.err
	}
	if result.message.Error != nil {
		return nil, fmt.Errorf("acp: rpc %d: %s", result.message.Error.Code, result.message.Error.Message)
	}
	return cloneRaw(result.message.Result), nil
}

func (c *Client) finishPending(key string, result pendingResult) bool {
	c.mu.Lock()
	call, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
		c.addTombstoneLocked(key)
	}
	c.mu.Unlock()
	if ok {
		call.result <- result
	}
	return ok
}

func (c *Client) addTombstoneLocked(key string) {
	if _, exists := c.tombstones[key]; exists {
		return
	}
	c.tombstones[key] = struct{}{}
	c.tombstoneOrder = append(c.tombstoneOrder, key)
	if len(c.tombstoneOrder) > MaxPending {
		oldest := c.tombstoneOrder[0]
		c.tombstoneOrder = c.tombstoneOrder[1:]
		delete(c.tombstones, oldest)
	}
}
