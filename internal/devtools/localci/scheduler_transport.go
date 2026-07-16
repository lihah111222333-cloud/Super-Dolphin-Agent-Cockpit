package localci

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	schedulerSocketKeyBytes  = 16
	schedulerTransportIOWait = 5 * time.Second
)

// SchedulerOwner 独占一个 daemon 的 Scheduler、SQLite、flock 与 Unix listener。
type SchedulerOwner struct {
	mu           sync.Mutex
	scheduler    *Scheduler
	identity     daemonIdentity
	listener     net.Listener
	socketPath   string
	socketInfo   os.FileInfo
	connections  map[net.Conn]struct{}
	active       sync.WaitGroup
	serveStarted bool
	serveErr     error
	closed       bool
	closeOnce    sync.Once
	closeErr     error
	replayMu     sync.Mutex
	replayIDs    map[string]struct{}
	replayOrder  []string
	nowFunc      func() time.Time
}

// SchedulerClient 通过 owner-global Unix socket 调用唯一 Scheduler owner。
type SchedulerClient struct {
	mu       sync.Mutex
	identity daemonIdentity
	conn     net.Conn
	closed   bool
	closeErr error
	nowFunc  func() time.Time
}

// OpenSchedulerOwner 在 owner-global 路径打开唯一 Scheduler 与 transport listener。
func OpenSchedulerOwner(ctx context.Context, config SchedulerConfig) (*SchedulerOwner, error) {
	identity, err := schedulerTransportIdentity(ctx, config)
	if err != nil {
		return nil, err
	}
	runtimeRoot, err := defaultSchedulerRuntimeRoot(identity.ownerUID)
	if err != nil {
		return nil, fmt.Errorf("resolve scheduler owner runtime root: %w", err)
	}
	return openSchedulerOwnerAtRuntimeRoot(ctx, identity, runtimeRoot)
}

// DialScheduler 连接 owner-global 路径上的唯一 Scheduler owner。
func DialScheduler(ctx context.Context, config SchedulerConfig) (*SchedulerClient, error) {
	identity, err := schedulerTransportIdentity(ctx, config)
	if err != nil {
		return nil, err
	}
	runtimeRoot, err := defaultSchedulerRuntimeRoot(identity.ownerUID)
	if err != nil {
		return nil, fmt.Errorf("resolve scheduler client runtime root: %w", err)
	}
	return dialSchedulerAtRuntimeRoot(ctx, identity, runtimeRoot)
}

func schedulerTransportIdentity(ctx context.Context, config SchedulerConfig) (daemonIdentity, error) {
	if ctx == nil {
		return daemonIdentity{}, fmt.Errorf("%w: context is required", ErrInvalidSchedulerInput)
	}
	identity, err := newDaemonIdentity(config.Endpoint, config.TLSFingerprint, config.DaemonID, config.OwnerUID)
	if err != nil {
		return daemonIdentity{}, fmt.Errorf("%w: normalize daemon identity: %w", ErrInvalidSchedulerInput, err)
	}
	return identity, nil
}

func openSchedulerOwnerWithRuntimeRoot(
	ctx context.Context,
	config SchedulerConfig,
	runtimeRoot string,
) (*SchedulerOwner, error) {
	identity, err := schedulerTransportIdentity(ctx, config)
	if err != nil {
		return nil, err
	}
	return openSchedulerOwnerAtRuntimeRoot(ctx, identity, runtimeRoot)
}

func openSchedulerOwnerAtRuntimeRoot(
	ctx context.Context,
	identity daemonIdentity,
	runtimeRoot string,
) (*SchedulerOwner, error) {
	socketPath, err := deriveSchedulerSocketPath(runtimeRoot, identity)
	if err != nil {
		return nil, fmt.Errorf("%w: derive scheduler socket path: %w", ErrInvalidSchedulerInput, err)
	}
	scheduler, err := openSchedulerAtRuntimeRoot(ctx, identity, runtimeRoot)
	if err != nil {
		return nil, err
	}
	listener, socketInfo, err := openSchedulerTransportListener(socketPath, identity.ownerUID)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open scheduler transport listener: %w", err), scheduler.Close())
	}
	return &SchedulerOwner{
		scheduler:   scheduler,
		identity:    identity,
		listener:    listener,
		socketPath:  socketPath,
		socketInfo:  socketInfo,
		connections: make(map[net.Conn]struct{}),
		replayIDs:   make(map[string]struct{}),
		replayOrder: make([]string, 0, schedulerReplayWindowCapacity),
		nowFunc:     time.Now,
	}, nil
}

