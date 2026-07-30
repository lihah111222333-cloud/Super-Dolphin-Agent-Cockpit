// Package oss 通过 aliyun CLI 提供受前缀约束的 OSS 对象传输。
package oss

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxCLIAttempts    = 12
	initialRetryDelay = 500 * time.Millisecond
	maxRetryDelay     = 8 * time.Second
)

var (
	bucketPattern                  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	profilePattern                 = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	objectNotFoundPattern          = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(NoSuchKey|ObjectNotExist)(?:[^A-Za-z0-9]|$)`)
	sensitiveQueryParameterPattern = regexp.MustCompile(`(?i)((?:AccessKeyId|AccessKeySecret|Signature|SecurityToken)=)[^&#\s"'<>]+`)
)

// Config 描述 OSS CLI 调用所需的非敏感配置。
type Config struct {
	Binary   string
	Bucket   string
	Endpoint string
	Profile  string
	Prefix   string
}

// Metadata 是 OSS 对象的最小只读元数据。
type Metadata struct {
	ETag string
}

// CommandRunner 执行一个命令；stderr 必须与 stdout 分开返回。
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (stdout []byte, stderr []byte, err error)
}

// CommandError 保留 aliyun CLI 的 stderr，便于调用方诊断远端失败。
type CommandError struct {
	Operation string
	Stderr    string
	Err       error
}

// Error 返回不会丢失 stderr 的命令失败说明。
func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("oss %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("oss %s: %v: %s", e.Operation, e.Err, e.Stderr)
}

// Unwrap 暴露底层命令错误，以便调用方识别 context 取消或退出错误。
func (e *CommandError) Unwrap() error { return e.Err }

type client struct {
	config Config
	runner CommandRunner
	wait   func(context.Context, time.Duration) error
}

// New 严格校验配置并创建仅使用指定 profile 和 endpoint 的客户端。
func New(config Config, runner CommandRunner) (*client, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, errors.New("oss command runner must not be nil")
	}
	return &client{config: config, runner: runner, wait: waitForRetry}, nil
}

// NewCLI 使用系统 aliyun CLI；它不读取或保存 AccessKey。
func NewCLI(config Config) (*client, error) {
	return New(config, execRunner{})
}

// Upload 将本地文件上传到已限定前缀内的对象 key。
func (c *client) Upload(ctx context.Context, localPath string, key string) error {
	if strings.TrimSpace(localPath) == "" {
		return errors.New("oss upload source path must not be empty")
	}
	objectURL, err := c.objectURL(key)
	if err != nil {
		return err
	}
	return c.copy(ctx, "upload", localPath, objectURL)
}

// UploadDirectory 用单个 OSS 进程并行上传目录中的全部普通文件。
func (c *client) UploadDirectory(
	ctx context.Context,
	localPath string,
	prefix string,
	jobs int,
) (returnErr error) {
	if jobs <= 0 || jobs > 10000 {
		return errors.New("oss upload directory jobs must be between 1 and 10000")
	}
	if err := validateUploadDirectorySource(localPath); err != nil {
		return err
	}
	prefixURL, err := c.prefixURL(prefix)
	if err != nil {
		return err
	}
	checkpointRoot, err := os.MkdirTemp("", "super-dolphin-oss-checkpoint-*")
	if err != nil {
		return fmt.Errorf("create OSS checkpoint root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, os.RemoveAll(checkpointRoot))
	}()
	source := filepath.Clean(localPath) + string(filepath.Separator)
	_, returnErr = c.run(
		ctx,
		"upload directory",
		"oss",
		"cp",
		source,
		prefixURL,
		"--recursive",
		"--force",
		"--disable-ignore-error",
		"--disable-dir-object",
		"--disable-all-symlink",
		"--jobs",
		strconv.Itoa(jobs),
		"--checkpoint-dir",
		filepath.Join(checkpointRoot, "checkpoint"),
	)
	return returnErr
}

func validateUploadDirectorySource(localPath string) error {
	info, err := os.Lstat(localPath)
	if err != nil {
		return fmt.Errorf("inspect OSS upload directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("oss upload directory source must be a real directory")
	}
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return fmt.Errorf("read OSS upload directory: %w", err)
	}
	if len(entries) == 0 {
		return errors.New("oss upload directory must contain at least one file")
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return fmt.Errorf("oss upload directory entry %q is not a regular file", entry.Name())
		}
	}
	return nil
}

// Download 将已限定前缀内的对象下载到本地文件路径。
func (c *client) Download(ctx context.Context, key string, localPath string) error {
	if strings.TrimSpace(localPath) == "" {
		return errors.New("oss download destination path must not be empty")
	}
	objectURL, err := c.objectURL(key)
	if err != nil {
		return err
	}
	return c.copy(ctx, "download", objectURL, localPath)
}

