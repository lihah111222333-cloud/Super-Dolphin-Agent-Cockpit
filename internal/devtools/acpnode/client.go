package acpnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

var (
	ErrClientClosed    = errors.New("acp: client closed")
	ErrPendingLimit    = errors.New("acp: pending limit")
	ErrSessionLimit    = errors.New("acp: session limit")
	ErrReverseLimit    = errors.New("acp: reverse request limit")
	ErrUpdateOverflow  = errors.New("acp: update queue overflow")
	ErrSessionClosed   = errors.New("acp: session was closed")
	ErrShutdownTimeout = errors.New("acp: shutdown timeout")
	ErrRequestTimeout  = errors.New("acp: request timeout")
	ErrWriteTimeout    = errors.New("acp: protocol write timeout")
)

// ReverseRequestHandler 处理 ACP 对端发起的反向请求，并返回可编码结果或显式错误。
type ReverseRequestHandler func(context.Context, string, json.RawMessage) (any, error)

// Update is a validated session/update notification. Params is copied before
// publication so a caller cannot mutate the client's protocol state.
type Update struct {
	SessionID  string
	Method     string
	Params     json.RawMessage
	Generation uint64
}

// CapabilitySnapshot is immutable from the client's point of view. Each call
// to Capabilities returns a deep copy of the raw values.
type CapabilitySnapshot struct {
	ProtocolVersion int
	Capabilities    map[string]json.RawMessage
}

// SessionSnapshot exposes the bounded local state machine without exposing
// mutable session internals.
type SessionSnapshot struct {
	ID              string
	Generation      uint64
	Loaded          bool
	SetupPending    bool
	ActiveTurn      bool
	CancelRequested bool
	LastTerminal    string
}

type pendingResult struct {
	message Message
	err     error
}

type pendingCall struct {
	generation uint64
	result     chan pendingResult
}

type turnState struct {
	mu              sync.Mutex
	terminal        bool
	terminalReason  string
	cancelRequested bool
}

type sessionState struct {
	id           string
	generation   uint64
	loaded       bool
	setupPending bool
	closing      bool
	active       *turnState
	lastTerminal string
}

type reverseContextKey struct{}

// Client 持有单个 ACP 子进程、协议流、请求表和有界会话状态。
type Client struct {
	p   Process
	cfg LaunchConfig
	now func() time.Time

	writeMu              sync.Mutex
	writeOwnersMu        sync.Mutex
	writeOwners          map[*writeOwner]struct{}
	writeAdmissionMu     sync.Mutex
	writeAdmissionActive int
	writeAdmissionClosed bool
	writeAdmissionDone   chan struct{}
	ownersMu             sync.Mutex
	owners               map[trackedOwner]struct{}
	mu                   sync.Mutex
	next                 uint64
	pending              map[string]*pendingCall

	tombstones            map[string]struct{}
	tombstoneOrder        []string
	inboundIDs            map[string]struct{}
	inboundTombstones     map[string]struct{}
	inboundTombstoneOrder []string
	reverseCancels        map[string]context.CancelFunc
	reverseSlots          chan struct{}
	reverse               ReverseRequestHandler
	reverseWG             sync.WaitGroup
	reverseActive         int
	reverseDone           chan struct{}

	updates              chan Update
	updatesClosed        bool
	initialized          bool
	initializing         bool
	caps                 CapabilitySnapshot
	sessions             map[string]*sessionState
	closedSessions       map[string]struct{}
	sessionReservations  int
	closed               bool
	terminated           bool
	generation           uint64
	failureErr           error
	terminalErr          error
	lateStreamFailureErr error

	waitDone    chan struct{}
	done        chan struct{}
	waitErr     error
	waitOwner   *processActionOwner
	stdoutOwner *streamOwner
	stderrOwner *streamOwner
	waitOnce    sync.Once
	doneOnce    sync.Once

	closeMu      sync.Mutex
	closeStarted bool
	closeDone    chan struct{}
	closeErr     error

	stdinOnce     sync.Once
	stdinErr      error
	stdoutOnce    sync.Once
	stdoutErr     error
	stderrOnce    sync.Once
	stderrErr     error
	interruptOnce sync.Once
	interruptErr  error
	killOnce      sync.Once
	killErr       error
}

