package workerio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// Upload 创建固定配置的 OSS 对象，并在返回成功前校验元数据与对象字节。
// source 只消费一次，上传正文始终流式传输，不在内存中缓存完整对象。
func (client *Client) Upload(ctx context.Context, source io.Reader, size int64, digest string) error {
	if err := validateUploadArguments(client, ctx, source, size, digest); err != nil {
		return err
	}
	objectURL, resource, err := uploadObjectAddress(client.config)
	if err != nil {
		return err
	}
	credentials, err := client.currentCredentials(ctx)
	if err != nil {
		return err
	}
	body := newUploadBody(source, size)
	if err := client.putUpload(ctx, objectURL, resource, credentials, body, size, digest); err != nil {
		return err
	}
	return client.verifyUploadResult(ctx, objectURL, resource, credentials, body, size, digest)
}

// validateUploadArguments 在访问 IMDS 或 OSS 前拒绝上传参数、大小和摘要错误。
func validateUploadArguments(client *Client, ctx context.Context, source io.Reader, size int64, digest string) error {
	if client == nil {
		return errors.New("OSS upload client must not be nil")
	}
	if ctx == nil {
		return errors.New("OSS upload context must not be nil")
	}
	if nilUploadReader(source) {
		return errors.New("OSS upload source must not be nil")
	}
	if size < 0 || size > maxObjectBytes || size > client.config.MaxBytes {
		return errors.New("OSS upload size exceeds configured limit")
	}
	if !validUploadDigest(digest) {
		return errors.New("OSS upload SHA-256 digest is invalid")
	}
	return nil
}

func (client *Client) verifyUploadResult(ctx context.Context, objectURL url.URL, resource string, credentials temporaryCredentials, body *uploadBody, size int64, digest string) error {
	if body.readErr != nil {
		return errors.New("OSS upload source read failed")
	}
	if body.total != size {
		return errors.New("OSS upload source size does not match declared size")
	}
	if hex.EncodeToString(body.sum()) != digest {
		return errors.New("OSS upload source digest does not match declared digest")
	}
	if err := client.headUploadedObject(ctx, objectURL, resource, credentials, digest, size); err != nil {
		return err
	}
	return client.getUploadedObject(ctx, objectURL, resource, credentials, digest, size)
}