// DownloadIfExists 下载对象；只有 OSS 的精确不存在错误被转换为 cache miss。
func (c *client) DownloadIfExists(ctx context.Context, key string, localPath string) (bool, error) {
	err := c.Download(ctx, key, localPath)
	if err == nil {
		return true, nil
	}
	if objectNotFoundPattern.MatchString(err.Error()) {
		return false, nil
	}
	return false, err
}

// List 返回受限子前缀内的全部对象 key；短格式之外的输出一律拒绝。
func (c *client) List(ctx context.Context, prefix string) ([]string, error) {
	prefixURL, err := c.prefixURL(prefix)
	if err != nil {
		return nil, err
	}
	stdout, err := c.run(ctx, "list", "oss", "ls", prefixURL, "--short-format", "--encoding-type", "url")
	if err != nil {
		return nil, err
	}
	return parseListOutput(stdout, c.config.Bucket, prefix)
}

// parseListOutput 严格解析 OSS 短格式列表并拒绝越界或重复对象。
func parseListOutput(stdout []byte, bucket string, prefix string) ([]string, error) {
	const maxListingBytes = 64 << 20
	if len(stdout) > maxListingBytes {
		return nil, errors.New("oss list response exceeds size limit")
	}
	objectPrefix := "oss://" + bucket + "/"
	seen := make(map[string]struct{})
	var keys []string
	for rawLine := range strings.SplitSeq(string(stdout), "\n") {
		key, skip, err := parseListLine(rawLine, objectPrefix, prefix)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("oss list returned duplicate object key %q", key)
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func parseListLine(rawLine string, objectPrefix string, prefix string) (string, bool, error) {
	line := strings.TrimSpace(rawLine)
	if isListSummaryLine(line) {
		return "", true, nil
	}
	if !strings.HasPrefix(line, objectPrefix) {
		return "", false, fmt.Errorf("oss list returned an unsupported line %q", line)
	}
	key, err := url.PathUnescape(strings.TrimPrefix(line, objectPrefix))
	if err != nil {
		return "", false, errors.New("oss list returned an invalid object key")
	}
	if err := validateKey(prefix, key); err != nil {
		return "", false, errors.New("oss list returned an invalid object key")
	}
	return key, false, nil
}

func isListSummaryLine(line string) bool {
	return line == "" || strings.HasPrefix(line, "Object Number is:") || strings.HasSuffix(line, " elapsed")
}

// Metadata 读取对象 metadata 并返回 ETag。
func (c *client) Metadata(ctx context.Context, key string) (Metadata, error) {
	objectURL, err := c.objectURL(key)
	if err != nil {
		return Metadata{}, err
	}
	stdout, err := c.run(ctx, "stat", "oss", "stat", objectURL)
	if err != nil {
		return Metadata{}, err
	}
	var payload struct {
		ETag string `json:"ETag"`
	}
	if err := json.Unmarshal(stdout, &payload); err != nil {
		return Metadata{}, fmt.Errorf("decode oss metadata: %w", err)
	}
	if strings.TrimSpace(payload.ETag) == "" {
		return Metadata{}, errors.New("oss metadata response has no ETag")
	}
	return Metadata{ETag: payload.ETag}, nil
}

// Delete 删除已限定前缀内的对象。
func (c *client) Delete(ctx context.Context, key string) error {
	objectURL, err := c.objectURL(key)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, "delete", "oss", "rm", objectURL)
	return err
}

// EnsurePrefix 创建 OSSFS 挂载空子目录所需的目录标记。
func (c *client) EnsurePrefix(ctx context.Context, prefix string) error {
	prefixURL, err := c.prefixURL(prefix)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, "create prefix", "oss", "mkdir", prefixURL)
	return err
}

// DeletePrefix 递归删除一个完整 generation，禁止删除客户端配置的根前缀。
func (c *client) DeletePrefix(ctx context.Context, prefix string) error {
	objectURL, err := c.prefixURL(prefix)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, "delete prefix", "oss", "rm", objectURL, "--recursive", "--force")
	return err
}