func dialSchedulerWithRuntimeRoot(
	ctx context.Context,
	config SchedulerConfig,
	runtimeRoot string,
) (*SchedulerClient, error) {
	identity, err := schedulerTransportIdentity(ctx, config)
	if err != nil {
		return nil, err
	}
	return dialSchedulerAtRuntimeRoot(ctx, identity, runtimeRoot)
}

func dialSchedulerAtRuntimeRoot(
	ctx context.Context,
	identity daemonIdentity,
	runtimeRoot string,
) (*SchedulerClient, error) {
	socketPath, err := deriveSchedulerSocketPath(runtimeRoot, identity)
	if err != nil {
		return nil, fmt.Errorf("%w: derive scheduler socket path: %w", ErrInvalidSchedulerInput, err)
	}
	conn, err := dialSchedulerTransport(ctx, socketPath, identity.ownerUID)
	if err != nil {
		return nil, fmt.Errorf("%w: dial scheduler owner: %w", ErrSchedulerTransport, err)
	}
	return &SchedulerClient{identity: identity, conn: conn, nowFunc: time.Now}, nil
}

// deriveSchedulerSocketPath 从受信任 runtime root 与完整 identity key 派生短 socket 名。
func deriveSchedulerSocketPath(runtimeRoot string, identity daemonIdentity) (string, error) {
	if runtimeRoot == "" || !filepath.IsAbs(runtimeRoot) || filepath.Clean(runtimeRoot) != runtimeRoot {
		return "", errors.New("scheduler runtime root must be canonical and absolute")
	}
	if len(identity.key) < schedulerSocketKeyBytes*2 || strings.TrimSpace(identity.key) != identity.key {
		return "", errors.New("validated daemon identity key is required")
	}
	name := "s-" + identity.key[:schedulerSocketKeyBytes*2] + ".sock"
	socketPath := filepath.Join(runtimeRoot, name)
	if err := validatePrivateSchedulerParent(socketPath, identity.ownerUID); err != nil {
		return "", err
	}
	return socketPath, nil
}

// Serve 接受多个同 UID client，并在返回前等待全部连接 handler 退出。
func (o *SchedulerOwner) Serve(ctx context.Context) error {
	if o == nil || ctx == nil {
		return fmt.Errorf("%w: owner and context are required", ErrInvalidSchedulerInput)
	}
	if err := o.beginServe(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, o.closeNetwork)
	defer stop()
	for {
		conn, err := o.listener.Accept()
		if err != nil {
			o.active.Wait()
			return o.acceptError(ctx, err)
		}
		if err := o.acceptConnection(ctx, conn); err != nil {
			return err
		}
	}
}

func (o *SchedulerOwner) acceptError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if serveErr := o.currentServeError(); serveErr != nil {
		return serveErr
	}
	if o.isClosed() || errors.Is(err, net.ErrClosed) {
		return ErrSchedulerClosed
	}
	return fmt.Errorf("%w: accept scheduler client: %w", ErrSchedulerTransport, err)
}

func (o *SchedulerOwner) acceptConnection(ctx context.Context, conn net.Conn) error {
	if ctx.Err() == nil && o.startConnection(ctx, conn) {
		return nil
	}
	closeErr := conn.Close()
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), closeErr)
	}
	return errors.Join(ErrSchedulerClosed, closeErr)
}

func (o *SchedulerOwner) beginServe() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.listener == nil || o.scheduler == nil {
		return ErrSchedulerClosed
	}
	if o.serveStarted {
		return fmt.Errorf("%w: owner Serve may only run once", ErrSchedulerState)
	}
	o.serveStarted = true
	return nil
}

func (o *SchedulerOwner) startConnection(ctx context.Context, conn net.Conn) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	o.connections[conn] = struct{}{}
	o.active.Go(func() { o.runConnection(ctx, conn) })
	return true
}

func (o *SchedulerOwner) runConnection(ctx context.Context, conn net.Conn) {
	defer func() {
		if recovered := recover(); recovered != nil {
			o.recordServeError(fmt.Errorf("scheduler connection panic: %v", recovered))
		}
	}()
	o.serveConnection(ctx, conn)
}

