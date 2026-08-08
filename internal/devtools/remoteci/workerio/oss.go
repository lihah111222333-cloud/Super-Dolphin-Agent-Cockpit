// Package workerio transfers remote CI objects with an ECI RAM role.
package workerio

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const (
	defaultMetadataBaseURL = "http://100.100.100.200"
	metadataTokenTTL       = 21600
	metadataBodyLimit      = 64 << 10
	errorBodyLimit         = 4 << 10
	maxObjectBytes         = 1 << 30
	remoteHTTPTimeout      = 15 * time.Second
	maxDownloadAttempts    = 12
	initialRetryDelay      = 500 * time.Millisecond
	maxRetryDelay          = 8 * time.Second
)

// Config identifies the RAM role and OSS object a worker is allowed to download.
type Config struct {
	RoleName string
	Endpoint string
	Bucket   string
	Key      string
	MaxBytes int64
}

// Dependencies supplies transport and time dependencies for Client.
type Dependencies struct {
	HTTPClient      *http.Client
	MetadataBaseURL string
	Clock           func() time.Time
}

// Client downloads one explicitly configured OSS object using ECI RAM credentials.
type Client struct {
	config             Config
	metadataHTTPClient *http.Client
	ossHTTPClient      *http.Client
	metadataBaseURL    *url.URL
	clock              func() time.Time
	wait               func(context.Context, time.Duration) error
}

type temporaryCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	AccessKeySecret string `json:"AccessKeySecret"`
	SecurityToken   string `json:"SecurityToken"`
	Expiration      string `json:"Expiration"`
	Code            string `json:"Code"`
	LastUpdated     string `json:"LastUpdated"`
}

// NewClient 校验显式对象配置并创建使用 RAM 角色的 OSS 客户端。
func NewClient(config Config, dependencies Dependencies) (*Client, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	metadataURL, err := parseMetadataBaseURL(dependencies.MetadataBaseURL)
	if err != nil {
		return nil, err
	}
	metadataClient := noRedirectClient(dependencies.HTTPClient, remoteHTTPTimeout)
	ossClient := noRedirectClient(dependencies.HTTPClient, 0)
	ossClient.Transport = boundedOSSTransport(ossClient.Transport)
	return &Client{
		config:             config,
		metadataHTTPClient: metadataClient,
		ossHTTPClient:      ossClient,
		metadataBaseURL:    metadataURL,
		clock:              clientClock(dependencies.Clock),
		wait:               waitForRetry,
	}, nil
}

// parseMetadataBaseURL 校验并规范化 ECI IMDS 根地址。
func parseMetadataBaseURL(value string) (*url.URL, error) {
	if value == "" {
		value = defaultMetadataBaseURL
	}
	metadataURL, err := url.Parse(value)
	if err != nil || !validMetadataBaseURL(metadataURL) {
		return nil, errors.New("metadata base URL must be an absolute HTTP URL without credentials, query, or fragment")
	}
	if metadataURL.Path != "" && metadataURL.Path != "/" {
		return nil, errors.New("metadata base URL must not contain a path")
	}
	metadataURL.Path, metadataURL.RawPath = "", ""
	return metadataURL, nil
}

// validMetadataBaseURL 判断 IMDS 地址是否不携带可注入的 URL 组成部分。
func validMetadataBaseURL(value *url.URL) bool {
	return value != nil && value.Scheme == "http" && value.Host != "" && value.User == nil && value.RawQuery == "" && value.Fragment == ""
}

// noRedirectClient 复制调用方 HTTP 客户端、保留注入 transport 并拒绝跨地址重定向。
func noRedirectClient(client *http.Client, timeout time.Duration) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if timeout == 0 {
		copy.Timeout = 0
	} else if copy.Timeout == 0 || copy.Timeout > timeout {
		copy.Timeout = timeout
	}
	return &copy
}

// boundedOSSTransport 限制连接建立和响应头等待，不中断活动对象正文。
func boundedOSSTransport(roundTripper http.RoundTripper) http.RoundTripper {
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}
	transport, ok := roundTripper.(*http.Transport)
	if !ok {
		return roundTripper
	}
	copy := transport.Clone()
	if copy.TLSHandshakeTimeout == 0 || copy.TLSHandshakeTimeout > remoteHTTPTimeout {
		copy.TLSHandshakeTimeout = remoteHTTPTimeout
	}
	if copy.ResponseHeaderTimeout == 0 || copy.ResponseHeaderTimeout > remoteHTTPTimeout {
		copy.ResponseHeaderTimeout = remoteHTTPTimeout
	}
	dial := copy.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	copy.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		dialCtx, cancel := gateprivate.WithTimeout(ctx, remoteHTTPTimeout)
		defer cancel()
		return dial(dialCtx, network, address)
	}
	return copy
}

