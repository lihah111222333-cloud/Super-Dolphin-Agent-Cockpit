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
	"path/filepath"
	"strings"
	"time"
)

const (
	filesystemWorkerModeEnv        = "SUPER_DOLPHIN_SCHEMA_FS_WORKER"
	filesystemWorkerExecutableEnv  = "SUPER_DOLPHIN_SCHEMA_FS_WORKER_EXECUTABLE"
	filesystemWorkerVersion        = 2
	filesystemWorkerMaxHeaderBytes = 16 << 10
	filesystemWorkerMaxPathBytes   = 32 << 10
	filesystemWorkerMaxBinaryBytes = 256 << 20

	filesystemWorkerVerify  = "verify"
	filesystemWorkerExecute = "execute"
	filesystemWorkerCleanup = "cleanup"
	filesystemWorkerSweep   = "sweep"
)

type filesystemWorkerRequest struct {
	Version          int                        `json:"version"`
	Operation        string                     `json:"operation"`
	HelperPath       string                     `json:"helper_path,omitempty"`
	ManifestPath     string                     `json:"manifest_path,omitempty"`
	Identity         HelperIdentity             `json:"identity"`
	HelperGOOS       string                     `json:"helper_goos,omitempty"`
	ImageBytes       int                        `json:"image_bytes,omitempty"`
	RequestBytes     int                        `json:"request_bytes,omitempty"`
	DeadlineUnixNano int64                      `json:"deadline_unix_nano,omitempty"`
	Snapshot         filesystemSnapshotIdentity `json:"snapshot"`
}

type filesystemWorkerResponse struct {
	Version      int                    `json:"version"`
	Operation    string                 `json:"operation"`
	PayloadBytes int                    `json:"payload_bytes,omitempty"`
	Error        *filesystemWorkerError `json:"error,omitempty"`
}

type filesystemWorkerError struct {
	Code         Code                       `json:"code"`
	Message      string                     `json:"message"`
	FailureClass InitializationFailureClass `json:"failure_class"`
}

// PrepareFilesystemWorker 固定当前宿主执行物，避免请求期重新解析或读取可替换路径。
func PrepareFilesystemWorker() (func() error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve schema filesystem worker executable: %w", err)
	}
	dir, err := os.MkdirTemp("", "reasonix-schema-fs-worker.")
	if err != nil {
		return nil, fmt.Errorf("create schema filesystem worker directory: %w", err)
	}
	staged := filepath.Join(dir, "worker"+filepath.Ext(executable))
	if err := stageFilesystemWorkerExecutable(executable, staged); err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	previous, hadPrevious := os.LookupEnv(filesystemWorkerExecutableEnv)
	if err := os.Setenv(filesystemWorkerExecutableEnv, staged); err != nil {
		return nil, errors.Join(fmt.Errorf("publish schema filesystem worker: %w", err), os.RemoveAll(dir))
	}
	cleanup := func() error {
		return errors.Join(
			restoreFilesystemWorkerEnvironment(previous, hadPrevious),
			os.RemoveAll(dir),
		)
	}
	if err := sweepFilesystemSnapshotsWithWorker(staged); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	return cleanup, nil
}

func restoreFilesystemWorkerEnvironment(previous string, hadPrevious bool) error {
	if hadPrevious {
		return os.Setenv(filesystemWorkerExecutableEnv, previous)
	}
	return os.Unsetenv(filesystemWorkerExecutableEnv)
}

// PreparedFilesystemWorkerPath 返回启动期固定且只供子进程执行的宿主路径。
func PreparedFilesystemWorkerPath() (string, error) {
	path := strings.TrimSpace(os.Getenv(filesystemWorkerExecutableEnv))
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("prepared schema filesystem worker path is required")
	}
	return path, nil
}

