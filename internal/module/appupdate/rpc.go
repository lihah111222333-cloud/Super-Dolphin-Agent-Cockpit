package appupdate

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2/handler"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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
		return executeUpdateRPC("check", func() (CheckResult, error) { return svc.Check(ctx) })
	}
}

// downloadHandler 返回 appupdate.download 的 RPC handler，直接委托 Service.Download。
func downloadHandler(svc Service) func(context.Context, struct{}) (DownloadResult, error) {
	return func(ctx context.Context, _ struct{}) (DownloadResult, error) {
		return executeUpdateRPC("download", func() (DownloadResult, error) { return svc.Download(ctx) })
	}
}

// installHandler 返回 appupdate.install 的 RPC handler，直接委托 Service.Install。
func installHandler(svc Service) func(context.Context, struct{}) (InstallResult, error) {
	return func(ctx context.Context, _ struct{}) (InstallResult, error) {
		return executeUpdateRPC("install", func() (InstallResult, error) { return svc.Install(ctx) })
	}
}

// installLatestHandler 返回 appupdate.installLatest 的 RPC handler，先下载再安装最新版本。
func installLatestHandler(svc Service) func(context.Context, struct{}) (InstallResult, error) {
	return func(ctx context.Context, _ struct{}) (InstallResult, error) {
		return executeUpdateRPC("installLatest", func() (InstallResult, error) { return svc.InstallLatest(ctx) })
	}
}

func executeUpdateRPC[T any](operation string, invoke func() (T, error)) (T, error) {
	result, err := invoke()
	if err == nil {
		return result, nil
	}
	if recoveryErr, code, ok := updateRecoveryRPCError(err); ok {
		pkglogger.Error("appupdate RPC recovery action required", "operation", operation, "code", code)
		return result, recoveryErr
	}
	pkglogger.Error("appupdate RPC request failed", "operation", operation, "error", err)
	return result, platformrpc.ErrInvalidState("app update request failed")
}

func updateRecoveryRPCError(err error) (error, string, bool) {
	if recoveryErr, ok := platformrpc.RecoveryActionError(err); ok {
		failure, _ := contract.RecoveryFailureFromError(err)
		return recoveryErr, failure.Code, true
	}
	code := ""
	switch {
	case errors.Is(err, contract.ErrUpdateSignatureInvalid):
		code = "UPDATE_SIGNATURE_INVALID"
	case errors.Is(err, contract.ErrUpdateIntegrityInvalid):
		code = "UPDATE_INTEGRITY_INVALID"
	default:
		return nil, "", false
	}
	failure, ok := contract.RecoveryFailureForCode(code, "")
	if !ok {
		return nil, "", false
	}
	recoveryErr, ok := platformrpc.RecoveryActionError(updateRecoveryCarrier{failure: failure})
	return recoveryErr, code, ok
}

type updateRecoveryCarrier struct{ failure contract.RecoveryFailure }

// Error 返回固定内部分类文本，不携带底层签名或安装器输出。
func (updateRecoveryCarrier) Error() string { return "app update recovery action is required" }

// RecoveryFailure 返回经过 registry 校验的最小恢复元数据。
func (err updateRecoveryCarrier) RecoveryFailure() contract.RecoveryFailure { return err.failure }
