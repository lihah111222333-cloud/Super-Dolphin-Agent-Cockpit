package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	maxLiveHelpers    = 2
	capacityWait      = 250 * time.Millisecond
	operationDeadline = 2 * time.Second
	reapDeadline      = time.Second
)

type helperLimiter struct {
	slots chan struct{}
}

func newHelperLimiter(limit int) *helperLimiter {
	return &helperLimiter{slots: make(chan struct{}, limit)}
}

// archguard:ignore global_vars -- 冻结的双槽 limiter 必须约束整个桌面进程，而不是单个 Client。
var globalHelperLimiter = newHelperLimiter(maxLiveHelpers)

// ClientConfig 指定与桌面产物同版本的本地 helper 路径。
type ClientConfig struct {
	HelperPath   string
	ManifestPath string
	Identity     HelperIdentity
}

// Client 每次 Execute 都启动一个新 helper 进程。
type Client struct {
	helperImage      []byte
	helperGOOS       string
	operationTimeout time.Duration
	command          func(string) *exec.Cmd
}

// NewClient 创建 one-shot schema helper client。
func NewClient(config ClientConfig) (*Client, error) {
	if strings.TrimSpace(config.HelperPath) == "" {
		return nil, newDiagnostic(CodeProcessStartFailed, "helper path is required", nil)
	}
	if !filepath.IsAbs(config.HelperPath) || filepath.Clean(config.HelperPath) != config.HelperPath {
		return nil, newDiagnostic(CodeProcessStartFailed, "helper path must be absolute and clean", nil)
	}
	if strings.TrimSpace(config.ManifestPath) == "" || !filepath.IsAbs(config.ManifestPath) ||
		filepath.Clean(config.ManifestPath) != config.ManifestPath {
		return nil, newDiagnostic(CodeProcessStartFailed, "helper manifest path must be absolute and clean", nil)
	}
	image, err := verifyHelperPackage(config.HelperPath, config.ManifestPath, config.Identity)
	if err != nil {
		return nil, newDiagnostic(CodeProcessStartFailed, "verify package-owned schema helper", err)
	}
	return &Client{
		helperImage:      image,
		helperGOOS:       config.Identity.GOOS,
		operationTimeout: operationDeadline,
		command: func(path string) *exec.Cmd {
			return exec.Command(path)
		},
	}, nil
}

// Execute 完成父侧预检、双阶段 fence、进程执行和响应 identity/digest 校验。
func (client *Client) Execute(ctx context.Context, invocation Invocation, fence FenceHook) (Result, error) {
	if ctx == nil {
		return Result{}, newDiagnostic(CodeInvalidEnvelope, "context is required", nil)
	}
	if fence == nil {
		return Result{}, newDiagnostic(CodeGenerationStale, "authority fence hook is required", nil)
	}
	operationCtx, cancel := context.WithTimeout(ctx, client.operationTimeout)
	defer cancel()
	request, err := newProtocolRequest(invocation)
	if err != nil {
		return Result{}, err
	}
	if err := operationContextError(ctx, operationCtx); err != nil {
		return Result{}, err
	}
	identity := FenceIdentity{
		ServerID:            invocation.ServerID,
		ToolName:            invocation.ToolName,
		AuthorityGeneration: invocation.AuthorityGeneration,
		SchemaDigest:        invocation.Schema.Digest,
	}
	if err := fence(operationCtx, FenceBeforeLaunch, identity); err != nil {
		return Result{}, newDiagnostic(CodeGenerationStale, "authority changed before helper launch", err)
	}
	return globalHelperLimiter.run(operationCtx, func() (Result, error) {
		return client.executeProcess(ctx, operationCtx, request, identity, fence)
	})
}

// executeProcess 启动单次 helper，绑定进程边界并收集有界输出。
func (client *Client) executeProcess(
	parentCtx context.Context,
	operationCtx context.Context,
	request protocolRequest,
	identity FenceIdentity,
	fence FenceHook,
) (result Result, resultErr error) {
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return Result{}, newDiagnostic(CodeInvalidEnvelope, "marshal helper request", err)
	}
	if len(encodedRequest) > maxEnvelopeBytes {
		return Result{}, newDiagnostic(CodeInputTooLarge, "helper request exceeds 384 KiB", nil)
	}
	if err := operationContextError(parentCtx, operationCtx); err != nil {
		return Result{}, err
	}
	snapshotPath, cleanupSnapshot, err := writeExecutableSnapshot(client.helperImage, client.helperGOOS)
	if err != nil {
		return Result{}, newDiagnostic(CodeProcessStartFailed, "materialize verified schema helper snapshot", err)
	}
	defer func() {
		if err := cleanupSnapshot(); err != nil {
			result = Result{}
			resultErr = newDiagnostic(CodeProcessExited, "remove verified schema helper snapshot", errors.Join(resultErr, err))
		}
	}()
	cmd := client.command(snapshotPath)
	if cmd == nil {
		return Result{}, newDiagnostic(CodeProcessStartFailed, "helper command is nil", nil)
	}
	cmd.Env = helperEnvironment(cmd.Env)
	cmd.Stdin = bytes.NewReader(encodedRequest)
	stdout := &boundedBuffer{limit: maxStdoutBytes}
	stderr := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := configureProcess(cmd); err != nil {
		return Result{}, newDiagnostic(CodeProcessStartFailed, "configure schema helper process", err)
	}
	if err := cmd.Start(); err != nil {
		return Result{}, newDiagnostic(CodeProcessStartFailed, "start schema helper", err)
	}
	guard, err := attachProcessGuard(cmd)
	if err != nil {
		return Result{}, cleanupUnattachedProcess(cmd, err)
	}
	return client.waitForProcess(parentCtx, operationCtx, request, identity, fence, cmd, guard, stdout, stderr)
}

