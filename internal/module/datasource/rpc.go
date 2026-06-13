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

func datasourceRPCError(err error) error {
	switch {
	case errors.Is(err, errMissingCWD),
		errors.Is(err, errMissingSourcePath),
		errors.Is(err, errCWDMustBeAbsolute),
		errors.Is(err, errCWDMustBeDirectory),
		errors.Is(err, errSourcePathMustBeAbsolute),
		errors.Is(err, errSourcePathMustBeFile),
		errors.Is(err, errUnsupportedFileExtension):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, os.ErrNotExist):
		return platformrpc.ErrNotFound(err.Error())
	case errors.Is(err, errUploadTargetAlreadyExists):
		return platformrpc.ErrConflict(err.Error())
	default:
		return err
	}
}