// clientClock 返回注入时钟，未注入时使用系统时钟。
func clientClock(clock func() time.Time) func() time.Time {
	if clock == nil {
		return time.Now
	}
	return clock
}

// Download 将已绑定对象下载到目标写入器并返回字节数。
func (client *Client) Download(ctx context.Context, destination io.Writer) (int64, error) {
	if destination == nil {
		return 0, errors.New("download destination must not be nil")
	}
	var lastErr error
	var attempts int
	for attempt := 1; attempt <= maxDownloadAttempts; attempt++ {
		attempts = attempt
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("OSS download canceled before attempt %d: %w", attempt, err)
		}
		size, err := client.downloadAttempt(ctx, destination)
		if err == nil {
			return size, nil
		}
		if parentErr := ctx.Err(); parentErr != nil {
			return 0, fmt.Errorf("OSS download parent context ended during attempt %d: %w", attempt, parentErr)
		}
		lastErr = err
		if !retryableDownloadError(ctx, err) || attempt == maxDownloadAttempts {
			break
		}
		if err := client.wait(ctx, retryDelay(attempt)); err != nil {
			return 0, fmt.Errorf("OSS download retry wait after attempt %d: %w", attempt, err)
		}
	}
	return 0, fmt.Errorf("OSS download failed after %d attempt(s): %w", attempts, lastErr)
}

// downloadAttempt 在暴露字节前暂存对象，避免重试追加部分对象。
func (client *Client) downloadAttempt(ctx context.Context, destination io.Writer) (returnSize int64, returnErr error) {
	temporary, err := os.CreateTemp("", ".super-dolphin-oss-")
	if err != nil {
		return 0, fmt.Errorf("create OSS download staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		closeErr := temporary.Close()
		removeErr := os.Remove(temporaryPath)
		if returnErr == nil && (closeErr != nil || removeErr != nil) {
			returnSize = 0
			returnErr = fmt.Errorf("clean OSS download staging file: %w", errors.Join(closeErr, removeErr))
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return 0, fmt.Errorf("secure OSS download staging file: %w", err)
	}
	credentials, err := client.currentCredentials(ctx)
	if err != nil {
		return 0, err
	}
	size, err := client.downloadObject(ctx, credentials, temporary)
	if err != nil {
		return 0, err
	}
	return copyStagedObject(temporary, destination, client.config.MaxBytes, size)
}

func copyStagedObject(source *os.File, destination io.Writer, maxBytes, size int64) (int64, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("rewind OSS download staging file: %w", err)
	}
	copied, err := io.Copy(destination, io.LimitReader(source, maxBytes))
	if err != nil {
		return 0, fmt.Errorf("write downloaded OSS object: %w", err)
	}
	if copied != size {
		return 0, fmt.Errorf("staged OSS object size %d does not match copied size %d", size, copied)
	}
	return size, nil
}

func (client *Client) currentCredentials(ctx context.Context) (temporaryCredentials, error) {
	return readCurrentCredentials(ctx, client.config.RoleName, client.metadataHTTPClient, client.metadataBaseURL, client.clock)
}

// retryDelay 返回尝试之间的指数退避，并将单次等待限制在上限内。
func retryDelay(attempt int) time.Duration {
	delay := initialRetryDelay * time.Duration(1<<(attempt-1))
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

// waitForRetry 在下载退避期间保持父级 context 可取消。
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

// retryableDownloadError 仅允许网络、超时和服务端暂态错误触发重试。
func retryableDownloadError(parent context.Context, err error) bool {
	if parent.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if retryableHTTPStatus(err) {
		return true
	}
	if permanentCertificateError(err) {
		return false
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		err = urlErr.Err
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return true
	}
	_, ok := errors.AsType[tls.RecordHeaderError](err)
	return ok
}

func retryableHTTPStatus(err error) bool {
	statusErr, ok := errors.AsType[*retryableHTTPStatusError](err)
	return ok && (statusErr.status == http.StatusRequestTimeout || statusErr.status == http.StatusTooManyRequests || statusErr.status >= http.StatusInternalServerError)
}

// permanentCertificateError 识别重试无法修复的证书链和主机名校验失败。
func permanentCertificateError(err error) bool {
	var certificateInvalidError x509.CertificateInvalidError
	var hostnameError x509.HostnameError
	var unknownAuthorityError x509.UnknownAuthorityError
	var systemRootsError x509.SystemRootsError
	return errors.As(err, &certificateInvalidError) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &unknownAuthorityError) ||
		errors.As(err, &systemRootsError)
}

type retryableHTTPStatusError struct{ status int }

// Error 返回可供重试策略识别的 HTTP 状态描述。
func (err *retryableHTTPStatusError) Error() string { return fmt.Sprintf("HTTP %d", err.status) }

// metadataToken 向 IMDSv2 请求限定 TTL 的访问令牌。
func readCurrentCredentials(ctx context.Context, roleName string, metadataHTTPClient *http.Client, metadataBaseURL *url.URL, clock func() time.Time) (temporaryCredentials, error) {
	token, err := metadataToken(ctx, metadataHTTPClient, metadataBaseURL)
	if err != nil {
		return temporaryCredentials{}, err
	}
	credentials, err := metadataCredentials(ctx, roleName, token, metadataHTTPClient, metadataBaseURL)
	if err != nil {
		return temporaryCredentials{}, err
	}
	if err := validateCredentials(credentials, clock()); err != nil {
		return temporaryCredentials{}, err
	}
	return credentials, nil
}

// metadataToken 向 IMDSv2 请求限定 TTL 的访问令牌。
func metadataToken(ctx context.Context, metadataHTTPClient *http.Client, metadataBaseURL *url.URL) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, metadataBaseURL.String()+"/latest/api/token", nil)
	if err != nil {
		return "", fmt.Errorf("create metadata token request: %w", err)
	}
	request.Header.Set("X-aliyun-ecs-metadata-token-ttl-seconds", fmt.Sprintf("%d", metadataTokenTTL))
	response, err := metadataHTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request metadata token: %w", err)
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		discardResponse(response.Body, errorBodyLimit)
		return "", fmt.Errorf("metadata token returned: %w", &retryableHTTPStatusError{status: response.StatusCode})
	}
	body, err := readBounded(response.Body, metadataBodyLimit)
	if err != nil {
		return "", fmt.Errorf("read metadata token: %w", err)
	}
	token := strings.TrimSpace(string(body))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("metadata token is invalid")
	}
	return token, nil
}