// stageFilesystemWorkerExecutable 以固定预算复制并同步当前宿主执行物。
func stageFilesystemWorkerExecutable(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open schema filesystem worker source: %w", err)
	}
	info, err := source.Stat()
	if err != nil {
		return errors.Join(fmt.Errorf("inspect schema filesystem worker source: %w", err), source.Close())
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > filesystemWorkerMaxBinaryBytes {
		return errors.Join(fmt.Errorf("schema filesystem worker source is invalid: mode=%s size=%d", info.Mode(), info.Size()), source.Close())
	}
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return errors.Join(fmt.Errorf("create staged schema filesystem worker: %w", err), source.Close())
	}
	written, copyErr := io.Copy(target, io.LimitReader(source, info.Size()+1))
	if copyErr == nil && written != info.Size() {
		copyErr = fmt.Errorf("staged schema filesystem worker bytes = %d, want %d", written, info.Size())
	}
	err = errors.Join(copyErr, target.Sync(), target.Close(), source.Close())
	if err != nil {
		return errors.Join(err, os.Remove(targetPath))
	}
	return nil
}

// RunFilesystemWorkerIfRequested 在显式 worker 模式下处理唯一一条严格 framing 请求。
func RunFilesystemWorkerIfRequested(reader io.Reader, writer io.Writer) (bool, error) {
	if os.Getenv(filesystemWorkerModeEnv) != "1" {
		return false, nil
	}
	if reader == nil || writer == nil {
		return true, errors.New("schema filesystem worker reader and writer are required")
	}
	buffered := bufio.NewReader(reader)
	request, err := decodeFilesystemWorkerRequest(buffered)
	if err != nil {
		return true, err
	}
	payload, workerErr := executeFilesystemWorkerRequest(buffered, request)
	response := filesystemWorkerResponse{
		Version: filesystemWorkerVersion, Operation: request.Operation, PayloadBytes: len(payload), Error: workerErr,
	}
	if workerErr != nil {
		response.PayloadBytes = 0
		payload = nil
	}
	return true, encodeFilesystemWorkerResponse(writer, response, payload)
}

// executeFilesystemWorkerRequest 校验 operation 后分派 verify 或 execute 单次请求。
func executeFilesystemWorkerRequest(reader io.Reader, request filesystemWorkerRequest) ([]byte, *filesystemWorkerError) {
	if err := validateFilesystemWorkerRequest(request); err != nil {
		return nil, stableWorkerError(CodeInvalidEnvelope, "validate schema filesystem worker request", err)
	}
	switch request.Operation {
	case filesystemWorkerVerify:
		return executeFilesystemWorkerVerify(reader, request)
	case filesystemWorkerExecute:
		return executeVerifiedImageInWorker(reader, request)
	case filesystemWorkerCleanup:
		return executeFilesystemWorkerCleanup(reader, request)
	case filesystemWorkerSweep:
		return executeFilesystemWorkerSweep(reader)
	default:
		return nil, stableWorkerError(CodeInvalidEnvelope, "unknown schema filesystem worker operation", nil)
	}
}

func executeFilesystemWorkerVerify(reader io.Reader, request filesystemWorkerRequest) ([]byte, *filesystemWorkerError) {
	if err := ensureFilesystemWorkerPayloadEOF(reader); err != nil {
		return nil, stableWorkerError(CodeInvalidEnvelope, "verify request has trailing payload", err)
	}
	image, err := verifyHelperPackage(request.HelperPath, request.ManifestPath, request.Identity)
	if err != nil {
		return nil, classifiedWorkerError(
			CodeProcessStartFailed,
			"verify package-owned schema helper",
			err,
			initializationFailureClassOrTransient(err),
		)
	}
	return image, nil
}

func executeFilesystemWorkerCleanup(reader io.Reader, request filesystemWorkerRequest) ([]byte, *filesystemWorkerError) {
	if err := ensureFilesystemWorkerPayloadEOF(reader); err != nil {
		return nil, stableWorkerError(CodeInvalidEnvelope, "cleanup request has trailing payload", err)
	}
	if err := removeOwnedFilesystemSnapshot(request.Snapshot); err != nil {
		return nil, workerError(CodeProcessExited, "remove owned schema helper snapshot", err)
	}
	return nil, nil
}

