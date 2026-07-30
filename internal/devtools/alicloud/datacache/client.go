// Package datacache provides a narrow, credential-free adapter for Aliyun ECI DataCache APIs.
package datacache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	StatusAvailable    = "Available"
	StatusDeleting     = "Deleting"
	StatusFailed       = "Failed"
	StatusUpdateFailed = "UpdateFailed"
	maxCLIAttempts     = 12
	initialRetryDelay  = 500 * time.Millisecond
	maxRetryDelay      = 8 * time.Second
)

// Config identifies the Aliyun CLI profile and fixed network used to materialize DataCaches.
type Config struct {
	Binary          string
	RegionID        string
	VSwitchID       string
	SecurityGroupID string
	Profile         string
}

// OSSDataSource identifies one same-region OSS prefix copied into a DataCache snapshot.
type OSSDataSource struct {
	Bucket   string
	Endpoint string
	Path     string
	RoleName string
}

// CreateRequest binds one immutable DataCache generation to an OSS source prefix.
type CreateRequest struct {
	Name          string
	Bucket        string
	Path          string
	SizeGiB       int
	RetentionDays int
	ClientToken   string
	Source        OSSDataSource
	Tags          map[string]string
}

// DataCache is the subset of ECI state required by the baseline refresh state machine.
type DataCache struct {
	ID      string `json:"DataCacheId"`
	Name    string `json:"Name"`
	Status  string `json:"Status"`
	Bucket  string `json:"Bucket"`
	Path    string `json:"Path"`
	SizeGiB int    `json:"Size"`
}

// CommandRunner executes one external command and permits deterministic unit tests.
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// Client invokes ECI DataCache actions through an already configured Aliyun CLI profile.
type Client struct {
	config Config
	runner CommandRunner
	wait   func(context.Context, time.Duration) error
}

// New 创建 DataCache CLI 客户端，访问凭据仍由本机 Aliyun profile 管理。
func New(config Config) (*Client, error) {
	return NewWithRunner(config, execRunner{})
}

// NewWithRunner 注入命令执行边界，使测试可以校验完整的 OpenAPI 参数。
func NewWithRunner(config Config, runner CommandRunner) (*Client, error) {
	for name, value := range map[string]string{
		"CLI binary": config.Binary, "region ID": config.RegionID, "vSwitch ID": config.VSwitchID,
		"security group ID": config.SecurityGroupID, "profile": config.Profile,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("DataCache %s is required", name)
		}
	}
	if runner == nil {
		return nil, errors.New("DataCache command runner is required")
	}
	return &Client{config: config, runner: runner, wait: waitForRetry}, nil
}

// Create 从受 RAM 角色保护的 OSS 前缀创建一个有期限的不可变 DataCache。
func (client *Client) Create(ctx context.Context, request CreateRequest) (DataCache, error) {
	if err := validateCreateRequest(request); err != nil {
		return DataCache{}, err
	}
	args := []string{
		"--VSwitchId", client.config.VSwitchID,
		"--SecurityGroupId", client.config.SecurityGroupID,
		"--Bucket", request.Bucket,
		"--Path", request.Path,
		"--Name", request.Name,
		"--Size", strconv.Itoa(request.SizeGiB),
		"--RetentionDays", strconv.Itoa(request.RetentionDays),
		"--ClientToken", request.ClientToken,
		"--DataSource.Type", "OSS",
		"--DataSource.Options.#6#bucket", request.Source.Bucket,
		"--DataSource.Options.#3#url", request.Source.Endpoint,
		"--DataSource.Options.#4#path", request.Source.Path,
		"--DataSource.Options.#7#ramRole", request.Source.RoleName,
	}
	args = appendTags(args, request.Tags)
	args = append(args, "--method", "POST", "--force")
	output, err := client.run(ctx, "CreateDataCache", args...)
	if err != nil {
		return DataCache{}, fmt.Errorf("create DataCache: %w", err)
	}
	var response struct {
		DataCacheID string `json:"DataCacheId"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return DataCache{}, fmt.Errorf("decode CreateDataCache response: %w", err)
	}
	if strings.TrimSpace(response.DataCacheID) == "" {
		return DataCache{}, errors.New("CreateDataCache response is missing DataCacheId")
	}
	return DataCache{ID: response.DataCacheID, Name: request.Name, Bucket: request.Bucket, Path: request.Path, SizeGiB: request.SizeGiB}, nil
}

// Describe 查询指定 DataCache；删除后不存在会返回空集合，其余项必须具备完整身份。
func (client *Client) Describe(ctx context.Context, ids ...string) ([]DataCache, error) {
	args, err := describeArgs(ids)
	if err != nil {
		return nil, err
	}
	output, err := client.run(ctx, "DescribeDataCaches", args...)
	if err != nil {
		return nil, fmt.Errorf("describe DataCaches: %w", err)
	}
	var response struct {
		DataCaches []DataCache `json:"DataCaches"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return nil, fmt.Errorf("decode DescribeDataCaches response: %w", err)
	}
	if err := validateDescribeResponse(response.DataCaches); err != nil {
		return nil, err
	}
	return response.DataCaches, nil
}