func metadataCredentials(ctx context.Context, roleName string, token string, metadataHTTPClient *http.Client, metadataBaseURL *url.URL) (temporaryCredentials, error) {
	requestURL := metadataBaseURL.String() + "/latest/meta-data/ram/security-credentials/" + url.PathEscape(roleName)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return temporaryCredentials{}, fmt.Errorf("create RAM credentials request: %w", err)
	}
	request.Header.Set("X-aliyun-ecs-metadata-token", token)
	response, err := metadataHTTPClient.Do(request)
	if err != nil {
		return temporaryCredentials{}, fmt.Errorf("request RAM credentials: %w", err)
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		discardResponse(response.Body, errorBodyLimit)
		return temporaryCredentials{}, fmt.Errorf("RAM credentials returned: %w", &retryableHTTPStatusError{status: response.StatusCode})
	}
	var credentials temporaryCredentials
	if err := decodeJSONBounded(response.Body, metadataBodyLimit, &credentials); err != nil {
		return temporaryCredentials{}, errors.New("RAM credentials response is invalid")
	}
	return credentials, nil
}

// downloadObject 使用临时 RAM 凭据签名并下载已绑定的 OSS 对象。
func (client *Client) downloadObject(ctx context.Context, credentials temporaryCredentials, destination io.Writer) (int64, error) {
	endpoint, _ := url.Parse(client.config.Endpoint)
	objectURL := *endpoint
	if strings.HasSuffix(endpoint.Hostname(), ".aliyuncs.com") {
		objectURL.Host = client.config.Bucket + "." + endpoint.Host
		objectURL.Path = "/" + client.config.Key
	} else {
		objectURL.Path = "/" + client.config.Bucket + "/" + client.config.Key
	}
	objectURL.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, objectURL.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("create OSS download request: %w", err)
	}
	date := client.clock().UTC().Format(http.TimeFormat)
	request.Header.Set("Date", date)
	request.Header.Set("x-oss-security-token", credentials.SecurityToken)
	resource := request.URL.EscapedPath()
	if strings.HasSuffix(endpoint.Hostname(), ".aliyuncs.com") {
		resource = "/" + client.config.Bucket + resource
	}
	request.Header.Set("Authorization", ossAuthorization(request.Method, date, resource, credentials))
	response, err := client.ossHTTPClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("request OSS object: %w", err)
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		discardResponse(response.Body, errorBodyLimit)
		return 0, fmt.Errorf("OSS object returned: %w", &retryableHTTPStatusError{status: response.StatusCode})
	}
	if response.ContentLength > client.config.MaxBytes {
		return 0, errors.New("OSS object exceeds configured maximum size")
	}
	limited := &limitedWriter{destination: destination, remaining: client.config.MaxBytes}
	_, err = io.Copy(limited, response.Body)
	if err != nil {
		return limited.written, fmt.Errorf("download OSS object: %w", err)
	}
	return limited.written, nil
}

