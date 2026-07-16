package localci

import (
	"context"
	"errors"
	"reflect"
)

// validateImageBuilderEntry 在读取请求或命中缓存前校验构建器和调用上下文。
func validateImageBuilderEntry(builder *ImageBuilder, ctx context.Context) error {
	if builder == nil || buildKitRunnerIsNil(builder.runner) {
		return errors.New("image builder is not initialized")
	}
	if ctx == nil {
		return errors.New("candidate build context is required")
	}
	return ctx.Err()
}

// buildKitRunnerIsNil 识别接口本身为空以及接口内承载的各类 typed nil。
func buildKitRunnerIsNil(runner BuildKitRunner) bool {
	if runner == nil {
		return true
	}
	value := reflect.ValueOf(runner)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