// FindByPath 通过存储身份和标签精确查找未记录 ID 的 DataCache。
func (client *Client) FindByPath(
	ctx context.Context,
	bucket string,
	cachePath string,
	tags map[string]string,
) ([]DataCache, error) {
	if err := validateFindByPathRequest(bucket, cachePath, tags); err != nil {
		return nil, err
	}
	args := []string{"--Bucket", bucket, "--Path", cachePath}
	args = appendTags(args, tags)
	args = append(args, "--Limit", "20")
	output, err := client.run(ctx, "DescribeDataCaches", args...)
	if err != nil {
		return nil, fmt.Errorf("find DataCaches by path: %w", err)
	}
	var response struct {
		DataCaches []DataCache `json:"DataCaches"`
		NextToken  string      `json:"NextToken"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return nil, fmt.Errorf("decode DescribeDataCaches path response: %w", err)
	}
	if err := validateFindByPathResponse(response.DataCaches, response.NextToken, bucket, cachePath); err != nil {
		return nil, err
	}
	return response.DataCaches, nil
}

// validateFindByPathRequest 拒绝无标签或非规范存储身份的宽泛查询。
func validateFindByPathRequest(bucket string, cachePath string, tags map[string]string) error {
	if !validBucket(bucket) || !validAbsolutePath(cachePath) || len(tags) == 0 {
		return errors.New("DataCache path query identity is invalid")
	}
	return validateDataCacheTags(tags)
}

// validateFindByPathResponse 拒绝分页、字段缺失或偏离请求路径的查询结果。
func validateFindByPathResponse(
	caches []DataCache,
	nextToken string,
	bucket string,
	cachePath string,
) error {
	if strings.TrimSpace(nextToken) != "" {
		return errors.New("DescribeDataCaches path response is paginated")
	}
	if err := validateDescribeResponse(caches); err != nil {
		return err
	}
	for _, cache := range caches {
		if cache.Bucket != bucket || cache.Path != cachePath {
			return errors.New("DescribeDataCaches path response identity drifted")
		}
	}
	return nil
}

// describeArgs 校验查询身份并生成稳定的 DataCache CLI 参数。
func describeArgs(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > 20 {
		return nil, errors.New("DescribeDataCaches requires 1-20 IDs")
	}
	args := make([]string, 0, len(ids)*2+2)
	for index, id := range ids {
		if !dataCacheIDPattern.MatchString(id) {
			return nil, fmt.Errorf("DataCache ID %d is invalid", index+1)
		}
		args = append(args, fmt.Sprintf("--DataCacheId.%d", index+1), id)
	}
	return append(args, "--Limit", "20"), nil
}

// validateDescribeResponse 拒绝缺少 DataCache 身份字段的响应项。
func validateDescribeResponse(caches []DataCache) error {
	for _, cache := range caches {
		if !validDataCache(cache) {
			return errors.New("DescribeDataCaches response contains an incomplete cache")
		}
	}
	return nil
}

func validDataCache(cache DataCache) bool {
	return dataCacheIDPattern.MatchString(cache.ID) && strings.TrimSpace(cache.Status) != "" &&
		strings.TrimSpace(cache.Bucket) != "" && validAbsolutePath(cache.Path) && cache.SizeGiB > 0
}

// Renew 只续期已接受的 DataCache，不修改其数据源、大小或挂载身份。
func (client *Client) Renew(ctx context.Context, id string, retentionDays int, clientToken string) error {
	if !dataCacheIDPattern.MatchString(id) {
		return errors.New("DataCache renew identity is invalid")
	}
	if err := validateRetentionDays(retentionDays); err != nil {
		return err
	}
	if err := validateClientToken(clientToken); err != nil {
		return err
	}
	output, err := client.run(
		ctx,
		"UpdateDataCache",
		"--DataCacheId", id,
		"--RetentionDays", strconv.Itoa(retentionDays),
		"--ClientToken", clientToken,
	)
	if err != nil {
		return fmt.Errorf("renew DataCache: %w", err)
	}
	var response struct {
		RequestID string `json:"RequestId"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return fmt.Errorf("decode UpdateDataCache response: %w", err)
	}
	if strings.TrimSpace(response.RequestID) == "" {
		return errors.New("UpdateDataCache response is missing RequestId")
	}
	return nil
}

