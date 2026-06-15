package datasourcev2

import (
	"context"
	"errors"
	"os"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 暴露 datasource_v2 的 JSON-RPC 接口。
// RPC 层只负责参数反序列化和错误映射，正文读取与入库由 Service 完成。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"datasourceV2/importText": platformrpc.StrictHandler(importTextHandler(svc)),
	}}
}

func importTextHandler(svc Service) func(context.Context, ImportFileTextRequest) (ImportFileTextResult, error) {
	return func(ctx context.Context, req ImportFileTextRequest) (ImportFileTextResult, error) {
		if svc == nil {
			return ImportFileTextResult{}, platformrpc.ErrInvalidState("datasource v2 service is not configured")
		}
		result, err := svc.ImportFileText(ctx, req)
		if err != nil {
			return ImportFileTextResult{}, datasourceV2RPCError(err)
		}
		return result, nil
	}
}

func datasourceV2RPCError(err error) error {
	switch {
	case errors.Is(err, errMissingSourcePath),
		errors.Is(err, errSourcePathMustBeAbsolute),
		errors.Is(err, errSourcePathMustBeFile),
		errors.Is(err, errDatasourceV2ContentEmpty),
		errors.Is(err, errDatasourceV2InvalidUTF8),
		errors.Is(err, errDatasourceV2TextTooLarge):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, errDatasourceV2StoreNotConfigured):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, os.ErrNotExist):
		return platformrpc.ErrNotFound(err.Error())
	default:
		return err
	}
}
