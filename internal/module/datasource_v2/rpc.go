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
		"datasourceV2/importText": platformrpc.StrictHandler(importTextHandler(svc)),
		"datasourceV2/create":     platformrpc.StrictHandler(importTextHandler(svc)),
		"datasourceV2/list":       platformrpc.StrictHandler(listDocumentsHandler(svc)),
		"datasourceV2/get":        platformrpc.StrictHandler(getDocumentHandler(svc)),
		"datasourceV2/update":     platformrpc.StrictHandler(updateDocumentHandler(svc)),
		"datasourceV2/delete":     platformrpc.StrictHandler(deleteDocumentHandler(svc)),
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

func datasourceV2RPCError(err error) error {
	switch {
	case errors.Is(err, errMissingSourcePath),
		errors.Is(err, errSourcePathMustBeAbsolute),
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
