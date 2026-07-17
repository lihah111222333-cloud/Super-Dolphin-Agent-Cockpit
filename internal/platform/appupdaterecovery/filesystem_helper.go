package appupdaterecovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	releaseFilesystemHelperEnv             = "SUPER_DOLPHIN_RELEASE_FS_HELPER"
	releaseFilesystemHelperExecutableEnv   = "SUPER_DOLPHIN_RELEASE_FS_HELPER_EXECUTABLE"
	releaseFilesystemHelperProtocolVersion = 1
	releaseFilesystemHelperMaxConcurrent   = 4
	releaseFilesystemHelperMaxPathBytes    = 16 << 10
	releaseFilesystemHelperMaxRequestBytes = 32 << 10
	releaseFilesystemHelperMaxOutputBytes  = 64 << 10
	releaseFilesystemHelperMaxBinaryBytes  = 256 << 20
	releaseFilesystemHelperWaitDelay       = time.Second

	releaseFilesystemOperationDigest            = "digest"
	releaseFilesystemOperationCanonicalExisting = "canonical_existing"
)

// archguard:ignore global_vars -- 固定四槽 limiter 必须约束整个 updater/Guard 进程的 helper 并发量。
var releaseFilesystemHelperSlots = make(chan struct{}, releaseFilesystemHelperMaxConcurrent)

type releaseFilesystemHelperRequest struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	Path      string `json:"path"`
}

type releaseFilesystemHelperResponse struct {
	Version   int                           `json:"version"`
	Operation string                        `json:"operation"`
	Value     string                        `json:"value,omitempty"`
	Error     *releaseFilesystemHelperError `json:"error,omitempty"`
}

type releaseFilesystemHelperError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type releaseFilesystemLimitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

// RunReleaseFilesystemHelperIfRequested 在显式 helper 模式下处理唯一请求。
func RunReleaseFilesystemHelperIfRequested(reader io.Reader, writer io.Writer) (bool, error) {
	if os.Getenv(releaseFilesystemHelperEnv) != "1" {
		return false, nil
	}
	if reader == nil || writer == nil {
		return true, errors.New("release filesystem helper reader and writer are required")
	}
	return true, serveReleaseFilesystemHelper(reader, writer)
}

// PrepareReleaseFilesystemHelper 在事务效果发生前暂存当前可执行文件，返回显式清理函数。
func PrepareReleaseFilesystemHelper() (func() error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve release filesystem helper executable: %w", err)
	}
	dir, err := os.MkdirTemp("", "super-dolphin-release-fs-helper-")
	if err != nil {
		return nil, fmt.Errorf("create release filesystem helper directory: %w", err)
	}
	staged := filepath.Join(dir, "helper"+filepath.Ext(executable))
	if err := stageReleaseFilesystemHelperExecutable(executable, staged); err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	previous, hadPrevious := os.LookupEnv(releaseFilesystemHelperExecutableEnv)
	if err := os.Setenv(releaseFilesystemHelperExecutableEnv, staged); err != nil {
		return nil, errors.Join(fmt.Errorf("publish release filesystem helper executable: %w", err), os.RemoveAll(dir))
	}
	return releaseFilesystemHelperCleanup(dir, previous, hadPrevious), nil
}

// stageReleaseFilesystemHelperExecutable 以固定预算复制并落盘当前可执行文件。
func stageReleaseFilesystemHelperExecutable(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open release filesystem helper source: %w", err)
	}
	info, err := source.Stat()
	if err != nil {
		return errors.Join(fmt.Errorf("inspect release filesystem helper source: %w", err), source.Close())
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > releaseFilesystemHelperMaxBinaryBytes {
		return errors.Join(
			fmt.Errorf("release filesystem helper source size or mode is invalid: mode=%s size=%d", info.Mode(), info.Size()),
			source.Close(),
		)
	}
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return errors.Join(fmt.Errorf("create staged release filesystem helper: %w", err), source.Close())
	}
	copyErr := copyAndCloseReleaseFilesystemHelper(source, target, info.Size())
	if err := errors.Join(copyErr, source.Close()); err != nil {
		return errors.Join(err, os.Remove(targetPath))
	}
	return nil
}

