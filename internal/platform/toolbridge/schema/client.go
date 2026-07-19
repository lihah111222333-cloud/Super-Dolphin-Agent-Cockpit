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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
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
	HelperPath           string
	ManifestPath         string
	FilesystemWorkerPath string
	Identity             HelperIdentity
}

// Client 每次 Execute 都启动一个新 helper 进程。
type Client struct {
	helperImage          []byte
	helperGOOS           string
	ownerIdentity        pidregistry.StableProcessIdentity
	filesystemWorkerPath string
	operationTimeout     time.Duration
	workerCommand        func(string) *exec.Cmd
	workerEnv            []string
}

// NewClient 创建 one-shot schema helper client。
func NewClient(ctx context.Context, config ClientConfig) (*Client, error) {
	if ctx == nil {
		return nil, newDiagnostic(CodeInvalidEnvelope, "context is required", nil)
	}
	if err := validateClientConfig(config); err != nil {
		return nil, StableInitializationError(newDiagnostic(CodeProcessStartFailed, err.Error(), nil))
	}
	image, err := verifyHelperPackageInWorker(ctx, config)
	if err != nil {
		return nil, newDiagnostic(CodeProcessStartFailed, "verify package-owned schema helper", err)
	}
	ownerIdentity, err := pidregistry.CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		return nil, TransientInitializationError(
			newDiagnostic(CodeProcessStartFailed, "capture schema snapshot owner identity", err),
		)
	}
	return &Client{
		helperImage:          image,
		helperGOOS:           config.Identity.GOOS,
		ownerIdentity:        ownerIdentity,
		filesystemWorkerPath: config.FilesystemWorkerPath,
		operationTimeout:     operationDeadline,
		workerCommand: func(path string) *exec.Cmd {
			return exec.Command(path)
		},
	}, nil
}

func validateClientConfig(config ClientConfig) error {
	if err := validateAbsoluteCleanPath(config.HelperPath, "helper path"); err != nil {
		return err
	}
	if err := validateAbsoluteCleanPath(config.ManifestPath, "helper manifest path"); err != nil {
		return err
	}
	return validateAbsoluteCleanPath(config.FilesystemWorkerPath, "filesystem worker path")
}

func validateAbsoluteCleanPath(path, label string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s is required", label)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s must be absolute and clean", label)
	}
	return nil
}

// WithOperationDeadline 让 lazy 初始化与 helper 执行共享同一个冻结的操作期限。
func WithOperationDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, operationDeadline)
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

// executeProcess 在受监管 worker 内完成快照物化、helper 执行和清理。
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
	raw, err := client.executeInFilesystemWorker(parentCtx, operationCtx, encodedRequest)
	if err != nil {
		return Result{}, err
	}
	return client.finish(operationCtx, request, identity, fence, raw)
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

// run 获取一个全局 helper 槽，并仅在已确认没有未回收进程时归还。
func (limiter *helperLimiter) run(ctx context.Context, operation func() (Result, error)) (Result, error) {
	if err := limiter.acquire(ctx); err != nil {
		return Result{}, err
	}
	result, err := operation()
	if !errorTreeContainsCode(err, CodeReapFailed) {
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
	raw []byte,
) (Result, error) {
	response, err := decodeProtocolResponse(raw)
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
		closeErr := closeProcessGuard(guard)
		return newDiagnostic(
			CodeReapFailed,
			"schema helper was not reaped within one second",
			errors.Join(terminateErr, closeErr, cause),
		)
	}
}

// cleanupUnattachedProcessTree 在 attach 失败后等待完整树被已验证 ownership lease 同步回收。
func cleanupUnattachedProcessTree(cmd *exec.Cmd, guard *processGuard, attachErr error) error {
	terminateErr := terminateUnattachedProcessTree(cmd, guard)
	waitResult := make(chan error, 1)
	reapCtx, cancel := context.WithTimeout(context.Background(), reapDeadline)
	defer cancel()
	safego.Go(reapCtx, nil, "toolbridge.schema-helper.unattached-wait", func(context.Context) {
		waitResult <- cmd.Wait()
	})
	select {
	case <-waitResult:
		closeErr := closeProcessGuard(guard)
		if terminateErr != nil || closeErr != nil {
			return newDiagnostic(
				CodeReapFailed,
				"unattached helper process tree ownership was not proven",
				errors.Join(attachErr, terminateErr, closeErr),
			)
		}
		return newDiagnostic(
			CodeProcessStartFailed,
			"attach helper process boundary",
			attachErr,
		)
	case <-reapCtx.Done():
		closeErr := closeProcessGuard(guard)
		return newDiagnostic(
			CodeReapFailed,
			"unattached helper was not reaped within one second",
			errors.Join(attachErr, terminateErr, closeErr),
		)
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