// validateConfig 校验 RAM 角色、OSS 端点和对象下载容量边界。
func validateConfig(config Config) error {
	if !validRoleName(config.RoleName) {
		return errors.New("RAM role name is invalid")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || !validOSSEndpoint(endpoint) {
		return errors.New("OSS endpoint must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return errors.New("OSS endpoint must not contain a path")
	}
	if !validBucket(config.Bucket) {
		return errors.New("OSS bucket is invalid")
	}
	if !validKey(config.Key) {
		return errors.New("OSS object key is invalid")
	}
	if config.MaxBytes <= 0 || config.MaxBytes > maxObjectBytes {
		return fmt.Errorf("maximum object size must be between 1 and %d bytes", maxObjectBytes)
	}
	return nil
}

// validOSSEndpoint 判断 OSS 端点是否为无身份信息的 HTTPS 根地址。
func validOSSEndpoint(value *url.URL) bool {
	return value != nil && value.Scheme == "https" && value.Host != "" && value.User == nil && value.RawQuery == "" && value.Fragment == ""
}

// validateCredentials 校验 IMDS 返回的临时凭据在当前时刻仍可用。
func validateCredentials(credentials temporaryCredentials, now time.Time) error {
	if credentials.Code != "Success" {
		return errors.New("RAM credentials response did not succeed")
	}
	if !validSecretValue(credentials.AccessKeyID) || !validSecretValue(credentials.AccessKeySecret) || !validSecretValue(credentials.SecurityToken) {
		return errors.New("RAM credentials response is incomplete")
	}
	expiration, err := time.Parse(time.RFC3339, credentials.Expiration)
	if err != nil || !expiration.After(now) {
		return errors.New("RAM credentials are expired or invalid")
	}
	return nil
}

func ossAuthorization(method string, date string, resource string, credentials temporaryCredentials) string {
	return ossAuthorizationWithHeaders(method, date, resource, credentials, "")
}

func ossAuthorizationWithHeaders(method string, date string, resource string, credentials temporaryCredentials, extraCanonicalHeaders string) string {
	canonicalHeaders := extraCanonicalHeaders + "x-oss-security-token:" + credentials.SecurityToken + "\n"
	stringToSign := method + "\n\n\n" + date + "\n" + canonicalHeaders + resource
	mac := hmac.New(sha1.New, []byte(credentials.AccessKeySecret))
	_, _ = mac.Write([]byte(stringToSign))
	return "OSS " + credentials.AccessKeyID + ":" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func decodeJSONBounded(reader io.Reader, limit int64, target any) error {
	body, err := readBounded(reader, limit)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON payload")
	}
	return nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("response body exceeds limit")
	}
	return body, nil
}

func discardResponse(reader io.Reader, limit int64) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, limit))
}

func isSuccess(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

// validRoleName 校验 RAM 角色名称字符和首字符约束。
func validRoleName(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if !validRoleCharacter(index, character) {
			return false
		}
	}
	return true
}

// validRoleCharacter 判断 RAM 角色名中的单个字符是否处于允许位置。
func validRoleCharacter(index int, character rune) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '_'))
}

// validBucket 校验 OSS bucket 名称字符和连字符位置。
func validBucket(value string) bool {
	if len(value) < 3 || len(value) > 63 {
		return false
	}
	for index, character := range value {
		if !validBucketCharacter(index, len(value), character) {
			return false
		}
	}
	return true
}

// validBucketCharacter 判断 bucket 字符是否符合位置约束。
func validBucketCharacter(index int, length int, character rune) bool {
	return (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || (index > 0 && index < length-1 && character == '-')
}

func validKey(value string) bool {
	if value == "" || len(value) > 1023 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n?#")
}

func validSecretValue(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\x00\r\n")
}

type limitedWriter struct {
	destination io.Writer
	remaining   int64
	written     int64
}

// Write 在写入目标前强制执行对象大小上限。
func (writer *limitedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.remaining {
		return 0, errors.New("OSS object exceeds configured maximum size")
	}
	written, err := writer.destination.Write(data)
	writer.written += int64(written)
	writer.remaining -= int64(written)
	return written, err
}
