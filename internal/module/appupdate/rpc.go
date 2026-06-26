package appupdate

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 注册 appupdate RPC handler，并把业务错误原样交给 RPC 层映射。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"app/update/check":         platformrpc.StrictHandler(checkHandler(svc)),
		"app/update/download":      platformrpc.StrictHandler(downloadHandler(svc)),
		"app/update/install":       platformrpc.StrictHandler(installHandler(svc)),
		"app/update/installLatest": platformrpc.StrictHandler(installLatestHandler(svc)),
	}}
}

// checkHandler 返回 appupdate.check 的 RPC handler，直接委托 Service.Check。
func checkHandler(svc Service) func(context.Context, struct{}) (CheckResult, error) {
	return func(ctx context.Context, _ struct{}) (CheckResult, error) {
		return svc.Check(ctx)
	}
}

// downloadHandler 返回 appupdate.download 的 RPC handler，直接委托 Service.Download。
func downloadHandler(svc Service) func(context.Context, struct{}) (DownloadResult, error) {
	return func(ctx context.Context, _ struct{}) (DownloadResult, error) {
		return svc.Download(ctx)
	}
}

// installHandler 返回 appupdate.install 的 RPC handler，直接委托 Service.Install。
func installHandler(svc Service) func(context.Context, struct{}) (InstallResult, error) {
	return func(ctx context.Context, _ struct{}) (InstallResult, error) {
		return svc.Install(ctx)
	}
}

// installLatestHandler 返回 appupdate.installLatest 的 RPC handler，先下载再安装最新版本。
func installLatestHandler(svc Service) func(context.Context, struct{}) (InstallResult, error) {
	return func(ctx context.Context, _ struct{}) (InstallResult, error) {
		return svc.InstallLatest(ctx)
	}
}
