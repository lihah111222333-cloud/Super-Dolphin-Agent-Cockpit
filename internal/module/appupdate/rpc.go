package appupdate

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 创建处理器。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"app/update/check":         platformrpc.StrictHandler(checkHandler(svc)),
		"app/update/download":      platformrpc.StrictHandler(downloadHandler(svc)),
		"app/update/install":       platformrpc.StrictHandler(installHandler(svc)),
		"app/update/installLatest": platformrpc.StrictHandler(installLatestHandler(svc)),
	}}
}

func checkHandler(svc Service) func(context.Context, struct{}) (CheckResult, error) {
	return func(ctx context.Context, _ struct{}) (CheckResult, error) {
		return svc.Check(ctx)
	}
}

func downloadHandler(svc Service) func(context.Context, struct{}) (DownloadResult, error) {
	return func(ctx context.Context, _ struct{}) (DownloadResult, error) {
		return svc.Download(ctx)
	}
}

func installHandler(svc Service) func(context.Context, struct{}) (InstallResult, error) {
	return func(ctx context.Context, _ struct{}) (InstallResult, error) {
		return svc.Install(ctx)
	}
}

func installLatestHandler(svc Service) func(context.Context, struct{}) (InstallResult, error) {
	return func(ctx context.Context, _ struct{}) (InstallResult, error) {
		return svc.InstallLatest(ctx)
	}
}
