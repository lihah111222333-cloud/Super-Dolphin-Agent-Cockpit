package schema

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	filesystemSnapshotCleanupTimeout = 3 * time.Second
	filesystemSnapshotSweepTimeout   = 5 * time.Second
)

type processGuardAttacher func(*exec.Cmd, *processGuard) error

type processGuardAttachStage string

const (
	processGuardAttachCaptureIdentity      processGuardAttachStage = "capture_identity"
	processGuardAttachValidateProcessGroup processGuardAttachStage = "validate_process_group"
	processGuardAttachValidateOwnership    processGuardAttachStage = "validate_ownership"
	processGuardAttachOpenProcess          processGuardAttachStage = "open_process"
	processGuardAttachAssignJob            processGuardAttachStage = "assign_job"
)

type processGuardAttachProbe func(processGuardAttachStage) error

func verifyHelperPackageInWorker(ctx context.Context, config ClientConfig) ([]byte, error) {
	request := filesystemWorkerRequest{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerVerify,
		HelperPath: config.HelperPath, ManifestPath: config.ManifestPath, Identity: config.Identity,
	}
	setFilesystemWorkerDeadline(ctx, &request)
	return runFilesystemWorker(ctx, ctx, config.FilesystemWorkerPath, func(path string) *exec.Cmd {
		return exec.Command(path)
	}, nil, request, nil, maxHelperBytes, nil)
}

func (client *Client) executeInFilesystemWorker(
	parentCtx context.Context,
	operationCtx context.Context,
	encodedRequest []byte,
	capacity *helperCapacityTracker,
) ([]byte, error) {
	snapshot, err := newFilesystemSnapshotIdentity(client.helperGOOS, client.ownerIdentity)
	if err != nil {
		return nil, newDiagnostic(CodeProcessStartFailed, "create schema snapshot cleanup identity", err)
	}
	request := filesystemWorkerRequest{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerExecute,
		HelperGOOS: client.helperGOOS, ImageBytes: len(client.helperImage), RequestBytes: len(encodedRequest),
		Snapshot: snapshot,
	}
	setFilesystemWorkerDeadline(operationCtx, &request)
	payload := io.MultiReader(bytes.NewReader(client.helperImage), bytes.NewReader(encodedRequest))
	return runFilesystemWorker(
		parentCtx, operationCtx, client.filesystemWorkerPath, client.workerCommand, client.workerEnv,
		request, payload, maxStdoutBytes, capacity,
	)
}

func setFilesystemWorkerDeadline(ctx context.Context, request *filesystemWorkerRequest) {
	if deadline, ok := ctx.Deadline(); ok {
		request.DeadlineUnixNano = deadline.UnixNano()
	}
}

// runFilesystemWorker 启动单次受监管 worker，并在任何取消路径同步回收。
func runFilesystemWorker(
	parentCtx context.Context,
	operationCtx context.Context,
	workerPath string,
	command func(string) *exec.Cmd,
	extraEnv []string,
	request filesystemWorkerRequest,
	payload io.Reader,
	maxPayload int,
	capacity *helperCapacityTracker,
) ([]byte, error) {
	return runFilesystemWorkerWithAttacher(
		parentCtx,
		operationCtx,
		workerPath,
		command,
		extraEnv,
		request,
		payload,
		maxPayload,
		attachProcessGuard,
		capacity,
	)
}