func executeFilesystemWorkerSweep(reader io.Reader) ([]byte, *filesystemWorkerError) {
	if err := ensureFilesystemWorkerPayloadEOF(reader); err != nil {
		return nil, stableWorkerError(CodeInvalidEnvelope, "sweep request has trailing payload", err)
	}
	if err := sweepStaleFilesystemSnapshots(); err != nil {
		return nil, workerError(CodeProcessExited, "sweep stale schema helper snapshots", err)
	}
	return nil, nil
}

// executeVerifiedImageInWorker 在 worker 内完成输入读取、快照物化和同步清理。
func executeVerifiedImageInWorker(reader io.Reader, request filesystemWorkerRequest) (payload []byte, responseErr *filesystemWorkerError) {
	image := make([]byte, request.ImageBytes)
	encodedRequest := make([]byte, request.RequestBytes)
	if _, err := io.ReadFull(reader, image); err != nil {
		return nil, workerError(CodeInvalidEnvelope, "read verified helper image", err)
	}
	if _, err := io.ReadFull(reader, encodedRequest); err != nil {
		return nil, workerError(CodeInvalidEnvelope, "read schema helper request", err)
	}
	if err := ensureFilesystemWorkerPayloadEOF(reader); err != nil {
		return nil, workerError(CodeInvalidEnvelope, "execute request has trailing payload", err)
	}
	ctx, cancel := filesystemWorkerContext(request.DeadlineUnixNano)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, workerError(CodeTimeout, "schema helper deadline exceeded before snapshot", err)
	}
	snapshotPath, err := writeExecutableSnapshot(image, request.Snapshot)
	if err != nil {
		return nil, workerError(CodeProcessStartFailed, "materialize verified schema helper snapshot", err)
	}
	defer func() {
		if err := removeOwnedFilesystemSnapshot(request.Snapshot); err != nil {
			payload = nil
			responseErr = workerError(CodeProcessExited, "remove verified schema helper snapshot", err)
		}
	}()
	return runSchemaHelperSnapshot(ctx, snapshotPath, encodedRequest)
}

// runSchemaHelperSnapshot 仅在 deadline 仍有效时启动固定快照并收集有界输出。
func runSchemaHelperSnapshot(ctx context.Context, snapshotPath string, encodedRequest []byte) ([]byte, *filesystemWorkerError) {
	if err := ctx.Err(); err != nil {
		return nil, workerError(CodeTimeout, "schema helper deadline exceeded before launch", err)
	}
	cmd := exec.CommandContext(ctx, snapshotPath)
	cmd.Env = filesystemWorkerChildEnvironment()
	cmd.Stdin = bytes.NewReader(encodedRequest)
	stdout := &boundedBuffer{limit: maxStdoutBytes}
	stderr := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = reapDeadline
	runErr := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, workerError(CodeTimeout, "schema helper deadline exceeded", errors.Join(ctxErr, runErr))
	}
	if stdout.overflow || stderr.overflow {
		return nil, workerError(CodeOutputTooLarge, "helper stdout or stderr exceeded the frozen cap", nil)
	}
	if runErr != nil {
		return nil, workerError(CodeProcessExited, "schema helper exited non-zero", fmt.Errorf("%w; stderr=%q", runErr, stderr.String()))
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func filesystemWorkerContext(deadlineUnixNano int64) (context.Context, context.CancelFunc) {
	if deadlineUnixNano == 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithDeadline(context.Background(), time.Unix(0, deadlineUnixNano))
}

func ensureFilesystemWorkerPayloadEOF(reader io.Reader) error {
	var extra [1]byte
	count, err := reader.Read(extra[:])
	if errors.Is(err, io.EOF) && count == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("trailing payload byte")
}

func filesystemWorkerChildEnvironment() []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, filesystemWorkerModeEnv+"=") {
			continue
		}
		environment = append(environment, item)
	}
	return helperEnvironment(environment)
}

