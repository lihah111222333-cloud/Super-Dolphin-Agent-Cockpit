package gopls

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

const (
	defaultShutdownTimeout = 5 * time.Second
	stderrLimitBytes       = 8 * 1024
	jsonRPCMethodNotFound  = -32601
	jsonRPCInternalError   = -32603
)

type ServerRequestHandler func(context.Context, string, json.RawMessage) (any, error)
type transportOptions struct {
	Binary, Dir          string
	Args, Env            []string
	NotificationHandler  protocol.NotificationHandler
	RequestHandler       ServerRequestHandler
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
func startTransport(options transportOptions) (*exec.Cmd, io.WriteCloser, io.ReadCloser, *limitedBuffer, error) {
	cmd := exec.Command(options.Binary, options.Args...)
	cmd.Dir = options.Dir
	if len(options.Env) > 0 {
		cmd.Env = append(os.Environ(), options.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("gopls start stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, fmt.Errorf("gopls start stdout pipe: %w", err)
	}
	stderr := &limitedBuffer{limit: stderrLimitBytes}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, nil, fmt.Errorf("gopls start process: %w", err)
	}
	return cmd, stdin, stdout, stderr, nil
}
func (t *transport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ctx = normalizeContext(ctx)
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
func (t *transport) Close() error {
	if t == nil {
		return nil
	}
	if !t.closed.CompareAndSwap(false, true) {
		return t.waitForExit(defaultShutdownTimeout)
	}
	t.closeInput()
	t.clearPending(ErrTransportClosed)
	return errors.Join(t.killProcess(), t.waitForExit(defaultShutdownTimeout))
}
func (t *transport) readLoop() {
	for {
		payload, err := t.readMessage()
		if err != nil {
			t.stopWithError(t.readFailure(err))
			return
		}
		if err := t.dispatchMessage(payload); err != nil {
			t.stopWithError(err)
			return
		}
	}
}
func (t *transport) readMessage() (json.RawMessage, error) {
	length := -1
	for {
		line, err := t.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("gopls: malformed header %q", line)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil || length < 0 {
			return nil, fmt.Errorf("gopls: invalid Content-Length %q", value)
		}
	}
	if length < 0 {
		return nil, errors.New("gopls: missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(t.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
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
	var request struct{ Items []json.RawMessage `json:"items"` }
	if err := json.Unmarshal(params, &request); err != nil || len(request.Items) == 0 {
		return []any{}
	}
	return make([]any, len(request.Items))
}
func (t *transport) writeMessage(message any) error {
	if t.closed.Load() {
		return ErrTransportClosed
	}
	payload, err := protocol.EncodeMessage(message)
	if err != nil {
		return fmt.Errorf("gopls: encode message: %w", err)
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin == nil {
		return ErrTransportClosed
	}
	if _, err := fmt.Fprintf(t.stdin, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return t.joinWaitError(err)
	}
	if _, err := t.stdin.Write(payload); err != nil {
		return t.joinWaitError(err)
	}
	return nil
}
func (t *transport) wait() {
	err := t.cmd.Wait()
	if stderr := strings.TrimSpace(t.stderr.String()); stderr != "" {
		switch {
		case err != nil:
			err = fmt.Errorf("%w: %s", err, stderr)
		case !t.closed.Load():
			err = errors.New(stderr)
		}
	}
	t.doneMu.Lock()
	t.doneErr = err
	t.doneMu.Unlock()
	close(t.done)
}
func (t *transport) waitErr() error {
	t.doneMu.Lock()
	defer t.doneMu.Unlock()
	return t.doneErr
}
func (t *transport) waitForExit(timeout time.Duration) error {
	select {
	case <-t.done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("gopls: process did not exit within %s", timeout)
	}
}
func (t *transport) killProcess() error {
	if t.cmd == nil || t.cmd.Process == nil {
		return nil
	}
	if err := t.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
func (t *transport) closeInput() {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
}
func (t *transport) readFailure(err error) error {
	if t.closed.Load() {
		return ErrTransportClosed
	}
	if waitErr := t.waitErr(); waitErr != nil {
		if errors.Is(err, io.EOF) {
			return waitErr
		}
		return errors.Join(err, waitErr)
	}
	return err
}
func (t *transport) stopWithError(err error) {
	t.closed.Store(true)
	t.clearPending(err)
	t.closeInput()
	_ = t.killProcess()
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