// runFilesystemWorkerWithAttacher 允许故障测试在真实 Start 后注入 guard attach 失败。
func runFilesystemWorkerWithAttacher(
	parentCtx context.Context,
	operationCtx context.Context,
	workerPath string,
	command func(string) *exec.Cmd,
	extraEnv []string,
	request filesystemWorkerRequest,
	payload io.Reader,
	maxPayload int,
	attacher processGuardAttacher,
	capacity *helperCapacityTracker,
) ([]byte, error) {
	if attacher == nil {
		return nil, TransientInitializationError(
			newDiagnostic(CodeProcessStartFailed, "schema filesystem worker guard attacher is nil", nil),
		)
	}
	header, err := encodeFilesystemWorkerRequest(request)
	if err != nil {
		return nil, err
	}
	cmd := command(workerPath)
	if cmd == nil {
		return nil, TransientInitializationError(
			newDiagnostic(CodeProcessStartFailed, "schema filesystem worker command is nil", nil),
		)
	}
	cmd.Env = filesystemWorkerProcessEnvironment(cmd.Env, extraEnv)
	if payload == nil {
		cmd.Stdin = bytes.NewReader(header)
	} else {
		cmd.Stdin = io.MultiReader(bytes.NewReader(header), payload)
	}
	stdout := &boundedBuffer{limit: filesystemWorkerMaxHeaderBytes + maxPayload}
	stderr := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	guard, err := prepareProcessGuard(cmd)
	if err != nil {
		return nil, TransientInitializationError(
			newDiagnostic(CodeProcessStartFailed, "prepare schema filesystem worker process boundary", err),
		)
	}
	if err := cmd.Start(); err != nil {
		closeErr := closeProcessGuard(guard)
		return nil, TransientInitializationError(
			newDiagnostic(CodeProcessStartFailed, "start schema filesystem worker", errors.Join(err, closeErr)),
		)
	}
	if err := attacher(cmd, guard); err != nil {
		return nil, TransientInitializationError(
			cleanupFilesystemWorkerAfterAttachFailure(workerPath, command, request, cmd, guard, err, capacity),
		)
	}
	result, runErr := waitFilesystemWorker(
		parentCtx,
		operationCtx,
		request.Operation,
		cmd,
		guard,
		stdout,
		stderr,
		maxPayload,
		capacity,
	)
	if request.Operation != filesystemWorkerExecute {
		return result, runErr
	}
	cleanupErr := cleanupFilesystemSnapshotWithWorker(
		workerPath,
		command,
		request.Snapshot,
		capacity,
	)
	if cleanupErr != nil {
		return nil, errors.Join(runErr, cleanupErr)
	}
	return result, runErr
}

// cleanupFilesystemWorkerAfterAttachFailure 仅在确认 worker 已回收后清除其已发布 snapshot。
func cleanupFilesystemWorkerAfterAttachFailure(
	workerPath string,
	command func(string) *exec.Cmd,
	request filesystemWorkerRequest,
	cmd *exec.Cmd,
	guard *processGuard,
	attachErr error,
	capacity *helperCapacityTracker,
) error {
	processErr := cleanupUnattachedProcessTree(cmd, guard, attachErr)
	if request.Operation != filesystemWorkerExecute || errorTreeContainsCode(processErr, CodeReapFailed) {
		return processErr
	}
	cleanupErr := cleanupFilesystemSnapshotWithWorker(workerPath, command, request.Snapshot, capacity)
	return errors.Join(processErr, cleanupErr)
}

func cleanupFilesystemSnapshotWithWorker(
	workerPath string,
	command func(string) *exec.Cmd,
	snapshot filesystemSnapshotIdentity,
	capacity *helperCapacityTracker,
) error {
	ctx, cancel := platformconfig.WithTimeout(context.Background(), filesystemSnapshotCleanupTimeout)
	defer cancel()
	request := filesystemWorkerRequest{
		Version:   filesystemWorkerVersion,
		Operation: filesystemWorkerCleanup,
		Snapshot:  snapshot,
	}
	setFilesystemWorkerDeadline(ctx, &request)
	_, err := runFilesystemWorker(ctx, ctx, workerPath, command, nil, request, nil, 0, capacity)
	if err != nil {
		return fmt.Errorf("bounded schema snapshot cleanup failed: %w", err)
	}
	return nil
}

