// Package heterocitransport 提供注入式异构节点探针传输适配器。
package heterocitransport

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const maxProbeRequestBytes = 4 << 10

var (
	// ErrInvalidDependencies 表示构造器缺少显式 requester 依赖。
	ErrInvalidDependencies = errors.New("heteroci probe transport dependencies are invalid")
	// ErrInvalidRequest 表示探针调用参数违反传输边界。
	ErrInvalidRequest = errors.New("heteroci probe transport request is invalid")
	// ErrInvalidResponse 表示 requester 返回了不可消费的响应形状。
	ErrInvalidResponse = errors.New("heteroci probe transport response is invalid")
	// ErrTransportUnavailable 表示注入 requester 明确报告传输当前不可用。
	ErrTransportUnavailable = errors.New("heteroci probe requester transport unavailable")
)

// ResponseBody 是 requester 返回的带 context 读取能力的响应体。
type ResponseBody interface {
	Read(context.Context, []byte) (int, error)
	Close() error
}

// Requester 是适配器唯一的注入式外部请求端口。
type Requester interface {
	Do(context.Context, string, []byte) (ResponseBody, error)
}

// Client 是 Requester 的语义别名，便于调用方按客户端命名注入依赖。
type Client = Requester

// Transport 将不透明请求字节适配为 heteroci.ProbeTransport。
type Transport struct {
	requester Requester
}

var _ heteroci.ProbeTransport = (*Transport)(nil)

// New 使用显式 requester 构造探针传输适配器，不提供默认或 fallback 客户端。
func New(requester Requester) (*Transport, error) {
	if isNilValue(requester) {
		return nil, ErrInvalidDependencies
	}
	return &Transport{requester: requester}, nil
}

// Probe 原样转发 endpoint、opaque request 和 context，并返回可关闭的响应体。
func (transport *Transport) Probe(ctx context.Context, endpoint string, request []byte) (heteroci.ProbeResponseBody, error) {
	if err := validateProbeInput(ctx, endpoint, request); err != nil {
		return nil, err
	}
	if transport == nil || isNilValue(transport.requester) {
		return nil, ErrInvalidDependencies
	}
	requestCopy := append([]byte(nil), request...)
	body, err := transport.requester.Do(ctx, endpoint, requestCopy)
	if err != nil {
		return discardRequesterBody(body, mapRequesterError(err))
	}
	if isNilValue(body) {
		return nil, ErrInvalidResponse
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return discardRequesterBody(body, ctxErr)
	}
	return &responseBody{inner: body}, nil
}

// validateProbeInput 校验 context、端点和 opaque request 的基本传输边界。
func validateProbeInput(ctx context.Context, endpoint string, request []byte) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrInvalidRequest, err)
	}
	if endpoint == "" || strings.TrimSpace(endpoint) != endpoint || containsControl(endpoint) {
		return fmt.Errorf("%w: endpoint is invalid", ErrInvalidRequest)
	}
	if len(request) == 0 || len(request) > maxProbeRequestBytes {
		return fmt.Errorf("%w: request bytes have invalid length", ErrInvalidRequest)
	}
	return nil
}

// containsControl 判断端点是否包含不可传输的 ASCII 控制字符。
func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

// mapRequesterError 只把单链明确 unavailable 映射为 core sentinel。
func mapRequesterError(err error) error {
	if onlyUnavailable(err) {
		return heteroci.ErrProbeTransportUnavailable
	}
	return err
}

// onlyUnavailable 拒绝多因果错误，避免把 fatal 根因误投影为 offline。
func onlyUnavailable(err error) bool {
	for depth := 0; err != nil && depth < 64; depth++ {
		if isUnavailableSentinel(err) {
			return true
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			if len(children) != 1 {
				return false
			}
			err = children[0]
			continue
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

// isUnavailableSentinel 只比较可比较的明确错误身份，不调用外部 Is/As。
func isUnavailableSentinel(err error) bool {
	if err == nil {
		return false
	}
	value := reflect.ValueOf(err)
	if !value.IsValid() || !value.Comparable() {
		return false
	}
	return err == ErrTransportUnavailable || err == heteroci.ErrProbeTransportUnavailable
}

// discardRequesterBody 关闭 requester 错误路径上已经返回的 body，防止资源泄漏。
func discardRequesterBody(body ResponseBody, primary error) (heteroci.ProbeResponseBody, error) {
	if isNilValue(body) {
		return nil, primary
	}
	closeErr := body.Close()
	if closeErr == nil {
		return nil, errors.Join(ErrInvalidResponse, primary)
	}
	return nil, errors.Join(ErrInvalidResponse, primary, closeErr)
}

// isNilValue 识别 nil 接口以及携带 nil 指针的接口依赖。
func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
