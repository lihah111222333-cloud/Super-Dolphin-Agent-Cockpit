package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// CallTool 调用当前 peer 暴露的工具，先补全工作区作用域并在完成后签发语义回执。
func (p registryToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if p.ensureReady != nil {
		if err := p.ensureReady(); err != nil {
			return nil, err
		}
	}
	var err error
	ctx, err = withRuntimeWorkspaceScopeFallback(ctx)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	result, callErr := handleToolCall(ctx, p.defs, name, args)
	if p.receiptSink != nil {
		receiptErr := p.receiptSink.Append(ctx, name, args, result, callErr, started, time.Now())
		if receiptErr != nil {
			return nil, errors.Join(callErr, receiptErr)
		}
	}
	return result, callErr
}