// NewClient 启动受边界约束的 ACP 客户端，并在流不完整时回收进程。
func NewClient(cfg LaunchConfig, factory ProcessFactory, reverse ReverseRequestHandler) (*Client, error) {
	if factory == nil {
		return nil, fmt.Errorf("acp: nil process factory")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p, err := startProcess(cfg, factory)
	if err != nil {
		return nil, err
	}
	if err := validateProcessStreams(p); err != nil {
		return nil, errors.Join(err, cleanupProcess(p, cfg.ShutdownTimeout))
	}
	c := &Client{
		p:                  p,
		cfg:                cfg,
		now:                time.Now,
		writeOwners:        make(map[*writeOwner]struct{}),
		writeAdmissionDone: closedChannel(),
		owners:             make(map[trackedOwner]struct{}),
		pending:            make(map[string]*pendingCall),
		tombstones:         make(map[string]struct{}),
		inboundIDs:         make(map[string]struct{}),
		inboundTombstones:  make(map[string]struct{}),
		reverseCancels:     make(map[string]context.CancelFunc),
		reverseSlots:       make(chan struct{}, MaxReverse),
		reverse:            reverse,
		updates:            make(chan Update, MaxUpdates),
		sessions:           make(map[string]*sessionState),
		closedSessions:     make(map[string]struct{}),
		generation:         1,
		waitDone:           make(chan struct{}),
		done:               make(chan struct{}),
		closeDone:          make(chan struct{}),
		reverseDone:        closedChannel(),
	}
	stdoutOwner := newStreamOwner("stdout")
	stderrOwner := newStreamOwner("stderr")
	c.stdoutOwner = stdoutOwner
	c.stderrOwner = stderrOwner
	c.trackOwner(stdoutOwner)
	c.trackOwner(stderrOwner)
	launchACP(context.Background(), "acp stdout reader", func() { c.runStdout(stdoutOwner) })
	launchACP(context.Background(), "acp stderr reader", func() { c.runStderr(stderrOwner) })
	return c, nil
}

func validateProcessStreams(process Process) error {
	if process == nil {
		return fmt.Errorf("acp: process factory returned nil process")
	}
	if process.Stdin() == nil || process.Stdout() == nil || process.Stderr() == nil {
		return fmt.Errorf("acp: process factory returned incomplete process")
	}
	return nil
}

func (c *Client) trackOwner(owner trackedOwner) {
	c.registerOwner(owner, nil)
}

func (c *Client) registerOwner(owner trackedOwner, onDone func()) {
	if owner == nil {
		return
	}
	c.ownersMu.Lock()
	c.owners[owner] = struct{}{}
	c.ownersMu.Unlock()
	owner.setOnDone(func() {
		c.ownersMu.Lock()
		delete(c.owners, owner)
		c.ownersMu.Unlock()
		if onDone != nil {
			onDone()
		}
	})
}

func (c *Client) trackedOwners() []trackedOwner {
	c.ownersMu.Lock()
	defer c.ownersMu.Unlock()
	owners := make([]trackedOwner, 0, len(c.owners))
	for owner := range c.owners {
		owners = append(owners, owner)
	}
	return owners
}

// joinTrackedOwners 在最终 join 窗口内等待所有 owner，并保留非合作操作的类型错误。
func (c *Client) joinTrackedOwners(timeout time.Duration) error {
	if timeout <= 0 {
		return ErrShutdownTimeout
	}
	deadline := c.currentTime().Add(timeout)
	for {
		owners := c.trackedOwners()
		if len(owners) == 0 {
			return nil
		}
		pending := waitTrackedOwners(owners, deadline, c.currentTime)
		if len(pending) > 0 {
			return errors.Join(pending...)
		}
		if !c.currentTime().Before(deadline) {
			return ErrShutdownTimeout
		}
	}
}

func (c *Client) currentTime() time.Time {
	if c == nil || c.now == nil {
		panic("acp: client clock required")
	}
	return c.now()
}

func waitTrackedOwners(owners []trackedOwner, deadline time.Time, now func() time.Time) []error {
	if now == nil {
		panic("acp: owner wait clock required")
	}
	var pending []error
	for _, owner := range owners {
		if err := waitTrackedOwner(owner, deadline, now); err != nil {
			pending = append(pending, err)
		}
	}
	return pending
}

func waitTrackedOwner(owner trackedOwner, deadline time.Time, now func() time.Time) error {
	remaining := deadline.Sub(now())
	if remaining <= 0 {
		return owner.pendingError()
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-owner.Done():
		return completedTrackedOwnerError(owner)
	case <-timer.C:
		return owner.pendingError()
	}
}

func completedTrackedOwnerError(owner trackedOwner) error {
	stream, ok := owner.(*streamOwner)
	if !ok || stream == nil {
		return nil
	}
	err := stream.Err()
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (c *Client) startWait() {
	c.waitOnce.Do(func() {
		owner := newProcessActionOwner()
		c.mu.Lock()
		c.waitOwner = owner
		c.mu.Unlock()
		c.trackOwner(owner)
		launchACP(context.Background(), "acp process wait", func() { runProcessActionOwner(owner, c.p.Wait) })
		launchACP(context.Background(), "acp wait observer", func() { c.waitLoop(owner) })
	})
}

func (c *Client) waitLoop(owner *processActionOwner) {
	err := owner.Join()
	c.mu.Lock()
	c.waitErr = err
	c.refreshTerminalErrorLocked()
	c.mu.Unlock()
	c.terminate(nil)
	close(c.waitDone)
	c.doneOnce.Do(func() { close(c.done) })
}

// drainStderr 持续消费并限制子进程 stderr，污染或超限立即终止会话。
func (c *Client) drainStderr() error {
	b := make([]byte, 4096)
	total := 0
	for {
		n, err := c.p.Stderr().Read(b)
		if n > 0 {
			total += n
			if total > c.cfg.MaxStderr {
				return fmt.Errorf("acp: stderr exceeds %d bytes", c.cfg.MaxStderr)
			}
		}
		if err != nil {
			if err == io.EOF {
				return io.EOF
			}
			return fmt.Errorf("acp: stderr: %w", err)
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}

// readLoop 串行解码 stdout，并把响应、反向请求和通知分派到各自状态机。
func (c *Client) readLoop() error {
	for {
		m, err := readMessage(c.p.Stdout(), c.cfg.MaxMessage)
		if err != nil {
			if err == io.EOF {
				return io.EOF
			}
			return fmt.Errorf("acp: wire contamination: %w", err)
		}
		if m.Method != "" {
			c.dispatchIncoming(m)
			continue
		}
		c.routeResponse(m)
	}
}

func (c *Client) runStdout(owner *streamOwner) {
	err := c.readLoop()
	if errors.Is(err, io.EOF) {
		owner.result <- nil
		owner.finish(nil)
		c.startWait()
		return
	}
	owner.result <- err
	if err != nil && !errors.Is(err, io.EOF) {
		c.recordStreamError(err)
	}
	owner.finish(err)
}

func (c *Client) runStderr(owner *streamOwner) {
	err := c.drainStderr()
	owner.result <- err
	if err != nil && !errors.Is(err, io.EOF) {
		c.recordStreamError(err)
	}
	owner.finish(err)
}

// recordStreamError 记录读流失败，并在进程退出后保留迟到错误供 Close 返回。
func (c *Client) recordStreamError(err error) {
	if err == nil || errors.Is(err, io.EOF) {
		return
	}
	c.mu.Lock()
	closed := c.closed
	waitFinished := false
	select {
	case <-c.waitDone:
		waitFinished = true
	default:
	}
	if closed && errors.Is(err, io.ErrClosedPipe) {
		c.mu.Unlock()
		return
	}
	c.addFailureLocked(err)
	if waitFinished {
		if c.lateStreamFailureErr == nil {
			c.lateStreamFailureErr = err
		} else if !errors.Is(c.lateStreamFailureErr, err) {
			c.lateStreamFailureErr = errors.Join(c.lateStreamFailureErr, err)
		}
	}
	c.mu.Unlock()
	if !closed {
		c.fail(err)
	}
}

// routeResponse 只交付当前 generation 的待决请求，并保留迟到响应墓碑。
func (c *Client) routeResponse(m Message) {
	key, err := semanticIDKey(m.ID)
	if err != nil {
		c.fail(err)
		return
	}
	c.mu.Lock()
	call, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
		c.addTombstoneLocked(key)
	}
	_, late := c.tombstones[key]
	terminated := c.terminated
	currentGeneration := c.generation
	c.mu.Unlock()
	if !ok {
		if late || terminated {
			return
		}
		c.fail(fmt.Errorf("acp: unmatched response id %s", key))
		return
	}
	if call.generation != currentGeneration {
		call.result <- pendingResult{err: fmt.Errorf("acp: stale response generation")}
		return
	}
	call.result <- pendingResult{message: m}
}

// dispatchIncoming 先执行通知形态守卫，再进入反向请求和响应分支。
func (c *Client) dispatchIncoming(m Message) {
	if m.Method == "session/update" {
		if len(m.ID) != 0 {
			c.fail(fmt.Errorf("acp: session/update must be a notification"))
			return
		}
		c.dispatchSessionUpdate(m.Params)
		return
	}
	if m.Method == "$/cancel_request" {
		if len(m.ID) != 0 {
			c.fail(fmt.Errorf("acp: $/cancel_request must be a notification"))
			return
		}
		c.dispatchCancelRequest(m.Params)
		return
	}
	if len(m.ID) == 0 {
		// Unknown notifications are intentionally ignored; they do not create a
		// response obligation or a new capability surface.
		return
	}
	c.dispatchReverse(m)
}

func (c *Client) dispatchSessionUpdate(raw json.RawMessage) {
	if err := c.handleSessionUpdate(raw); err != nil {
		c.fail(err)
	}
}

func (c *Client) dispatchCancelRequest(raw json.RawMessage) {
	if err := c.handleCancelRequest(raw); err != nil {
		c.fail(err)
	}
}

func (c *Client) dispatchReverse(m Message) {
	key, err := semanticIDKey(m.ID)
	if err != nil {
		c.fail(err)
		return
	}
	if err := c.registerInboundID(key); err != nil {
		c.fail(err)
		return
	}
	if !c.acquireReverseSlot(m.ID, key) {
		return
	}
	ctx, cancel, ok := c.registerReverse(key)
	if !ok {
		cancel()
		<-c.reverseSlots
		c.completeInboundID(key)
		return
	}
	launchACP(ctx, "acp reverse request", func() { c.runReverseRequest(ctx, key, cloneRaw(m.ID), m.Method, cloneRaw(m.Params)) })
}

func (c *Client) registerInboundID(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.inboundIDs[key]; exists {
		return fmt.Errorf("acp: duplicate inbound request id %s", key)
	}
	if _, completed := c.inboundTombstones[key]; completed {
		return fmt.Errorf("acp: reused completed inbound request id %s", key)
	}
	c.inboundIDs[key] = struct{}{}
	return nil
}

func (c *Client) completeInboundID(key string) {
	c.mu.Lock()
	c.completeInboundIDLocked(key)
	c.mu.Unlock()
}

func (c *Client) completeInboundIDLocked(key string) {
	delete(c.inboundIDs, key)
	if _, exists := c.inboundTombstones[key]; exists {
		return
	}
	c.inboundTombstones[key] = struct{}{}
	c.inboundTombstoneOrder = append(c.inboundTombstoneOrder, key)
	if len(c.inboundTombstoneOrder) > MaxPending {
		oldest := c.inboundTombstoneOrder[0]
		c.inboundTombstoneOrder = c.inboundTombstoneOrder[1:]
		delete(c.inboundTombstones, oldest)
	}
}

func (c *Client) acquireReverseSlot(id json.RawMessage, key string) bool {
	select {
	case c.reverseSlots <- struct{}{}:
		return true
	default:
		if err := c.sendRPCError(id, -32000, "reverse request limit"); err != nil {
			c.fail(err)
		}
		c.completeInboundID(key)
		return false
	}
}

// registerReverse 原子登记反向请求、取消函数和等待组所有权。
func (c *Client) registerReverse(key string) (context.Context, context.CancelFunc, bool) {
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), reverseContextKey{}, 1))
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.terminated {
		return ctx, cancel, false
	}
	if _, completed := c.inboundTombstones[key]; completed {
		return ctx, cancel, false
	}
	if _, exists := c.reverseCancels[key]; exists {
		return ctx, cancel, false
	}
	if _, exists := c.inboundIDs[key]; !exists {
		return ctx, cancel, false
	}
	if c.reverseActive == 0 {
		c.reverseDone = make(chan struct{})
	}
	c.reverseCancels[key] = cancel
	c.reverseActive++
	c.reverseWG.Add(1)
	return ctx, cancel, true
}