func (client *Client) waitForProcess(
	parentCtx context.Context,
	operationCtx context.Context,
	request protocolRequest,
	identity FenceIdentity,
	fence FenceHook,
	cmd *exec.Cmd,
	guard *processGuard,
	stdout *boundedBuffer,
	stderr *boundedBuffer,
) (Result, error) {
	waitResult := make(chan error, 1)
	safego.Go(operationCtx, nil, "toolbridge.schema-helper.wait", func(context.Context) {
		waitResult <- cmd.Wait()
	})
	select {
	case waitErr := <-waitResult:
		return client.finish(operationCtx, request, identity, fence, guard, waitErr, stdout, stderr)
	case <-operationCtx.Done():
		code, message, cause := operationStopReason(parentCtx)
		return Result{}, stopAndReap(cmd, guard, waitResult, code, message, cause)
	}
}

func operationContextError(parentCtx, operationCtx context.Context) error {
	if parentErr := parentCtx.Err(); parentErr != nil {
		return newDiagnostic(CodeCancelled, "schema helper request cancelled", parentErr)
	}
	if operationErr := operationCtx.Err(); operationErr != nil {
		return newDiagnostic(CodeTimeout, "schema helper deadline exceeded", operationErr)
	}
	return nil
}

func operationStopReason(parentCtx context.Context) (Code, string, error) {
	if parentErr := parentCtx.Err(); parentErr != nil {
		return CodeCancelled, "schema helper request cancelled", parentErr
	}
	return CodeTimeout, "schema helper deadline exceeded", nil
}

// run 获取一个全局 helper 槽，并仅在已确认没有未回收进程时归还。
func (limiter *helperLimiter) run(ctx context.Context, operation func() (Result, error)) (Result, error) {
	if err := limiter.acquire(ctx); err != nil {
		return Result{}, err
	}
	result, err := operation()
	if ErrorCode(err) != CodeReapFailed {
		<-limiter.slots
	}
	return result, err
}

func (limiter *helperLimiter) acquire(ctx context.Context) error {
	timer := time.NewTimer(capacityWait)
	defer timer.Stop()
	select {
	case limiter.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return newDiagnostic(CodeCancelled, "schema helper capacity wait cancelled", ctx.Err())
	case <-timer.C:
		return newDiagnostic(CodeCapacityExhausted, "schema helper capacity wait exceeded 250 ms", nil)
	}
}

// finish 在成功后验证协议、摘要和调用方拥有的 generation fence。
func (client *Client) finish(
	ctx context.Context,
	request protocolRequest,
	identity FenceIdentity,
	fence FenceHook,
	guard *processGuard,
	waitErr error,
	stdout *boundedBuffer,
	stderr *boundedBuffer,
) (Result, error) {
	if err := closeProcessGuard(guard); err != nil {
		return Result{}, newDiagnostic(CodeProcessExited, "close helper process guard", err)
	}
	if stdout.overflow || stderr.overflow {
		return Result{}, newDiagnostic(CodeOutputTooLarge, "helper stdout or stderr exceeded the frozen cap", nil)
	}
	if waitErr != nil {
		cause := fmt.Errorf("%w; stderr=%q", waitErr, stderr.String())
		return Result{}, newDiagnostic(CodeProcessExited, "schema helper exited non-zero", cause)
	}
	response, err := decodeProtocolResponse(stdout.Bytes())
	if err != nil {
		return Result{}, err
	}
	if err := verifyResponseIdentity(request, response); err != nil {
		return Result{}, err
	}
	result := resultFromResponse(response)
	if !response.OK {
		return result, newDiagnostic(response.Code, response.Message, nil)
	}
	if err := fence(ctx, FenceAfterSuccess, identity); err != nil {
		return Result{}, newDiagnostic(CodeGenerationStale, "authority changed after helper success", err)
	}
	return result, nil
}