func nilUploadReader(source io.Reader) bool {
	if source == nil {
		return true
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// validUploadDigest 只接受规范的小写十六进制 SHA-256 摘要。
func validUploadDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

// uploadObjectAddress 根据固定 endpoint/bucket/key 构造 OSS URL 与签名资源路径。
func uploadObjectAddress(config Config) (url.URL, string, error) {
	if err := validateConfig(config); err != nil {
		return url.URL{}, "", err
	}
	for segment := range strings.SplitSeq(config.Key, "/") {
		if segment == "." || segment == ".." || strings.Contains(segment, "\\") {
			return url.URL{}, "", errors.New("OSS object key contains an invalid path segment")
		}
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil {
		return url.URL{}, "", errors.New("OSS endpoint is invalid")
	}
	objectURL := *endpoint
	if strings.HasSuffix(endpoint.Hostname(), ".aliyuncs.com") {
		objectURL.Host = config.Bucket + "." + endpoint.Host
		objectURL.Path = "/" + config.Key
	} else {
		objectURL.Path = "/" + config.Bucket + "/" + config.Key
	}
	objectURL.RawPath = ""
	resource := objectURL.EscapedPath()
	if strings.HasSuffix(endpoint.Hostname(), ".aliyuncs.com") {
		resource = "/" + config.Bucket + resource
	}
	return objectURL, resource, nil
}

type uploadBody struct {
	source    io.Reader
	closer    io.Closer
	remaining int64
	total     int64
	digest    hash.Hash
	readErr   error
	closeOnce sync.Once
	closeErr  error
}

func newUploadBody(source io.Reader, size int64) *uploadBody {
	body := &uploadBody{source: source, remaining: size, digest: sha256.New()}
	if closer, ok := source.(io.Closer); ok {
		body.closer = closer
	}
	return body
}

// Read 按声明的对象大小限制读取，并在流式读取过程中计算摘要。
func (body *uploadBody) Read(destination []byte) (int, error) {
	if body.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(destination)) > body.remaining {
		destination = destination[:body.remaining]
	}
	count, err := body.source.Read(destination)
	if count < 0 || count > len(destination) {
		body.readErr = errors.New("invalid source reader result")
		return 0, body.readErr
	}
	if count > 0 {
		_, _ = body.digest.Write(destination[:count])
		body.total += int64(count)
		body.remaining -= int64(count)
	}
	if err == io.EOF {
		if body.remaining > 0 {
			body.readErr = io.ErrUnexpectedEOF
			return count, body.readErr
		}
		return count, nil
	}
	if err != nil {
		body.readErr = err
	}
	return count, err
}

func (body *uploadBody) sum() []byte { return body.digest.Sum(nil) }

// Close 至多关闭一次调用方持有的 source，并将关闭错误替换为不泄密的摘要错误。
func (body *uploadBody) Close() error {
	body.closeOnce.Do(func() {
		if body.closer != nil {
			if err := body.closer.Close(); err != nil {
				body.closeErr = errors.New("close OSS upload source")
			}
		}
	})
	return body.closeErr
}

// putUpload 使用临时 RAM 凭据执行一次不可覆盖的 OSS PUT，并确保响应体和 source 被关闭。
func (client *Client) putUpload(ctx context.Context, objectURL url.URL, resource string, credentials temporaryCredentials, body *uploadBody, size int64, digest string) (returnErr error) {
	date := client.clock().UTC().Format(http.TimeFormat)
	request, err := newSignedUploadRequest(ctx, http.MethodPut, objectURL, resource, credentials, strings.Join([]string{
		"x-oss-forbid-overwrite:true",
		"x-oss-meta-sha256:" + digest,
		"x-oss-meta-size:" + strconv.FormatInt(size, 10),
	}, "\n")+"\n", date, body)
	if err != nil {
		return errors.New("create OSS upload request")
	}
	request.ContentLength = size
	request.Header.Set("x-oss-forbid-overwrite", "true")
	request.Header.Set("x-oss-meta-sha256", digest)
	request.Header.Set("x-oss-meta-size", strconv.FormatInt(size, 10))
	defer func() {
		returnErr = errors.Join(returnErr, closeUploadResource(body, "close OSS upload source"))
	}()
	response, err := client.ossHTTPClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("OSS upload canceled: %w", ctxErr)
		}
		if body.readErr != nil {
			return errors.New("OSS upload source read failed")
		}
		return errors.New("OSS upload request failed")
	}
	if response.Body != nil {
		defer func() {
			returnErr = errors.Join(returnErr, closeUploadResource(response.Body, "close OSS upload response body"))
		}()
	}
	if !isSuccess(response.StatusCode) {
		discardResponse(response.Body, errorBodyLimit)
		return fmt.Errorf("OSS upload returned HTTP %d", response.StatusCode)
	}
	return nil
}

func newSignedUploadRequest(ctx context.Context, method string, objectURL url.URL, resource string, credentials temporaryCredentials, extraCanonicalHeaders string, date string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, objectURL.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Date", date)
	request.Header.Set("x-oss-security-token", credentials.SecurityToken)
	request.Header.Set("Authorization", ossAuthorizationWithHeaders(method, date, resource, credentials, extraCanonicalHeaders))
	return request, nil
}