func sweepFilesystemSnapshotsWithWorker(workerPath string) error {
	ctx, cancel := platformconfig.WithTimeout(context.Background(), filesystemSnapshotSweepTimeout)
	defer cancel()
	request := filesystemWorkerRequest{
		Version:   filesystemWorkerVersion,
		Operation: filesystemWorkerSweep,
	}
	setFilesystemWorkerDeadline(ctx, &request)
	_, err := runFilesystemWorker(
		ctx,
		ctx,
		workerPath,
		func(path string) *exec.Cmd { return exec.Command(path) },
		nil,
		request,
		nil,
		0,
		nil,
	)
	if err != nil {
		return fmt.Errorf("sweep stale schema snapshots through bounded worker: %w", err)
	}
	return nil
}

// waitFilesystemWorker 等待 worker 完成，或终止整个进程树后再返回取消结果。
func waitFilesystemWorker(
	parentCtx context.Context,
	operationCtx context.Context,
	operation string,
	cmd *exec.Cmd,
	guard *processGuard,
	stdout *boundedBuffer,
	stderr *boundedBuffer,
	maxPayload int,
	capacity *helperCapacityTracker,
) ([]byte, error) {
	waitResult := make(chan error, 1)
	safego.Go(operationCtx, nil, "toolbridge.schema-filesystem-worker.wait", func(context.Context) {
		waitResult <- waitGuardedProcess(cmd, guard)
	})
	select {
	case waitErr := <-waitResult:
		return completeFilesystemWorkerWait(operation, waitErr, guard, stdout, stderr, maxPayload)
	case <-operationCtx.Done():
		// deadline 与正常退出同时就绪时，优先消费已完成的同步 Wait。
		select {
		case waitErr := <-waitResult:
			return completeFilesystemWorkerWait(operation, waitErr, guard, stdout, stderr, maxPayload)
		default:
		}
		code, message, cause := filesystemWorkerStopReason(parentCtx, operationCtx)
		return nil, stopAndReap(cmd, guard, waitResult, code, message, cause, capacity)
	}
}

func completeFilesystemWorkerWait(
	operation string,
	waitErr error,
	guard *processGuard,
	stdout *boundedBuffer,
	stderr *boundedBuffer,
	maxPayload int,
) ([]byte, error) {
	if err := closeProcessGuard(guard); err != nil {
		return nil, newDiagnostic(CodeProcessExited, "close schema filesystem worker guard", err)
	}
	if stdout.overflow || stderr.overflow {
		return nil, newDiagnostic(CodeOutputTooLarge, "schema filesystem worker output exceeded the frozen cap", nil)
	}
	if waitErr != nil {
		return nil, newDiagnostic(CodeProcessExited, "schema filesystem worker exited non-zero", fmt.Errorf("%w; stderr=%q", waitErr, stderr.String()))
	}
	return decodeFilesystemWorkerResponse(stdout.Bytes(), operation, maxPayload)
}

func filesystemWorkerStopReason(parentCtx, operationCtx context.Context) (Code, string, error) {
	if err := parentCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return CodeTimeout, "schema filesystem worker deadline exceeded", err
		}
		return CodeCancelled, "schema filesystem worker request cancelled", err
	}
	return CodeTimeout, "schema filesystem worker deadline exceeded", operationCtx.Err()
}

func filesystemWorkerProcessEnvironment(current, extra []string) []string {
	if current == nil {
		current = os.Environ()
	}
	environment := make([]string, 0, len(current)+len(extra)+1)
	for _, item := range append(append([]string(nil), current...), extra...) {
		if strings.HasPrefix(item, filesystemWorkerModeEnv+"=") {
			continue
		}
		environment = append(environment, item)
	}
	return append(environment, filesystemWorkerModeEnv+"=1")
}

func encodeFilesystemWorkerRequest(request filesystemWorkerRequest) ([]byte, error) {
	header, err := json.Marshal(request)
	if err != nil {
		return nil, newDiagnostic(CodeInvalidEnvelope, "encode schema filesystem worker request", err)
	}
	if len(header)+1 > filesystemWorkerMaxHeaderBytes {
		return nil, newDiagnostic(CodeInputTooLarge, "schema filesystem worker request header exceeds byte budget", nil)
	}
	return append(header, '\n'), nil
}