// runReverseRequest 运行反向处理器，并在取消或关闭后抑制迟到响应。
func (c *Client) runReverseRequest(ctx context.Context, key string, id json.RawMessage, method string, params json.RawMessage) {
	defer c.releaseReverse(key)
	var result any
	var handlerErr error
	if c.reverse == nil {
		handlerErr = fmt.Errorf("reverse request denied")
	} else {
		result, handlerErr = c.reverse(ctx, method, params)
	}
	if ctx.Err() != nil {
		return
	}
	if handlerErr != nil {
		if err := c.sendRPCError(id, -32601, "method not found"); err != nil {
			c.fail(err)
		}
		return
	}
	encoded, err := mustJSONBounded(result, c.cfg.MaxMessage)
	if err != nil {
		if sendErr := c.sendRPCError(id, -32603, "internal error"); sendErr != nil {
			c.fail(sendErr)
		}
		return
	}
	if err := c.sendContext(context.Background(), Message{JSONRPC: "2.0", ID: id, Result: encoded}, c.cfg.ShutdownTimeout); err != nil {
		c.fail(err)
	}
}

func (c *Client) releaseReverse(key string) {
	<-c.reverseSlots
	c.mu.Lock()
	delete(c.reverseCancels, key)
	c.completeInboundIDLocked(key)
	c.reverseActive--
	c.reverseWG.Done()
	if c.reverseActive == 0 {
		close(c.reverseDone)
	}
	c.mu.Unlock()
}

func closedChannel() chan struct{} {
	channel := make(chan struct{})
	close(channel)
	return channel
}