// copyAndCloseReleaseFilesystemHelper 复制精确长度、同步并关闭暂存文件。
func copyAndCloseReleaseFilesystemHelper(source io.Reader, target *os.File, expected int64) error {
	written, copyErr := io.Copy(target, io.LimitReader(source, expected+1))
	if copyErr == nil && written != expected {
		copyErr = fmt.Errorf("staged release filesystem helper bytes = %d, want %d", written, expected)
	}
	syncErr := target.Sync()
	closeErr := target.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

// releaseFilesystemHelperCleanup 恢复父进程环境并删除私有暂存目录。
func releaseFilesystemHelperCleanup(dir, previous string, hadPrevious bool) func() error {
	return func() error {
		var envErr error
		if hadPrevious {
			envErr = os.Setenv(releaseFilesystemHelperExecutableEnv, previous)
		} else {
			envErr = os.Unsetenv(releaseFilesystemHelperExecutableEnv)
		}
		return errors.Join(envErr, os.RemoveAll(dir))
	}
}

// computeReleaseDigestInHelper 在有界 helper 进程中计算发布摘要。
func computeReleaseDigestInHelper(ctx context.Context, path string) (string, error) {
	return runReleaseFilesystemHelper(ctx, releaseFilesystemHelperRequest{
		Version: releaseFilesystemHelperProtocolVersion, Operation: releaseFilesystemOperationDigest, Path: path,
	})
}

// canonicalExistingPathInHelper 在有界 helper 进程中解析已存在路径。
func canonicalExistingPathInHelper(ctx context.Context, path string) (string, error) {
	return runReleaseFilesystemHelper(ctx, releaseFilesystemHelperRequest{
		Version: releaseFilesystemHelperProtocolVersion, Operation: releaseFilesystemOperationCanonicalExisting, Path: path,
	})
}

// runReleaseFilesystemHelper 用单个可终止子进程包住一次完整只读操作，并同步 Wait 回收。
func runReleaseFilesystemHelper(ctx context.Context, request releaseFilesystemHelperRequest) (string, error) {
	if ctx == nil {
		return "", errors.New("release filesystem helper context is required")
	}
	if err := context.Cause(ctx); err != nil {
		return "", err
	}
	if err := validateReleaseFilesystemHelperRequest(request); err != nil {
		return "", err
	}
	if err := acquireReleaseFilesystemHelperSlot(ctx); err != nil {
		return "", err
	}
	defer func() { <-releaseFilesystemHelperSlots }()

	payload, err := encodeReleaseFilesystemHelperRequest(request)
	if err != nil {
		return "", err
	}
	raw, err := executeReleaseFilesystemHelperProcess(ctx, payload)
	if err != nil {
		return "", err
	}
	return validateReleaseFilesystemHelperResponse(request, raw)
}

// acquireReleaseFilesystemHelperSlot 限制同一进程并发创建 helper 的数量。
func acquireReleaseFilesystemHelperSlot(ctx context.Context) error {
	select {
	case releaseFilesystemHelperSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// encodeReleaseFilesystemHelperRequest 编码并校验请求字节预算。
func encodeReleaseFilesystemHelperRequest(request releaseFilesystemHelperRequest) ([]byte, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode release filesystem helper request: %w", err)
	}
	payload = append(payload, '\n')
	if len(payload) > releaseFilesystemHelperMaxRequestBytes {
		return nil, errors.New("release filesystem helper request exceeds byte budget")
	}
	return payload, nil
}

// executeReleaseFilesystemHelperProcess 启动一次 helper，并在所有路径同步 Wait 或返回启动错误。
func executeReleaseFilesystemHelperProcess(ctx context.Context, payload []byte) ([]byte, error) {
	cmd, err := defaultReleaseFilesystemHelperCommand(ctx)
	if err != nil {
		return nil, err
	}
	if cmd == nil {
		return nil, errors.New("release filesystem helper command is required")
	}
	cmd.Stdin = bytes.NewReader(payload)
	stdout := &releaseFilesystemLimitedBuffer{limit: releaseFilesystemHelperMaxOutputBytes}
	stderr := &releaseFilesystemLimitedBuffer{limit: releaseFilesystemHelperMaxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = releaseFilesystemHelperWaitDelay
	runErr := cmd.Run()
	if ctxErr := context.Cause(ctx); ctxErr != nil {
		if runErr != nil {
			return nil, errors.Join(ctxErr, fmt.Errorf("reap canceled release filesystem helper: %w", runErr))
		}
		return nil, ctxErr
	}
	if runErr != nil {
		return nil, releaseFilesystemHelperProcessError(runErr, stderr.String())
	}
	if stdout.overflowed() || stderr.overflowed() {
		return nil, errors.New("release filesystem helper output exceeds byte budget")
	}
	return stdout.Bytes(), nil
}

// validateReleaseFilesystemHelperResponse 校验响应身份、错误和结果形态。
func validateReleaseFilesystemHelperResponse(request releaseFilesystemHelperRequest, raw []byte) (string, error) {
	response, err := decodeReleaseFilesystemHelperResponse(raw)
	if err != nil {
		return "", err
	}
	if response.Version != request.Version || response.Operation != request.Operation {
		return "", errors.New("release filesystem helper response identity mismatch")
	}
	if response.Error != nil {
		return "", decodeReleaseFilesystemHelperError(*response.Error)
	}
	if err := validateReleaseFilesystemHelperValue(request.Operation, response.Value); err != nil {
		return "", err
	}
	return response.Value, nil
}

// defaultReleaseFilesystemHelperCommand 优先使用稳定暂存路径，否则使用当前可执行文件启动一次性 helper。
func defaultReleaseFilesystemHelperCommand(ctx context.Context) (*exec.Cmd, error) {
	executable := os.Getenv(releaseFilesystemHelperExecutableEnv)
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve release filesystem helper executable: %w", err)
		}
	}
	cmd := exec.CommandContext(ctx, executable)
	cmd.Env = append(os.Environ(), releaseFilesystemHelperEnv+"=1")
	return cmd, nil
}

// serveReleaseFilesystemHelper 读取并执行唯一一条 helper 请求。
func serveReleaseFilesystemHelper(reader io.Reader, writer io.Writer) error {
	request, err := decodeReleaseFilesystemHelperRequest(reader)
	if err != nil {
		return err
	}
	response := releaseFilesystemHelperResponse{Version: request.Version, Operation: request.Operation}
	response.Value, err = executeReleaseFilesystemHelperRequest(request)
	if err != nil {
		response.Value = ""
		response.Error = encodeReleaseFilesystemHelperError(err)
	}
	if err := json.NewEncoder(writer).Encode(response); err != nil {
		return fmt.Errorf("encode release filesystem helper response: %w", err)
	}
	return nil
}

// decodeReleaseFilesystemHelperRequest 严格解码并校验 helper 请求。
func decodeReleaseFilesystemHelperRequest(reader io.Reader) (releaseFilesystemHelperRequest, error) {
	var request releaseFilesystemHelperRequest
	if err := decodeReleaseFilesystemHelperJSON(reader, &request, releaseFilesystemHelperMaxRequestBytes); err != nil {
		return releaseFilesystemHelperRequest{}, fmt.Errorf("decode release filesystem helper request: %w", err)
	}
	if err := validateReleaseFilesystemHelperRequest(request); err != nil {
		return releaseFilesystemHelperRequest{}, err
	}
	return request, nil
}

// decodeReleaseFilesystemHelperResponse 严格解码 helper 响应。
func decodeReleaseFilesystemHelperResponse(raw []byte) (releaseFilesystemHelperResponse, error) {
	var response releaseFilesystemHelperResponse
	if err := decodeReleaseFilesystemHelperJSON(bytes.NewReader(raw), &response, releaseFilesystemHelperMaxOutputBytes); err != nil {
		return releaseFilesystemHelperResponse{}, fmt.Errorf("decode release filesystem helper response: %w", err)
	}
	if response.Error != nil && response.Value != "" {
		return releaseFilesystemHelperResponse{}, errors.New("release filesystem helper response contains both value and error")
	}
	if response.Error == nil && response.Value == "" {
		return releaseFilesystemHelperResponse{}, errors.New("release filesystem helper response contains neither value nor error")
	}
	return response, nil
}

// decodeReleaseFilesystemHelperJSON 在预算内解码唯一一个严格 JSON 值。
func decodeReleaseFilesystemHelperJSON(reader io.Reader, target any, limit int64) error {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit {
		return errors.New("release filesystem helper protocol exceeds byte budget")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("release filesystem helper protocol contains multiple values")
		}
		return err
	}
	return nil
}

