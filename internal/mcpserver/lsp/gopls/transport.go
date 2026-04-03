package gopls

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
func (t *transport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ctx = normalizeContext(ctx)
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
		return cloneRawMessage(outcome.result), outcome.err
	case <-ctx.Done():
		t.removePending(key)
		return nil, ctx.Err()
	}
}
func (t *transport) notify(ctx context.Context, method string, params any) error {
	ctx = normalizeContext(ctx)
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
		go t.respondToServerRequest(envelope)
		return nil
	}
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
			Data:    cloneRawMessage(response.Error.Data),
		}}
		return nil
	}
	result <- pendingResult{result: cloneRawMessage(response.Result)}
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
func defaultServerRequestResult(method string, params json.RawMessage) (any, error) {
	switch method {
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create":
		return struct{}{}, nil
	case "workspace/configuration":
		return emptyConfigurationResult(params), nil
	case "workspace/semanticTokens/refresh", "workspace/codeLens/refresh", "workspace/inlayHint/refresh", "workspace/diagnostic/refresh":
		return struct{}{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrMethodNotSupported, method)
	}
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