// validateFilesystemWorkerRequest 按操作拆分并拒绝所有多余字段。
func validateFilesystemWorkerRequest(request filesystemWorkerRequest) error {
	if request.Version != filesystemWorkerVersion {
		return errors.New("schema filesystem worker version mismatch")
	}
	if request.DeadlineUnixNano < 0 {
		return errors.New("schema filesystem worker deadline is invalid")
	}
	switch request.Operation {
	case filesystemWorkerVerify:
		return validateFilesystemWorkerVerifyRequest(request)
	case filesystemWorkerExecute:
		return validateFilesystemWorkerExecuteRequest(request)
	case filesystemWorkerCleanup:
		return validateFilesystemWorkerCleanupRequest(request)
	case filesystemWorkerSweep:
		return validateFilesystemWorkerSweepRequest(request)
	default:
		return errors.New("schema filesystem worker operation is invalid")
	}
}

type filesystemWorkerVerifyFields struct {
	helperPath   string
	manifestPath string
	identity     HelperIdentity
}

type filesystemWorkerExecutePayloadFields struct {
	helperGOOS   string
	imageBytes   int
	requestBytes int
}

type filesystemWorkerExecuteFields struct {
	payload  filesystemWorkerExecutePayloadFields
	snapshot filesystemSnapshotIdentity
}

func verifyRequestFields(request filesystemWorkerRequest) filesystemWorkerVerifyFields {
	return filesystemWorkerVerifyFields{
		helperPath: request.HelperPath, manifestPath: request.ManifestPath, identity: request.Identity,
	}
}

func executePayloadFields(request filesystemWorkerRequest) filesystemWorkerExecutePayloadFields {
	return filesystemWorkerExecutePayloadFields{
		helperGOOS: request.HelperGOOS, imageBytes: request.ImageBytes, requestBytes: request.RequestBytes,
	}
}

func executeRequestFields(request filesystemWorkerRequest) filesystemWorkerExecuteFields {
	return filesystemWorkerExecuteFields{payload: executePayloadFields(request), snapshot: request.Snapshot}
}

// validateFilesystemWorkerVerifyRequest 校验 verify 专属字段并拒绝 execute 字段。
func validateFilesystemWorkerVerifyRequest(request filesystemWorkerRequest) error {
	if request.HelperPath == "" || request.ManifestPath == "" {
		return errors.New("schema filesystem verify paths are required")
	}
	if len(request.HelperPath) > filesystemWorkerMaxPathBytes || len(request.ManifestPath) > filesystemWorkerMaxPathBytes {
		return errors.New("schema filesystem verify path exceeds byte budget")
	}
	if executeRequestFields(request) != (filesystemWorkerExecuteFields{}) {
		return errors.New("schema filesystem verify request has execute fields")
	}
	return validateIdentity(request.Identity)
}

// validateFilesystemWorkerExecuteRequest 校验 execute 专属长度并拒绝 verify 字段。
func validateFilesystemWorkerExecuteRequest(request filesystemWorkerRequest) error {
	if verifyRequestFields(request) != (filesystemWorkerVerifyFields{}) {
		return errors.New("schema filesystem execute request has verify fields")
	}
	if strings.TrimSpace(request.HelperGOOS) == "" {
		return errors.New("schema filesystem execute GOOS is required")
	}
	if request.ImageBytes <= 0 || request.ImageBytes > maxHelperBytes {
		return errors.New("schema filesystem execute image size is invalid")
	}
	if request.RequestBytes <= 0 || request.RequestBytes > maxEnvelopeBytes {
		return errors.New("schema filesystem execute request size is invalid")
	}
	if err := validateFilesystemSnapshotIdentity(request.Snapshot); err != nil {
		return err
	}
	if request.Snapshot.HelperGOOS != request.HelperGOOS {
		return errors.New("schema filesystem execute snapshot GOOS mismatch")
	}
	return nil
}

