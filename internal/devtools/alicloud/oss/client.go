// Package oss 通过 aliyun CLI 提供受前缀约束的 OSS 对象传输。
package oss

import (
	"bytes"
	"context"
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
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const (
	maxCLIAttempts      = 12
	cliAttemptTimeout   = 15 * time.Second
	cliProcessWaitDelay = time.Second
	initialRetryDelay   = 500 * time.Millisecond
	maxRetryDelay       = 8 * time.Second
)

var (
	bucketPattern                  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	profilePattern                 = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	objectConflictPattern          = regexp.MustCompile(`(?i)(?:FileAlreadyExists|PreconditionFailed|(?:HTTP|status)[ :=]+(?:409|412)|\b(?:409|412)\b)`)
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
	config         Config
	runner         CommandRunner
	wait           func(context.Context, time.Duration) error
	attemptTimeout time.Duration
}

// New 严格校验配置并创建仅使用指定 profile 和 endpoint 的客户端。
func New(config Config, runner CommandRunner) (*client, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, errors.New("oss command runner must not be nil")
	}
	return &client{config: config, runner: runner, wait: waitForRetry, attemptTimeout: cliAttemptTimeout}, nil
}

// NewCLI 使用系统 aliyun CLI；它不读取或保存 AccessKey。
func NewCLI(config Config) (*client, error) {
	return New(config, execRunner{})
}

// Create 将本地文件创建为已限定前缀内的全新对象。
// 同名对象必须由 OSS 原子拒绝，绝不允许覆盖。
func (c *client) Create(ctx context.Context, localPath string, key string) error {
	if strings.TrimSpace(localPath) == "" {
		return errors.New("oss create source path must not be empty")
	}
	objectURL, err := c.objectURL(key)
	if err != nil {
		return err
	}
	return c.copy(ctx, "create", localPath, objectURL, "--meta", "x-oss-forbid-overwrite:true")
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

func (c *client) copy(ctx context.Context, operation string, source string, destination string, extraArgs ...string) (returnErr error) {
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
	args := append([]string{"oss", "cp", source, destination}, extraArgs...)
	args = append(args, "--checkpoint-dir", filepath.Join(checkpointRoot, "checkpoint"))
	_, returnErr = c.run(ctx, operation, args...)
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
		if err := ctx.Err(); err != nil {
			return nil, &CommandError{Operation: operation, Err: err}
		}
		attemptArgs := append([]string(nil), commandArgs...)
		attemptContext, cancel := gateprivate.WithTimeout(ctx, c.attemptTimeout)
		stdout, stderr, err := c.runner.Run(attemptContext, c.config.Binary, attemptArgs...)
		cancel()
		if err == nil {
			return stdout, nil
		}
		safeStderr := redactSensitiveCLIText(strings.TrimSpace(string(stderr)))
		if operation == "create" && objectConflictPattern.MatchString(safeStderr) {
			return nil, &CommandError{Operation: operation, Stderr: safeStderr, Err: err}
		}
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
	// CLI 后代若在父进程退出后仍持有 stdout/stderr 管道，CommandContext 默认的
	// Wait 会越过 context 无界等待。固定 WaitDelay 使每次 OSS 控制面调用真正受到看门狗约束。
	command.WaitDelay = cliProcessWaitDelay
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	err = preserveCommandContextError(ctx, err)
	return stdout.Bytes(), stderr.Bytes(), err
}

// preserveCommandContextError 保留 CommandContext 杀进程时被退出状态遮蔽的超时或取消原因。
func preserveCommandContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return errors.Join(err, contextErr)
	}
	return err
}