func (c *client) copy(ctx context.Context, operation string, source string, destination string) (returnErr error) {
	if destination == "" {
		return errors.New("oss object destination must not be empty")
	}
	checkpointRoot, err := os.MkdirTemp("", "super-dolphin-oss-checkpoint-*")
	if err != nil {
		return fmt.Errorf("create OSS checkpoint root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, os.RemoveAll(checkpointRoot))
	}()
	_, returnErr = c.run(
		ctx, operation, "oss", "cp", source, destination,
		"--checkpoint-dir", filepath.Join(checkpointRoot, "checkpoint"),
	)
	return returnErr
}

func (c *client) objectURL(key string) (string, error) {
	if err := validateKey(c.config.Prefix, key); err != nil {
		return "", err
	}
	return "oss://" + c.config.Bucket + "/" + key, nil
}

func (c *client) prefixURL(prefix string) (string, error) {
	if prefix == c.config.Prefix || !strings.HasSuffix(prefix, "/") {
		return "", errors.New("OSS prefix must be a child generation")
	}
	key := strings.TrimSuffix(prefix, "/")
	if err := validateKey(c.config.Prefix, key); err != nil {
		return "", err
	}
	return "oss://" + c.config.Bucket + "/" + prefix, nil
}

// run 执行有界且可取消的 OSS CLI 瞬态错误重试序列。
func (c *client) run(ctx context.Context, operation string, args ...string) ([]byte, error) {
	commandArgs := append(append([]string(nil), args...), "--profile", c.config.Profile, "--endpoint", c.config.Endpoint)
	for attempt := 1; attempt <= maxCLIAttempts; attempt++ {
		attemptArgs := append([]string(nil), commandArgs...)
		stdout, stderr, err := c.runner.Run(ctx, c.config.Binary, attemptArgs...)
		if err == nil {
			return stdout, nil
		}
		safeStderr := redactSensitiveCLIText(strings.TrimSpace(string(stderr)))
		if !isTransientCLIError(err, safeStderr) || attempt == maxCLIAttempts {
			return nil, &CommandError{Operation: operation, Stderr: safeStderr, Err: err}
		}
		if err := c.wait(ctx, retryDelay(attempt)); err != nil {
			return nil, &CommandError{Operation: operation, Err: fmt.Errorf("retry wait: %w", err)}
		}
	}
	return nil, &CommandError{Operation: operation, Err: errors.New("retry attempts exhausted")}
}

// retryDelay 返回尝试之间的指数退避，并将单次等待限制在上限内。
func retryDelay(attempt int) time.Duration {
	delay := initialRetryDelay * time.Duration(1<<(attempt-1))
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

// waitForRetry 在 OSS CLI 退避期间保持 context 可取消。
func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// isTransientCLIError 仅识别网络、DNS 和传输层瞬态错误。
func isTransientCLIError(err error, stderr string) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error() + " " + stderr)
	for _, fragment := range []string{
		"tls handshake timeout", "i/o timeout", "unexpected eof", ": eof", " eof",
		"context deadline exceeded", "client.timeout exceeded", "connection reset",
		"temporary failure in name resolution", "no such host",
		"throttling.user", "user flow control",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

// redactSensitiveCLIText 清除 Aliyun CLI 错误 URL 中的凭据和签名值。
func redactSensitiveCLIText(value string) string {
	return sensitiveQueryParameterPattern.ReplaceAllString(value, `${1}<redacted>`)
}

// validateConfig 拒绝会改变 CLI 鉴权来源、目标端点或对象边界的配置。
func validateConfig(config Config) error {
	if strings.TrimSpace(config.Binary) == "" {
		return errors.New("oss CLI binary must not be empty")
	}
	if !bucketPattern.MatchString(config.Bucket) {
		return fmt.Errorf("invalid oss bucket %q", config.Bucket)
	}
	if !profilePattern.MatchString(config.Profile) {
		return fmt.Errorf("invalid aliyun profile %q", config.Profile)
	}
	if err := validateEndpoint(config.Endpoint); err != nil {
		return err
	}
	if err := validatePrefix(config.Prefix); err != nil {
		return err
	}
	return nil
}

// validateEndpoint 仅允许无附加路径和身份信息的 HTTPS OSS API 端点。
func validateEndpoint(value string) error {
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return fmt.Errorf("invalid OSS HTTPS endpoint %q", value)
	}
	return nil
}

func validatePrefix(prefix string) error {
	if strings.TrimSpace(prefix) == "" || !strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("invalid OSS prefix %q", prefix)
	}
	if err := validatePath(prefix[:len(prefix)-1]); err != nil {
		return fmt.Errorf("invalid OSS prefix %q: %w", prefix, err)
	}
	return nil
}

func validateKey(prefix string, key string) error {
	if err := validatePath(key); err != nil {
		return fmt.Errorf("invalid OSS object key %q: %w", key, err)
	}
	if !strings.HasPrefix(key, prefix) {
		return fmt.Errorf("OSS object key %q is outside configured prefix %q", key, prefix)
	}
	return nil
}

// validatePath 拒绝绝对路径、反斜杠和任何可逃逸 OSS 前缀的相对路径。
func validatePath(value string) error {
	if strings.TrimSpace(value) == "" || value == ".." || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || path.Clean(value) != value {
		return errors.New("must be a clean relative slash-separated path")
	}
	return nil
}

type execRunner struct{}

// Run 在调用方提供的 context 内执行 CLI，并分别捕获 stdout 和 stderr。
func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