// decodeFilesystemWorkerResponse 严格校验响应 identity、错误和 payload 长度。
func decodeFilesystemWorkerResponse(raw []byte, operation string, maxPayload int) ([]byte, error) {
	response, reader, err := decodeFilesystemWorkerResponseHeader(raw)
	if err != nil {
		return nil, StableInitializationError(err)
	}
	if err := validateFilesystemWorkerResponse(response, operation, maxPayload); err != nil {
		if _, ok := InitializationFailureClassOf(err); ok {
			return nil, err
		}
		return nil, StableInitializationError(err)
	}
	payload, err := readFilesystemWorkerResponsePayload(reader, response.PayloadBytes)
	if err != nil {
		return nil, StableInitializationError(err)
	}
	return payload, nil
}

func decodeFilesystemWorkerResponseHeader(raw []byte) (filesystemWorkerResponse, *bufio.Reader, error) {
	reader := bufio.NewReader(bytes.NewReader(raw))
	header, err := readFilesystemWorkerHeader(reader)
	if err != nil {
		return filesystemWorkerResponse{}, nil, newDiagnostic(CodeProtocolViolation, "decode schema filesystem worker response", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(header))
	decoder.DisallowUnknownFields()
	var response filesystemWorkerResponse
	if err := decoder.Decode(&response); err != nil {
		return filesystemWorkerResponse{}, nil, newDiagnostic(CodeProtocolViolation, "decode schema filesystem worker response", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return filesystemWorkerResponse{}, nil, newDiagnostic(CodeProtocolViolation, "decode schema filesystem worker response", err)
	}
	return response, reader, nil
}

// validateFilesystemWorkerResponse 校验 worker 响应 identity、尺寸和错误码。
func validateFilesystemWorkerResponse(response filesystemWorkerResponse, operation string, maxPayload int) error {
	if response.Version != filesystemWorkerVersion || response.Operation != operation || response.PayloadBytes < 0 || response.PayloadBytes > maxPayload {
		return newDiagnostic(CodeProtocolViolation, "schema filesystem worker response identity or size mismatch", nil)
	}
	if response.Error == nil {
		return nil
	}
	return filesystemWorkerResponseError(response)
}

// filesystemWorkerResponseError 校验 worker 错误响应并恢复初始化失败分类。
func filesystemWorkerResponseError(response filesystemWorkerResponse) error {
	if response.PayloadBytes != 0 || !validFilesystemWorkerErrorCode(response.Error.Code) ||
		strings.TrimSpace(response.Error.Message) == "" ||
		!validInitializationFailureClass(response.Error.FailureClass) {
		return newDiagnostic(CodeProtocolViolation, "schema filesystem worker error response is invalid", nil)
	}
	diagnostic := newDiagnostic(response.Error.Code, response.Error.Message, nil)
	if response.Error.FailureClass == InitializationFailureStable {
		return StableInitializationError(diagnostic)
	}
	return TransientInitializationError(diagnostic)
}

func readFilesystemWorkerResponsePayload(reader *bufio.Reader, payloadBytes int) ([]byte, error) {
	payload := make([]byte, payloadBytes)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, newDiagnostic(CodeProtocolViolation, "read schema filesystem worker response payload", err)
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return nil, newDiagnostic(CodeProtocolViolation, "schema filesystem worker response has trailing bytes", err)
	}
	return payload, nil
}

func validFilesystemWorkerErrorCode(code Code) bool {
	switch code {
	case CodeInvalidEnvelope, CodeInputTooLarge, CodeOutputTooLarge, CodeProcessStartFailed,
		CodeProcessExited, CodeTimeout, CodeCancelled, CodeReapFailed:
		return true
	default:
		return false
	}
}

func validInitializationFailureClass(class InitializationFailureClass) bool {
	return class == InitializationFailureStable || class == InitializationFailureTransient
}