// validateFilesystemWorkerCleanupRequest 只接受完整 snapshot identity。
func validateFilesystemWorkerCleanupRequest(request filesystemWorkerRequest) error {
	if verifyRequestFields(request) != (filesystemWorkerVerifyFields{}) ||
		executePayloadFields(request) != (filesystemWorkerExecutePayloadFields{}) {
		return errors.New("schema filesystem cleanup request has non-cleanup fields")
	}
	return validateFilesystemSnapshotIdentity(request.Snapshot)
}

// validateFilesystemWorkerSweepRequest 拒绝 sweep 之外的全部字段。
func validateFilesystemWorkerSweepRequest(request filesystemWorkerRequest) error {
	if verifyRequestFields(request) != (filesystemWorkerVerifyFields{}) ||
		executeRequestFields(request) != (filesystemWorkerExecuteFields{}) {
		return errors.New("schema filesystem sweep request has non-sweep fields")
	}
	return nil
}

func decodeFilesystemWorkerRequest(reader *bufio.Reader) (filesystemWorkerRequest, error) {
	raw, err := readFilesystemWorkerHeader(reader)
	if err != nil {
		return filesystemWorkerRequest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request filesystemWorkerRequest
	if err := decoder.Decode(&request); err != nil {
		return filesystemWorkerRequest{}, fmt.Errorf("decode schema filesystem worker request: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return filesystemWorkerRequest{}, fmt.Errorf("decode schema filesystem worker request: %w", err)
	}
	return request, nil
}

func readFilesystemWorkerHeader(reader *bufio.Reader) ([]byte, error) {
	var header []byte
	for len(header) <= filesystemWorkerMaxHeaderBytes {
		part, err := reader.ReadSlice('\n')
		header = append(header, part...)
		if err == nil {
			return bytes.TrimSuffix(header, []byte{'\n'}), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, fmt.Errorf("read schema filesystem worker header: %w", err)
		}
	}
	return nil, errors.New("schema filesystem worker header exceeds byte budget")
}

// encodeFilesystemWorkerResponse 写入单行严格 header 和精确长度 payload。
func encodeFilesystemWorkerResponse(writer io.Writer, response filesystemWorkerResponse, payload []byte) error {
	header, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode schema filesystem worker response: %w", err)
	}
	if len(header)+1 > filesystemWorkerMaxHeaderBytes {
		return errors.New("schema filesystem worker response header exceeds byte budget")
	}
	if _, err := writer.Write(append(header, '\n')); err != nil {
		return fmt.Errorf("write schema filesystem worker response header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := writer.Write(payload); err != nil {
			return fmt.Errorf("write schema filesystem worker response payload: %w", err)
		}
	}
	return nil
}

func workerError(code Code, message string, cause error) *filesystemWorkerError {
	return classifiedWorkerError(code, message, cause, InitializationFailureTransient)
}

func stableWorkerError(code Code, message string, cause error) *filesystemWorkerError {
	return classifiedWorkerError(code, message, cause, InitializationFailureStable)
}

func classifiedWorkerError(
	code Code,
	message string,
	cause error,
	class InitializationFailureClass,
) *filesystemWorkerError {
	if cause != nil {
		message = fmt.Sprintf("%s: %v", message, cause)
	}
	if len(message) > maxMessageBytes {
		message = message[:maxMessageBytes]
	}
	return &filesystemWorkerError{Code: code, Message: message, FailureClass: class}
}

func initializationFailureClassOrTransient(err error) InitializationFailureClass {
	class, ok := InitializationFailureClassOf(err)
	if !ok {
		return InitializationFailureTransient
	}
	return class
}
