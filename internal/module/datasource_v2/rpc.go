// Package datasourcev2 提供文件正文导入、分块存储和语义检索能力，供 prompt 动态段和前端数据源管理页使用。
package datasourcev2

import (
	"context"
	"errors"
	"os"

	"github.com/creachadair/jrpc2/handler"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 暴露 datasource_v2 的 JSON-RPC 接口。
// RPC 层只负责参数反序列化和错误映射，正文读取与入库由 Service 完成。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"datasourceV2/importText":      platformrpc.StrictHandler(importTextHandler(svc)),
		"datasourceV2/create":          platformrpc.StrictHandler(importTextHandler(svc)),
		"datasourceV2/importLocalFile": platformrpc.StrictHandler(importLocalFileHandler(svc)),
		"datasourceV2/list":            platformrpc.StrictHandler(listDocumentsHandler(svc)),
		"datasourceV2/get":             platformrpc.StrictHandler(getDocumentHandler(svc)),
		"datasourceV2/update":          platformrpc.StrictHandler(updateDocumentHandler(svc)),
		"datasourceV2/delete":          platformrpc.StrictHandler(deleteDocumentHandler(svc)),
	}}
}

// importTextHandler 绑定 datasourceV2/importText 和兼容名 datasourceV2/create。
// RPC 层只透传路径参数；workspace 校验、读取和事务写入都由 Service 负责。
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

// importLocalFileHandler 绑定 datasourceV2/importLocalFile 请求。
// 该入口只用于桌面端显式选择文件，因此允许 workspace 外绝对路径，仍由 Service 做文件类型校验。
func importLocalFileHandler(svc Service) func(context.Context, ImportLocalFileRequest) (ImportFileTextResult, error) {
	return func(ctx context.Context, req ImportLocalFileRequest) (ImportFileTextResult, error) {
		if svc == nil {
			return ImportFileTextResult{}, platformrpc.ErrInvalidState("datasource v2 service is not configured")
		}
		result, err := svc.ImportLocalFile(ctx, req)
		if err != nil {
			return ImportFileTextResult{}, datasourceV2RPCError(err)
		}
		return result, nil
	}
}

// listDocumentsHandler 绑定 datasourceV2/list 请求。
// limit 必须由前端显式传入，避免列表接口无界读取。
func listDocumentsHandler(svc Service) func(context.Context, ListDocumentsRequest) (ListDocumentsResult, error) {
	return func(ctx context.Context, req ListDocumentsRequest) (ListDocumentsResult, error) {
		if svc == nil {
			return ListDocumentsResult{}, platformrpc.ErrInvalidState("datasource v2 service is not configured")
		}
		result, err := svc.ListDocuments(ctx, req)
		if err != nil {
			return ListDocumentsResult{}, datasourceV2RPCError(err)
		}
		return result, nil
	}
}

// getDocumentHandler 绑定 datasourceV2/get 请求。
// 只做 documentId wire 适配，文档存在性和分块读取错误由 Service/Store 返回。
func getDocumentHandler(svc Service) func(context.Context, GetDocumentRequest) (GetDocumentResult, error) {
	return func(ctx context.Context, req GetDocumentRequest) (GetDocumentResult, error) {
		if svc == nil {
			return GetDocumentResult{}, platformrpc.ErrInvalidState("datasource v2 service is not configured")
		}
		result, err := svc.GetDocument(ctx, req)
		if err != nil {
			return GetDocumentResult{}, datasourceV2RPCError(err)
		}
		return result, nil
	}
}

// updateDocumentHandler 绑定 datasourceV2/update 请求。
// 更新路径只改元数据，不改已导入正文分块。
func updateDocumentHandler(svc Service) func(context.Context, UpdateDocumentRequest) (DocumentResult, error) {
	return func(ctx context.Context, req UpdateDocumentRequest) (DocumentResult, error) {
		if svc == nil {
			return DocumentResult{}, platformrpc.ErrInvalidState("datasource v2 service is not configured")
		}
		result, err := svc.UpdateDocument(ctx, req)
		if err != nil {
			return DocumentResult{}, datasourceV2RPCError(err)
		}
		return result, nil
	}
}

// deleteDocumentHandler 绑定 datasourceV2/delete 请求。
// 删除是否级联清理分块由 store 实现保证，RPC 层不吞掉 NotFound 或校验错误。
func deleteDocumentHandler(svc Service) func(context.Context, DeleteDocumentRequest) (DeleteDocumentResult, error) {
	return func(ctx context.Context, req DeleteDocumentRequest) (DeleteDocumentResult, error) {
		if svc == nil {
			return DeleteDocumentResult{}, platformrpc.ErrInvalidState("datasource v2 service is not configured")
		}
		result, err := svc.DeleteDocument(ctx, req)
		if err != nil {
			return DeleteDocumentResult{}, datasourceV2RPCError(err)
		}
		return result, nil
	}
}

// datasourceV2RPCError 将 datasource_v2 业务错误映射为标准 jrpc2 错误码。
func datasourceV2RPCError(err error) error {
	switch {
	case errors.Is(err, errMissingSourcePath),
		errors.Is(err, errSourcePathMustBeAbsolute),
		errors.Is(err, errSourcePathOutsideWorkspace),
		errors.Is(err, errSourcePathMustBeFile),
		errors.Is(err, errUnsupportedFileExtension),
		errors.Is(err, errDatasourceV2ContentEmpty),
		errors.Is(err, errDatasourceV2InvalidUTF8),
		errors.Is(err, errPDFTextNotFound),
		errors.Is(err, errDatasourceV2TextTooLarge),
		errors.Is(err, errDatasourceV2DocumentIDRequired),
		errors.Is(err, errDatasourceV2ListLimitRequired),
		errors.Is(err, errDatasourceV2MissingFileName),
		errors.Is(err, errDatasourceV2SizeBytesInvalid):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, errDatasourceV2StoreNotConfigured):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, os.ErrNotExist),
		errors.Is(err, platformdb.ErrNotFound):
		return platformrpc.ErrNotFound(err.Error())
	case errors.Is(err, platformdb.ErrConflict):
		return platformrpc.ErrConflict(err.Error())
	default:
		return err
	}
}