// serveConnection 校验 peer UID，并按有界 frame 顺序处理单连接请求。
func (o *SchedulerOwner) serveConnection(ctx context.Context, conn net.Conn) {
	defer o.unregisterConnection(conn)
	defer conn.Close()
	peerUID, err := schedulerTransportPeerUID(conn)
	if err != nil || peerUID != o.identity.ownerUID {
		return
	}
	for {
		if !o.serveFrame(ctx, conn) {
			return
		}
	}
}

// serveFrame 处理一条完整 frame，任一 framing/JSON/I/O 失败都关闭该连接。
func (o *SchedulerOwner) serveFrame(ctx context.Context, conn net.Conn) bool {
	if o.nowFunc == nil {
		o.recordServeError(errors.New("scheduler owner clock is required"))
		return false
	}
	if err := conn.SetDeadline(o.nowFunc().Add(schedulerTransportIOWait)); err != nil {
		return false
	}
	payload, err := readSchedulerFrame(conn)
	if err != nil {
		if !errors.Is(err, net.ErrClosed) && !errors.Is(err, os.ErrDeadlineExceeded) {
			_ = writeSchedulerFrame(conn, schedulerFailureResponse("", err))
		}
		return false
	}
	var request schedulerWireRequest
	if err := decodeStrictSchedulerJSON(payload, &request); err != nil {
		_ = writeSchedulerFrame(conn, schedulerFailureResponse("", err))
		return false
	}
	return writeSchedulerFrame(conn, o.dispatch(ctx, request)) == nil
}

func (o *SchedulerOwner) recordServeError(err error) {
	o.mu.Lock()
	if o.serveErr == nil {
		o.serveErr = err
	} else {
		o.serveErr = errors.Join(o.serveErr, err)
	}
	listener := o.listener
	o.mu.Unlock()
	if listener != nil {
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			o.mu.Lock()
			o.serveErr = errors.Join(o.serveErr, closeErr)
			o.mu.Unlock()
		}
	}
}

func (o *SchedulerOwner) currentServeError() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.serveErr
}

// dispatch 完成 envelope、params 与 replay 校验后才触发 Scheduler 操作。
func (o *SchedulerOwner) dispatch(ctx context.Context, request schedulerWireRequest) schedulerWireResponse {
	if err := validateSchedulerWireRequest(request, o.identity.key); err != nil {
		return schedulerFailureResponse(request.RequestID, err)
	}
	params, err := decodeSchedulerMethodParams(request.Method, request.Params)
	if err != nil {
		return schedulerFailureResponse(request.RequestID, err)
	}
	if err := o.claimRequestID(request.RequestID); err != nil {
		return schedulerFailureResponse(request.RequestID, err)
	}
	result, err := o.executeSchedulerMethod(ctx, request.Method, params)
	if err != nil {
		return schedulerFailureResponse(request.RequestID, err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return schedulerFailureResponse(request.RequestID, fmt.Errorf("encode scheduler result: %w", err))
	}
	return schedulerWireResponse{Version: schedulerProtocolVersion, RequestID: request.RequestID, Result: payload}
}

// decodeSchedulerMethodParams 为每个 method 选择唯一严格 params DTO。
func decodeSchedulerMethodParams(method string, payload json.RawMessage) (any, error) {
	var target any
	switch method {
	case schedulerMethodEnqueue:
		target = &schedulerEnqueueParams{}
	case schedulerMethodReserve, schedulerMethodSnapshot:
		target = &schedulerEmptyParams{}
	case schedulerMethodComplete:
		target = &schedulerCompleteParams{}
	case schedulerMethodState:
		target = &schedulerStateParams{}
	default:
		return nil, fmt.Errorf("%w: method %q is unsupported", ErrSchedulerProtocol, method)
	}
	if err := decodeStrictSchedulerObject(payload, target); err != nil {
		return nil, err
	}
	return target, nil
}

// executeSchedulerMethod 将 transport DTO 映射到窄 Scheduler facade。
func (o *SchedulerOwner) executeSchedulerMethod(ctx context.Context, method string, params any) (any, error) {
	switch method {
	case schedulerMethodEnqueue:
		request := params.(*schedulerEnqueueParams)
		return schedulerEmptyParams{}, o.scheduler.Enqueue(ctx, request.Request)
	case schedulerMethodReserve:
		reservations, err := o.scheduler.ReserveRunnable(ctx)
		return schedulerReserveResult{Reservations: reservations}, err
	case schedulerMethodComplete:
		request := params.(*schedulerCompleteParams)
		return schedulerEmptyParams{}, o.scheduler.Complete(ctx, request.WorkloadID, request.Status)
	case schedulerMethodState:
		request := params.(*schedulerStateParams)
		status, err := o.scheduler.State(request.WorkloadID)
		return schedulerStateResult{Status: status}, err
	case schedulerMethodSnapshot:
		snapshot, err := o.scheduler.Snapshot()
		return schedulerSnapshotResult{Snapshot: snapshot}, err
	default:
		return nil, fmt.Errorf("%w: method %q is unsupported", ErrSchedulerProtocol, method)
	}
}

func (o *SchedulerOwner) claimRequestID(requestID string) error {
	o.replayMu.Lock()
	defer o.replayMu.Unlock()
	if _, exists := o.replayIDs[requestID]; exists {
		return ErrSchedulerReplay
	}
	if len(o.replayOrder) == schedulerReplayWindowCapacity {
		delete(o.replayIDs, o.replayOrder[0])
		o.replayOrder = o.replayOrder[1:]
	}
	o.replayIDs[requestID] = struct{}{}
	o.replayOrder = append(o.replayOrder, requestID)
	return nil
}

func schedulerFailureResponse(requestID string, err error) schedulerWireResponse {
	return schedulerWireResponse{
		Version:   schedulerProtocolVersion,
		RequestID: requestID,
		Error:     schedulerWireErrorFor(err),
	}
}

func (o *SchedulerOwner) unregisterConnection(conn net.Conn) {
	o.mu.Lock()
	delete(o.connections, conn)
	o.mu.Unlock()
}

func (o *SchedulerOwner) closeNetwork() {
	o.mu.Lock()
	listener := o.listener
	connections := make([]net.Conn, 0, len(o.connections))
	for conn := range o.connections {
		connections = append(connections, conn)
	}
	o.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	for _, conn := range connections {
		_ = conn.Close()
	}
}

func (o *SchedulerOwner) isClosed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closed
}