// verifyResponseIdentity 校验 helper 回显身份与请求完全一致。
func verifyResponseIdentity(request protocolRequest, response protocolResponse) error {
	if protocolIdentityFromResponse(response) != protocolIdentityFromRequest(request) {
		return newDiagnostic(CodeProtocolViolation, "helper echoed mismatched request identity", nil)
	}
	parentDigest := recomputeDigest(request.CanonicalSchema)
	if request.SchemaDigest != parentDigest {
		return newDiagnostic(CodeDigestMismatch, "parent schema digest mismatch", nil)
	}
	if response.SchemaDigest != parentDigest {
		return newDiagnostic(CodeDigestMismatch, "helper schema digest mismatch", nil)
	}
	if response.OK && response.CompiledDigest != parentDigest {
		return newDiagnostic(CodeDigestMismatch, "helper compiled digest mismatch", nil)
	}
	return nil
}

type protocolIdentity struct {
	Protocol            string
	Operation           Operation
	RequestID           string
	ServerID            string
	ToolName            string
	AuthorityGeneration uint64
	Draft               string
}

func protocolIdentityFromRequest(request protocolRequest) protocolIdentity {
	return protocolIdentity{
		Protocol: request.Protocol, Operation: request.Operation, RequestID: request.RequestID,
		ServerID: request.ServerID, ToolName: request.ToolName,
		AuthorityGeneration: request.AuthorityGeneration, Draft: request.Draft,
	}
}

func protocolIdentityFromResponse(response protocolResponse) protocolIdentity {
	return protocolIdentity{
		Protocol: response.Protocol, Operation: response.Operation, RequestID: response.RequestID,
		ServerID: response.ServerID, ToolName: response.ToolName,
		AuthorityGeneration: response.AuthorityGeneration, Draft: response.Draft,
	}
}

func resultFromResponse(response protocolResponse) Result {
	result := Result{
		Operation:      response.Operation,
		SchemaDigest:   response.SchemaDigest,
		CompiledDigest: response.CompiledDigest,
		Code:           response.Code,
		Message:        response.Message,
	}
	if response.ArgumentsValid != nil {
		result.ArgumentsValid = *response.ArgumentsValid
	}
	return result
}

func stopAndReap(
	cmd *exec.Cmd,
	guard *processGuard,
	waitResult <-chan error,
	code Code,
	message string,
	cause error,
) error {
	terminateErr := terminateProcessTree(cmd, guard)
	timer := time.NewTimer(reapDeadline)
	defer timer.Stop()
	select {
	case <-waitResult:
		closeErr := closeProcessGuard(guard)
		if terminateErr != nil || closeErr != nil {
			return newDiagnostic(CodeProcessExited, "terminate schema helper process tree", errors.Join(terminateErr, closeErr, cause))
		}
		return newDiagnostic(code, message, cause)
	case <-timer.C:
		return newDiagnostic(CodeReapFailed, "schema helper was not reaped within one second", errors.Join(terminateErr, cause))
	}
}

func cleanupUnattachedProcess(cmd *exec.Cmd, attachErr error) error {
	killErr := cmd.Process.Kill()
	waitResult := make(chan error, 1)
	reapCtx, cancel := context.WithTimeout(context.Background(), reapDeadline)
	defer cancel()
	safego.Go(reapCtx, nil, "toolbridge.schema-helper.unattached-wait", func(context.Context) {
		waitResult <- cmd.Wait()
	})
	select {
	case <-waitResult:
		return newDiagnostic(CodeProcessStartFailed, "attach helper process boundary", errors.Join(attachErr, killErr))
	case <-reapCtx.Done():
		return newDiagnostic(CodeReapFailed, "unattached helper was not reaped within one second", errors.Join(attachErr, killErr))
	}
}

func helperEnvironment(current []string) []string {
	if current == nil {
		current = os.Environ()
	}
	environment := make([]string, 0, len(current)+1)
	for _, item := range current {
		if strings.HasPrefix(item, "GOMEMLIMIT=") {
			continue
		}
		environment = append(environment, item)
	}
	return append(environment, "GOMEMLIMIT=96MiB")
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	total    int
	overflow bool
}

// Write 写入容量受限的缓冲区，并在溢出后继续报告完整写入长度。
func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	buffer.total += len(data)
	remaining := buffer.limit + 1 - buffer.buffer.Len()
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = buffer.buffer.Write(data[:remaining])
	}
	if buffer.total > buffer.limit {
		buffer.overflow = true
	}
	return len(data), nil
}

// Bytes 返回缓冲区中已保留字节的独立副本。
func (buffer *boundedBuffer) Bytes() []byte {
	return buffer.buffer.Bytes()
}

// String 返回缓冲区中已保留的有界文本。
func (buffer *boundedBuffer) String() string {
	return buffer.buffer.String()
}