// headUploadedObject 读取并严格校验上传对象的 Content-Length 与 SHA/size 元数据。
func (client *Client) headUploadedObject(ctx context.Context, objectURL url.URL, resource string, credentials temporaryCredentials, digest string, size int64) (returnErr error) {
	request, err := client.newReadbackRequest(ctx, http.MethodHead, objectURL, resource, credentials)
	if err != nil {
		return errors.New("create OSS metadata readback request")
	}
	response, err := client.ossHTTPClient.Do(request)
	if err != nil {
		return client.readbackRequestError(ctx, "metadata", err)
	}
	if response.Body != nil {
		defer func() {
			returnErr = errors.Join(returnErr, closeUploadResource(response.Body, "close OSS metadata response body"))
		}()
	}
	if !isSuccess(response.StatusCode) {
		discardResponse(response.Body, errorBodyLimit)
		return fmt.Errorf("OSS metadata readback returned HTTP %d", response.StatusCode)
	}
	if err := validateReadbackHeaders(response, digest, size, true); err != nil {
		return err
	}
	discardResponse(response.Body, errorBodyLimit)
	return nil
}

func (client *Client) getUploadedObject(ctx context.Context, objectURL url.URL, resource string, credentials temporaryCredentials, digest string, size int64) (returnErr error) {
	request, err := client.newReadbackRequest(ctx, http.MethodGet, objectURL, resource, credentials)
	if err != nil {
		return errors.New("create OSS body readback request")
	}
	response, err := client.ossHTTPClient.Do(request)
	if err != nil {
		return client.readbackRequestError(ctx, "body", err)
	}
	return verifyUploadResponse(response, digest, size)
}

func verifyUploadResponse(response *http.Response, digest string, size int64) (returnErr error) {
	if response.Body == nil {
		return errors.New("OSS body readback has no response body")
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeUploadResource(response.Body, "close OSS body response body"))
	}()
	if !isSuccess(response.StatusCode) {
		discardResponse(response.Body, errorBodyLimit)
		return fmt.Errorf("OSS body readback returned HTTP %d", response.StatusCode)
	}
	if err := validateReadbackHeaders(response, digest, size, true); err != nil {
		return err
	}
	return verifyUploadBody(response.Body, digest, size)
}

func closeUploadResource(resource io.Closer, message string) error {
	if resource == nil {
		return nil
	}
	if err := resource.Close(); err != nil {
		return errors.New(message)
	}
	return nil
}

func verifyUploadBody(source io.Reader, digest string, size int64) error {
	hasher := sha256.New()
	actualSize, err := io.CopyN(hasher, source, size+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("read OSS body readback")
	}
	if actualSize != size {
		return errors.New("OSS body readback size does not match declared size")
	}
	if hex.EncodeToString(hasher.Sum(nil)) != digest {
		return errors.New("OSS body readback digest does not match declared digest")
	}
	return nil
}

func (client *Client) newReadbackRequest(ctx context.Context, method string, objectURL url.URL, resource string, credentials temporaryCredentials) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, objectURL.String(), nil)
	if err != nil {
		return nil, err
	}
	date := client.clock().UTC().Format(http.TimeFormat)
	request.Header.Set("Date", date)
	request.Header.Set("x-oss-security-token", credentials.SecurityToken)
	request.Header.Set("Authorization", ossAuthorization(method, date, resource, credentials))
	return request, nil
}

func (client *Client) readbackRequestError(ctx context.Context, kind string, _ error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("OSS %s readback canceled: %w", kind, ctxErr)
	}
	return fmt.Errorf("OSS %s readback request failed", kind)
}

func validateReadbackHeaders(response *http.Response, digest string, size int64, requireMetadata bool) error {
	if response.ContentLength != size {
		return errors.New("OSS readback Content-Length does not match declared size")
	}
	if !requireMetadata {
		return nil
	}
	if response.Header.Get("x-oss-meta-sha256") != digest {
		return errors.New("OSS readback SHA-256 metadata does not match declared digest")
	}
	if response.Header.Get("x-oss-meta-size") != strconv.FormatInt(size, 10) {
		return errors.New("OSS readback size metadata does not match declared size")
	}
	return nil
}
