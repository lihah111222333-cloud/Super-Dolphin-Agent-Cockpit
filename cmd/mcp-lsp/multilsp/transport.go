package multilsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultRequestTimeout = 30 * time.Second
	jsonRPCMethodNotFound = -32601
	jsonRPCInternalError  = -32603
)

type ServerRequestHandler func(context.Context, string, json.RawMessage) (any, error)
type transportOptions struct {
	Binary, Dir         string
	Args, Env           []string
	NotificationHandler protocol.NotificationHandler
	RequestHandler      ServerRequestHandler
}
type transport struct {
	cmd                 *exec.Cmd
	stdin               io.WriteCloser
	stdout              *bufio.Reader
	stderr              *limitedBuffer
	notificationHandler protocol.NotificationHandler
	requestHandler      ServerRequestHandler
	writeMu             sync.Mutex
	pendingMu           sync.Mutex
	pending             map[string]chan pendingResult
	nextID              atomic.Int64
	closed              atomic.Bool
	done                chan struct{}
	doneMu              sync.Mutex
	doneErr             error

	// responderWG tracks server-initiated request responder
	// goroutines. P22 P2 LSP-S3 (plan §484 / §489) owns the
	// contract: dispatchMessage must register with Add(1) before
	// spawning, and Close must Wait (bounded by
	// defaultResponderDrainTimeout) to drain them instead of leaving
	// fire-and-forget goroutines outliving the transport.
	responderWG sync.WaitGroup
}
type pendingResult struct {
	result json.RawMessage
	err    error
}

func newTransport(options transportOptions) (*transport, error) {
	cmd, stdin, stdout, stderr, err := startTransport(options)
	if err != nil {
		return nil, err
	}
	t := &transport{
		cmd:                 cmd,
		stdin:               stdin,
		stdout:              bufio.NewReader(stdout),
		stderr:              stderr,
		notificationHandler: options.NotificationHandler,
		requestHandler:      options.RequestHandler,
		pending:             map[string]chan pendingResult{},
		done:                make(chan struct{}),
	}
	go t.wait()
	go t.readLoop()
	return t, nil
}

// request 处理请求。
func (t *transport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ctx = platformshared.NonNilContext(ctx)
	ctx, cancel := platformconfig.WithTimeoutIfNone(ctx, defaultRequestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, err := protocol.BuildRequest(method, t.nextID.Add(1), params)
	if err != nil {
		return nil, err
	}
	key, result := normalizeID(request.ID), make(chan pendingResult, 1)
	if err := t.addPending(key, result); err != nil {
		return nil, err
	}
	if err := t.writeMessage(request); err != nil {
		t.removePending(key)
		return nil, err
	}
	select {
	case outcome := <-result:
		return platformshared.CloneRawMessage(outcome.result), outcome.err
	case <-ctx.Done():
		t.removePending(key)
		return nil, ctx.Err()
	}
}
func (t *transport) notify(ctx context.Context, method string, params any) error {
	ctx = platformshared.NonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	notification, err := protocol.BuildNotification(method, params)
	if err != nil {
		return err
	}
	return t.writeMessage(notification)
}
func (t *transport) dispatchMessage(payload json.RawMessage) error {
	envelope, err := protocol.DecodeEnvelope(payload)
	if err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(envelope.Method) == "":
		return t.handleResponse(payload)
	case normalizeID(envelope.ID) == "":
		return t.handleNotification(payload)
	default:
		t.spawnResponder(envelope)
		return nil
	}
}

// spawnResponder launches a server-request responder goroutine while
// keeping its lifecycle owned by the transport. Post-P22 P2 LSP-S3
// the responder is no longer fire-and-forget: Add(1) runs before the
// go statement so Close() can Wait for the goroutine to drain, and a
// late spawn that races past Close returns without spawning so we
// don't stall the drain. ctx is handed to the responder so a
// subsequent cancel can cut long serverRequestResult work short once
// the plan calls for it.
func (t *transport) spawnResponder(envelope protocol.Envelope) {
	if t.closed.Load() {
		return
	}
	t.responderWG.Add(1)
	go func() {
		defer t.responderWG.Done()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("LSP responder panic", "panic", fmt.Sprint(r))
			}
		}()
		t.respondToServerRequest(envelope)
	}()
}
func (t *transport) handleResponse(payload json.RawMessage) error {
	response, err := protocol.DecodeResponse(payload)
	if err != nil {
		return err
	}
	result := t.removePending(normalizeID(response.ID))
	if result == nil {
		return nil
	}
	if response.Error != nil {
		result <- pendingResult{err: &responseError{
			Code:    response.Error.Code,
			Message: response.Error.Message,
			Data:    platformshared.CloneRawMessage(response.Error.Data),
		}}
		return nil
	}
	result <- pendingResult{result: platformshared.CloneRawMessage(response.Result)}
	return nil
}
func (t *transport) handleNotification(payload json.RawMessage) error {
	if t.notificationHandler == nil {
		return nil
	}
	err := protocol.DispatchNotification(payload, t.notificationHandler)
	if errors.Is(err, protocol.ErrUnsupportedNotification) {
		return nil
	}
	return err
}
func (t *transport) respondToServerRequest(request protocol.Envelope) {
	result, err := t.serverRequestResult(context.Background(), request.Method, request.Params)
	message, err := buildServerResponse(request.ID, result, err)
	if err != nil {
		t.stopWithError(err)
		return
	}
	if err := t.writeMessage(message); err != nil && !errors.Is(err, ErrTransportClosed) {
		t.stopWithError(err)
	}
}
func (t *transport) serverRequestResult(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if t.requestHandler != nil {
		result, err := t.requestHandler(ctx, method, params)
		if err == nil || !errors.Is(err, ErrMethodNotSupported) {
			return result, err
		}
	}
	return defaultServerRequestResult(method, params)
}
func buildServerResponse(id json.RawMessage, result any, err error) (any, error) {
	if err == nil {
		return protocol.BuildSuccessResponse(id, result)
	}
	if errors.Is(err, ErrMethodNotSupported) {
		return protocol.BuildErrorResponse(id, jsonRPCMethodNotFound, err.Error(), nil)
	}
	return protocol.BuildErrorResponse(id, jsonRPCInternalError, err.Error(), nil)
}

// defaultServerRequestResult is the transport-side entry point for
// answering server-initiated requests. The frozen compatibility
// contract (see transport_compat.go, P22 P4 §309-311) owns the method
// set and response shapes so this file only holds transport glue.
func defaultServerRequestResult(method string, params json.RawMessage) (any, error) {
	return dispatchCompatServerRequest(method, params)
}
func emptyConfigurationResult(params json.RawMessage) []any {
	var request struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(params, &request); err != nil || len(request.Items) == 0 {
		return []any{}
	}
	return make([]any, len(request.Items))
}
func (t *transport) addPending(key string, result chan pendingResult) error {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	if t.closed.Load() {
		return ErrTransportClosed
	}
	t.pending[key] = result
	return nil
}
func (t *transport) removePending(key string) chan pendingResult {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	result := t.pending[key]
	delete(t.pending, key)
	return result
}
func (t *transport) clearPending(err error) {
	t.pendingMu.Lock()
	pending := t.pending
	t.pending = map[string]chan pendingResult{}
	t.pendingMu.Unlock()
	for _, result := range pending {
		select {
		case result <- pendingResult{err: err}:
		default:
		}
	}
}