// Delete 删除不再接受的 DataCache generation，并要求服务返回 RequestId。
func (client *Client) Delete(ctx context.Context, id string, bucket string, cachePath string) error {
	if !dataCacheIDPattern.MatchString(id) || !validBucket(bucket) || !validAbsolutePath(cachePath) {
		return errors.New("DataCache delete identity is invalid")
	}
	output, err := client.run(
		ctx,
		"DeleteDataCache",
		"--DataCacheId", id,
		"--Bucket", bucket,
		"--Path", cachePath,
	)
	if err != nil {
		return fmt.Errorf("delete DataCache: %w", err)
	}
	var response struct {
		RequestID string `json:"RequestId"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return fmt.Errorf("decode DeleteDataCache response: %w", err)
	}
	if strings.TrimSpace(response.RequestID) == "" {
		return errors.New("DeleteDataCache response is missing RequestId")
	}
	return nil
}

// run 执行有界且可取消的 DataCache CLI 瞬态错误重试序列。
func (client *Client) run(ctx context.Context, action string, args ...string) ([]byte, error) {
	commandArgs := []string{"eci", action, "--RegionId", client.config.RegionID, "--profile", client.config.Profile}
	commandArgs = append(commandArgs, args...)
	for attempt := 1; attempt <= maxCLIAttempts; attempt++ {
		output, err := client.runner.Run(ctx, client.config.Binary, commandArgs...)
		if err == nil {
			return output, nil
		}
		safeErr := redactSensitiveCLIError(err)
		if !isTransientCLIError(safeErr) || attempt == maxCLIAttempts {
			return nil, fmt.Errorf("aliyun eci %s: %w", action, safeErr)
		}
		if err := client.wait(ctx, retryDelay(attempt)); err != nil {
			return nil, fmt.Errorf("aliyun eci %s retry wait: %w", action, err)
		}
	}
	return nil, fmt.Errorf("aliyun eci %s retry attempts exhausted", action)
}

func retryDelay(attempt int) time.Duration {
	delay := initialRetryDelay * time.Duration(1<<(attempt-1))
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

// waitForRetry 在退避期间保持 context 可取消。
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
func isTransientCLIError(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) && (dnsError.IsTimeout || dnsError.IsTemporary) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
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

// validateCreateRequest 校验 DataCache、OSS 数据源和标签的完整身份绑定。
func validateCreateRequest(request CreateRequest) error {
	checks := []func() error{
		func() error { return validateCreateIdentity(request) },
		func() error { return validateRetentionDays(request.RetentionDays) },
		func() error { return validateClientToken(request.ClientToken) },
		func() error { return validateDataCacheSource(request.Source) },
		func() error { return validateDataCacheTags(request.Tags) },
	}
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func validateCreateIdentity(request CreateRequest) error {
	if !namePattern.MatchString(request.Name) || !validBucket(request.Bucket) ||
		!validAbsolutePath(request.Path) || request.SizeGiB <= 0 {
		return errors.New("DataCache create identity is invalid")
	}
	return nil
}

func validateDataCacheSource(source OSSDataSource) error {
	if !ossBucketPattern.MatchString(source.Bucket) || !internalOSSEndpointPattern.MatchString(source.Endpoint) ||
		!validAbsolutePath(source.Path) || strings.TrimSpace(source.RoleName) == "" {
		return errors.New("DataCache OSS source is invalid")
	}
	return nil
}

// validateDataCacheTags 限制标签数量、长度和控制字符，拒绝非规范元数据。
func validateDataCacheTags(tags map[string]string) error {
	if len(tags) > 10 {
		return errors.New("DataCache supports at most 10 tags")
	}
	for key, value := range tags {
		if key == "" || len(key) > 64 || len(value) > 128 || strings.ContainsAny(key+value, "\x00\r\n") {
			return errors.New("DataCache tag is invalid")
		}
	}
	return nil
}

func validateRetentionDays(value int) error {
	if value < 1 || value > 7 {
		return errors.New("DataCache retention must be 1-7 days")
	}
	return nil
}

func validateClientToken(value string) error {
	if value == "" || len(value) > 64 ||
		strings.TrimSpace(value) != value ||
		strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("DataCache client token is invalid")
	}
	return nil
}

func validBucket(value string) bool {
	return dataCacheBucketPattern.MatchString(value) && value != "eci-system"
}

func validAbsolutePath(value string) bool {
	return value != "/" && path.IsAbs(value) && path.Clean(value) == value &&
		len(value) <= 1024 && !strings.ContainsAny(value, ":\x00")
}

func appendTags(args []string, tags map[string]string) []string {
	keys := sortedTagKeys(tags)
	for index, key := range keys {
		args = append(
			args,
			fmt.Sprintf("--Tag.%d.Key", index+1), key,
			fmt.Sprintf("--Tag.%d.Value", index+1), tags[key],
		)
	}
	return args
}

func sortedTagKeys(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func decodeJSON(output []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("response contains more than one JSON value")
		}
		return fmt.Errorf("decode trailing response data: %w", err)
	}
	return nil
}

var (
	namePattern                = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])$`)
	dataCacheBucketPattern     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	ossBucketPattern           = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)
	internalOSSEndpointPattern = regexp.MustCompile(`^oss-[a-z0-9-]+-internal\.aliyuncs\.com$`)
	dataCacheIDPattern         = regexp.MustCompile(`^edc-[a-z0-9]+$`)
	sensitiveCLIValuePattern   = regexp.MustCompile(`(?i)((?:x-acs-(?:accesskey-id|security-token|signature|signature-nonce)|AccessKeyId|AccessKeySecret|SecurityToken|Signature)\s*[:=]\s*)[^\s&"',}\\]+`)
)

type redactedError struct {
	err error
}

// Error 返回移除凭据与签名值后的错误文本。
func (err redactedError) Error() string {
	return sensitiveCLIValuePattern.ReplaceAllString(err.err.Error(), "${1}<redacted>")
}

// Unwrap 保留底层错误链，供调用方使用 errors.Is 和 errors.As。
func (err redactedError) Unwrap() error {
	return err.err
}

func redactSensitiveCLIError(err error) error {
	return redactedError{err: err}
}

type execRunner struct{}

// Run 在调用方 context 下执行 Aliyun CLI，不读取或持久化 profile 中的凭据。
func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).Output()
	if err == nil {
		return output, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && len(exitError.Stderr) > 0 {
		return output, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitError.Stderr)))
	}
	return output, err
}
