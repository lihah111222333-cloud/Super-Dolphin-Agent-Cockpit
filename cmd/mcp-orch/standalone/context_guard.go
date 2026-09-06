package standalone

import (
	"context"
	"errors"
	"reflect"
)

// ErrExecutionContextRequired 表示调用方未提供可检查的执行上下文。
var ErrExecutionContextRequired = errors.New("CLI execution context is required")

func requireExecutionContext(ctx context.Context) error {
	if contextIsNil(ctx) {
		return ErrExecutionContextRequired
	}
	return nil
}

// requireActiveExecutionContext 在任何会改变会话或触发 provider I/O 的入口
// 之前检查调用方仍持有有效的执行窗口。已取消 context 不得继续推进生命周期。
func requireActiveExecutionContext(ctx context.Context) error {
	if err := requireExecutionContext(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

// contextIsNil 同时拒绝 nil interface 和 typed-nil context。
func contextIsNil(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	value := reflect.ValueOf(ctx)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