// Close 停止 listener/连接，等待 handler，再关闭 SQLite/flock 并安全移除 socket。
func (o *SchedulerOwner) Close() error {
	if o == nil {
		return ErrSchedulerClosed
	}
	o.closeOnce.Do(func() {
		o.mu.Lock()
		o.closed = true
		o.mu.Unlock()
		o.closeNetwork()
		o.active.Wait()
		schedulerErr := o.scheduler.Close()
		removeErr := removeSchedulerTransportSocket(o.socketPath, o.socketInfo, o.identity.ownerUID)
		o.closeErr = errors.Join(schedulerErr, removeErr)
	})
	return o.closeErr
}

// Enqueue 通过 owner transport durable enqueue 一个 workload。
func (c *SchedulerClient) Enqueue(ctx context.Context, request WorkloadRequest) error {
	return c.call(ctx, schedulerMethodEnqueue, schedulerEnqueueParams{Request: request}, &schedulerEmptyParams{})
}

// ReserveRunnable 通过 owner transport 原子预留当前 runnable workloads。
func (c *SchedulerClient) ReserveRunnable(ctx context.Context) ([]WorkloadReservation, error) {
	var result schedulerReserveResult
	if err := c.call(ctx, schedulerMethodReserve, schedulerEmptyParams{}, &result); err != nil {
		return nil, err
	}
	return result.Reservations, nil
}

// Complete 通过 owner transport 提交终态并释放 leases。
func (c *SchedulerClient) Complete(ctx context.Context, id string, status WorkloadStatus) error {
	params := schedulerCompleteParams{WorkloadID: id, Status: status}
	return c.call(ctx, schedulerMethodComplete, params, &schedulerEmptyParams{})
}

// State 通过 owner transport 读取 workload 状态。
func (c *SchedulerClient) State(ctx context.Context, id string) (WorkloadStatus, error) {
	var result schedulerStateResult
	if err := c.call(ctx, schedulerMethodState, schedulerStateParams{WorkloadID: id}, &result); err != nil {
		return "", err
	}
	return result.Status, nil
}

