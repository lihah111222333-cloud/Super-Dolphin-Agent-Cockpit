package datasource

import (
	"context"
	"errors"
	"os"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"datasource/upload": platformrpc.StrictHandler(uploadHandler(svc)),
		"datasource/list":   platformrpc.StrictHandler(listHandler(svc)),
		"datasource/delete": platformrpc.StrictHandler(deleteHandler(svc)),
	}}
}

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

func datasourceRPCError(err error) error {
	switch {
	case errors.Is(err, errMissingSourcePath),
		errors.Is(err, errSourcePathMustBeAbsolute),
		errors.Is(err, errSourcePathMustBeFile),
		errors.Is(err, errUnsupportedFileExtension),
		errors.Is(err, errInvalidDatasourceFileName),
		errors.Is(err, errDeleteTargetMustBeFile):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, os.ErrNotExist):
		return platformrpc.ErrNotFound(err.Error())
	default:
		return err
	}
}