// validateReleaseFilesystemHelperRequest 校验协议版本、操作和路径预算。
func validateReleaseFilesystemHelperRequest(request releaseFilesystemHelperRequest) error {
	if request.Version != releaseFilesystemHelperProtocolVersion {
		return fmt.Errorf("unsupported release filesystem helper protocol version %d", request.Version)
	}
	if request.Operation != releaseFilesystemOperationDigest &&
		request.Operation != releaseFilesystemOperationCanonicalExisting {
		return fmt.Errorf("unsupported release filesystem helper operation %q", request.Operation)
	}
	if request.Path == "" || len(request.Path) > releaseFilesystemHelperMaxPathBytes {
		return errors.New("release filesystem helper path is empty or exceeds byte budget")
	}
	return nil
}

// executeReleaseFilesystemHelperRequest 在可终止子进程内执行同步文件系统操作。
func executeReleaseFilesystemHelperRequest(request releaseFilesystemHelperRequest) (string, error) {
	switch request.Operation {
	case releaseFilesystemOperationDigest:
		// 子进程本身是可 kill/reap 的取消边界，内部同步 syscall 不再另起 goroutine。
		return computeReleaseDigestContextWithOps(context.Background(), request.Path, defaultReleaseDigestOps())
	case releaseFilesystemOperationCanonicalExisting:
		return canonicalExistingPath(request.Path)
	default:
		return "", fmt.Errorf("unsupported release filesystem helper operation %q", request.Operation)
	}
}

