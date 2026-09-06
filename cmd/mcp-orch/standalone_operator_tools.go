package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// standaloneOperatorToolProvider 在独立 operator 入口逐次校验持久化来源，不替代下游任务权限。
type standaloneOperatorToolProvider struct {
	base   common.ToolProvider
	source *standaloneOperatorSource
}

// newStandaloneOperatorToolProvider 拒绝缺失依赖，保留内层任务工具的过滤及授权边界。
func newStandaloneOperatorToolProvider(base common.ToolProvider, source *standaloneOperatorSource) (*standaloneOperatorToolProvider, error) {
	if base == nil || source == nil {
		return nil, errors.New("operator tools require provider and source")
	}
	value := reflect.ValueOf(base)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil, errors.New("operator tools require non-nil provider")
	}
	if err := source.Validate(); err != nil {
		return nil, err
	}
	return &standaloneOperatorToolProvider{base: base, source: source}, nil
}

// requireCurrent 在任何工具枚举或调用前重读当前来源，不信任请求参数中的身份。
func (p *standaloneOperatorToolProvider) requireCurrent(ctx context.Context) error {
	principal, err := common.RequireCallerPrincipal(ctx)
	if err != nil {
		return err
	}
	if p == nil || p.base == nil || p.source == nil {
		return errors.New("operator tool source is unavailable")
	}
	_, err = p.source.RequireCurrent(ctx, principal)
	return err
}

// ListTools 只在当前 operator 来源验证成功后进入内层过滤器。
func (p *standaloneOperatorToolProvider) ListTools(ctx context.Context) ([]common.MCPTool, error) {
	if err := p.requireCurrent(ctx); err != nil {
		return nil, err
	}
	return p.base.ListTools(ctx)
}

// CallTool 对已初始化 session 的每次调用重做来源 fence，撤销与换代后的后续调用逐次核验。
func (p *standaloneOperatorToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if err := p.requireCurrent(ctx); err != nil {
		return nil, err
	}
	return p.base.CallTool(ctx, name, args)
}