// Snapshot 通过 owner transport 读取深拷贝 scheduler 快照。
func (c *SchedulerClient) Snapshot(ctx context.Context) (SchedulerSnapshot, error) {
	var result schedulerSnapshotResult
	if err := c.call(ctx, schedulerMethodSnapshot, schedulerEmptyParams{}, &result); err != nil {
		return SchedulerSnapshot{}, err
	}
	return result.Snapshot, nil
}

func (c *SchedulerClient) call(ctx context.Context, method string, params any, result any) error {
	if c == nil || ctx == nil || result == nil {
		return fmt.Errorf("%w: client, context, and result are required", ErrInvalidSchedulerInput)
	}
	request, err := c.buildRequest(method, params)
	if err != nil {
		return err
	}
	return c.callRequest(ctx, request, result)
}

func (c *SchedulerClient) buildRequest(method string, params any) (schedulerWireRequest, error) {
	paramsPayload, err := json.Marshal(params)
	if err != nil {
		return schedulerWireRequest{}, fmt.Errorf("%w: encode scheduler params: %w", ErrSchedulerProtocol, err)
	}
	requestID, err := newSchedulerRequestID()
	if err != nil {
		return schedulerWireRequest{}, err
	}
	return schedulerWireRequest{
		Version:   schedulerProtocolVersion,
		RequestID: requestID,
		DaemonKey: c.identity.key,
		Method:    method,
		Params:    paramsPayload,
	}, nil
}

// callRequest 串行一条 client connection 上的 request/response framing。
func (c *SchedulerClient) callRequest(ctx context.Context, request schedulerWireRequest, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.conn == nil {
		return ErrSchedulerClosed
	}
	clearDeadline, err := setSchedulerCallDeadline(ctx, c.conn, c.nowFunc)
	if err != nil {
		return err
	}
	defer clearDeadline()
	if err := writeSchedulerFrame(c.conn, request); err != nil {
		return schedulerClientIOError(ctx, "write request", err)
	}
	payload, err := readSchedulerFrame(c.conn)
	if err != nil {
		return schedulerClientIOError(ctx, "read response", err)
	}
	var response schedulerWireResponse
	if err := decodeStrictSchedulerJSON(payload, &response); err != nil {
		return err
	}
	if err := validateSchedulerWireResponse(response, request.RequestID); err != nil {
		return err
	}
	if response.Error != nil {
		return schedulerErrorFromWire(response.Error)
	}
	return decodeStrictSchedulerJSON(response.Result, result)
}

// setSchedulerCallDeadline 同时绑定固定 I/O 上限和更早的调用 context deadline。
func setSchedulerCallDeadline(ctx context.Context, conn net.Conn, nowFunc func() time.Time) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if nowFunc == nil {
		return nil, fmt.Errorf("%w: scheduler client clock is required", ErrSchedulerTransport)
	}
	deadline := nowFunc().Add(schedulerTransportIOWait)
	if contextDeadline, exists := ctx.Deadline(); exists && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("%w: set scheduler call deadline: %w", ErrSchedulerTransport, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(nowFunc()) })
	return func() {
		stop()
		_ = conn.SetDeadline(time.Time{})
	}, nil
}

func schedulerClientIOError(ctx context.Context, action string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %s: %w", ErrSchedulerTransport, action, err)
}

func validateSchedulerWireResponse(response schedulerWireResponse, requestID string) error {
	if response.Version != schedulerProtocolVersion {
		return fmt.Errorf("%w: response version %d is unsupported", ErrSchedulerProtocol, response.Version)
	}
	if response.RequestID != requestID {
		return fmt.Errorf("%w: response request ID mismatch", ErrSchedulerProtocol)
	}
	if (len(response.Result) == 0) == (response.Error == nil) {
		return fmt.Errorf("%w: response must contain exactly one of result or error", ErrSchedulerProtocol)
	}
	return nil
}

func newSchedulerRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("%w: generate request ID: %w", ErrSchedulerTransport, err)
	}
	return hex.EncodeToString(value[:]), nil
}

// Close 幂等关闭 client connection，并返回首次关闭结果。
func (c *SchedulerClient) Close() error {
	if c == nil {
		return ErrSchedulerClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.closeErr
	}
	c.closed = true
	if c.conn == nil {
		c.closeErr = ErrSchedulerClosed
		return c.closeErr
	}
	c.closeErr = c.conn.Close()
	c.conn = nil
	return c.closeErr
}