// validateReleaseFilesystemHelperValue 校验不同操作的成功结果。
func validateReleaseFilesystemHelperValue(operation, value string) error {
	switch operation {
	case releaseFilesystemOperationDigest:
		if !validLowerHex(value, sha256.Size*2) {
			return errors.New("release filesystem helper returned invalid digest")
		}
	case releaseFilesystemOperationCanonicalExisting:
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return errors.New("release filesystem helper returned non-canonical path")
		}
	default:
		return fmt.Errorf("unsupported release filesystem helper operation %q", operation)
	}
	return nil
}

// encodeReleaseFilesystemHelperError 将文件系统错误映射为稳定协议错误码。
func encodeReleaseFilesystemHelperError(err error) *releaseFilesystemHelperError {
	code := "filesystem"
	switch {
	case errors.Is(err, fs.ErrNotExist):
		code = "not_exist"
	case errors.Is(err, fs.ErrPermission):
		code = "permission"
	case errors.Is(err, fs.ErrInvalid):
		code = "invalid"
	}
	return &releaseFilesystemHelperError{Code: code, Message: err.Error()}
}

// decodeReleaseFilesystemHelperError 恢复稳定错误分类并拒绝未知错误码。
func decodeReleaseFilesystemHelperError(helperErr releaseFilesystemHelperError) error {
	if helperErr.Message == "" {
		return errors.New("release filesystem helper returned empty error message")
	}
	switch helperErr.Code {
	case "filesystem":
		return errors.New(helperErr.Message)
	case "not_exist":
		return fmt.Errorf("%s: %w", helperErr.Message, fs.ErrNotExist)
	case "permission":
		return fmt.Errorf("%s: %w", helperErr.Message, fs.ErrPermission)
	case "invalid":
		return fmt.Errorf("%s: %w", helperErr.Message, fs.ErrInvalid)
	default:
		return fmt.Errorf("release filesystem helper returned unknown error code %q", helperErr.Code)
	}
}

// releaseFilesystemHelperProcessError 合并 helper 退出状态与有界错误输出。
func releaseFilesystemHelperProcessError(runErr error, stderr string) error {
	message := strings.TrimSpace(stderr)
	if message == "" {
		return fmt.Errorf("release filesystem helper process failed: %w", runErr)
	}
	return fmt.Errorf("release filesystem helper process failed: %w: %s", runErr, message)
}

// Write 写入预算内内容，超出预算立即失败。
func (buffer *releaseFilesystemLimitedBuffer) Write(content []byte) (int, error) {
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 || len(content) > remaining {
		buffer.overflow = true
		return 0, errors.New("release filesystem helper output exceeds byte budget")
	}
	return buffer.buffer.Write(content)
}

// Bytes 返回已收集的原始字节。
func (buffer *releaseFilesystemLimitedBuffer) Bytes() []byte {
	return buffer.buffer.Bytes()
}

// String 返回已收集的文本。
func (buffer *releaseFilesystemLimitedBuffer) String() string {
	return buffer.buffer.String()
}

// overflowed 报告写入是否曾超过预算。
func (buffer *releaseFilesystemLimitedBuffer) overflowed() bool {
	return buffer.overflow
}
