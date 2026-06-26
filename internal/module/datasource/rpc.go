// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import (
	"context"
	"errors"
	"os"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 注册 datasource/* JSON-RPC handler。
// RPC 层只做服务缺失检查和错误码映射，文件路径安全由 Service 负责。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"datasource/upload": platformrpc.StrictHandler(uploadHandler(svc)),
		"datasource/list":   platformrpc.StrictHandler(listHandler(svc)),
		"datasource/delete": platformrpc.StrictHandler(deleteHandler(svc)),
	}}
}

// uploadHandler 处理 datasource/upload 请求，将上传文件错误映射为 RPC 错误码。
func uploadHandler(svc Service) func(context.Context, UploadFileRequest) (UploadFileResult, error) {
	return func(ctx context.Context, req UploadFileRequest) (UploadFileResult, error) {
		if svc == nil {
			return UploadFileResult{}, platformrpc.ErrInvalidState("datasource service is not configured")
		}
		result, err := svc.UploadFile(ctx, req)
		if err != nil {
			return UploadFileResult{}, datasourceRPCError(err)
		}
		return result, nil
	}
}

// listHandler 处理 datasource/list 请求，服务未接线时返回 invalid state。
func listHandler(svc Service) func(context.Context, struct{}) (ListFilesResult, error) {
	return func(ctx context.Context, _ struct{}) (ListFilesResult, error) {
		if svc == nil {
			return ListFilesResult{}, platformrpc.ErrInvalidState("datasource service is not configured")
		}
		result, err := svc.ListFiles(ctx)
		if err != nil {
			return ListFilesResult{}, datasourceRPCError(err)
		}
		return result, nil
	}
}

// deleteHandler 处理 datasource/delete 请求，并复用 datasourceRPCError 映射路径错误。
func deleteHandler(svc Service) func(context.Context, DeleteFileRequest) (DeleteFileResult, error) {
	return func(ctx context.Context, req DeleteFileRequest) (DeleteFileResult, error) {
		if svc == nil {
			return DeleteFileResult{}, platformrpc.ErrInvalidState("datasource service is not configured")
		}
		result, err := svc.DeleteFile(ctx, req)
		if err != nil {
			return DeleteFileResult{}, datasourceRPCError(err)
		}
		return result, nil
	}
}

// datasourceRPCError 将 datasource 业务错误映射为标准 jrpc2 错误码。
// 未识别错误原样返回，保留调用栈上下文给上层日志。
func datasourceRPCError(err error) error {
	switch {
	case errors.Is(err, errMissingSourcePath),
		errors.Is(err, errSourcePathMustBeAbsolute),
		errors.Is(err, errSourcePathMustBeFile),
		errors.Is(err, errUnsupportedFileExtension),
		errors.Is(err, errUnsupportedTextEncoding),
		errors.Is(err, errInvalidDatasourceFileName),
		errors.Is(err, errDeleteTargetMustBeFile),
		errors.Is(err, errDatasourceContentEmpty),
		errors.Is(err, errPDFTextNotFound):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, os.ErrNotExist):
		return platformrpc.ErrNotFound(err.Error())
	default:
		return err
	}
}
