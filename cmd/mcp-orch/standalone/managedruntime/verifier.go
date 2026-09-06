package managedruntime

import (
	"context"
	"errors"
	"reflect"
	"runtime"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

var (
	// ErrProcessReceiptIdentityMismatch 表示 receipt 对应的进程代际或可执行文件身份已变化。
	ErrProcessReceiptIdentityMismatch = errors.New("managed runtime process receipt identity mismatch")
	// ErrProcessReceiptExecutableDigestMismatch 表示实际可执行文件摘要不符合 request/receipt。
	ErrProcessReceiptExecutableDigestMismatch = errors.New("managed runtime executable digest mismatch")
	// ErrProcessReceiptCaptureUnsupported 表示当前平台没有受支持的生产进程身份 capture。
	ErrProcessReceiptCaptureUnsupported = errors.New("managed runtime process identity capture is unsupported")
)

// ExecutableDigestVerifier 验证稳定进程身份指向的可执行文件内容摘要。
//
// 实现必须读取可执行文件并自行计算摘要；调用方不得把路径字符串当作摘要，
// 也不得通过 IsRunning 或 PID 存活探测替代该验证。
type ExecutableDigestVerifier interface {
	VerifyExecutableDigest(context.Context, pidregistry.StableProcessIdentity, string) error
}

// ProcessReceiptVerifier 只读验证外部进程 prepared receipt，不负责启动或终止进程。
// Capture 仅供测试注入；生产构造器始终绑定 pidregistry 的内核身份 capture。
type ProcessReceiptVerifier struct {
	Capture                  func(int) (pidregistry.StableProcessIdentity, error)
	ExecutableDigestVerifier ExecutableDigestVerifier
}

// NewProcessReceiptVerifier 构造生产 receipt verifier；缺少显式摘要验证器时拒绝构造。
func NewProcessReceiptVerifier(digestVerifier ExecutableDigestVerifier) (*ProcessReceiptVerifier, error) {
	if interfaceIsNil(digestVerifier) {
		return nil, ErrProductionNoGo
	}
	return &ProcessReceiptVerifier{
		Capture:                  productionCaptureStableProcessIdentity,
		ExecutableDigestVerifier: digestVerifier,
	}, nil
}

// VerifyPrepared 在两次稳定身份 capture 之间回验 prepared receipt，不产生进程副作用。
func (v *ProcessReceiptVerifier) VerifyPrepared(
	ctx context.Context,
	request PrepareRequest,
	receipt PreparedReceipt,
) error {
	if err := validatePreparedVerification(ctx, v, request, receipt); err != nil {
		return err
	}
	pid, err := receiptPID(receipt.Process.PID)
	if err != nil {
		return err
	}
	before, err := captureReceiptIdentity(ctx, v.Capture, pid, receipt)
	if err != nil {
		return err
	}
	if err := verifyExecutableDigest(ctx, v.ExecutableDigestVerifier, before, request.ExecutableDigest); err != nil {
		return err
	}
	after, err := captureReceiptIdentity(ctx, v.Capture, pid, receipt)
	if err != nil {
		return err
	}
	if !sameCapturedIdentity(before, after) {
		return errors.Join(ErrProcessReceiptIdentityMismatch, pidregistry.ErrStableProcessIdentityMismatch)
	}
	if err := verifyExecutableDigest(ctx, v.ExecutableDigestVerifier, after, receipt.Process.ExecutableDigest); err != nil {
		return err
	}
	return contextError(ctx)
}

// validatePreparedVerification 验证上下文、依赖和两个 managed runtime 合同值。
func validatePreparedVerification(
	ctx context.Context,
	verifier *ProcessReceiptVerifier,
	request PrepareRequest,
	receipt PreparedReceipt,
) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if verifier == nil || verifier.Capture == nil || interfaceIsNil(verifier.ExecutableDigestVerifier) {
		return ErrProductionNoGo
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	return MatchPrepare(request, receipt)
}

// captureReceiptIdentity 执行一次有界 capture，并立刻与 receipt 的稳定身份比对。
func captureReceiptIdentity(
	ctx context.Context,
	capture func(int) (pidregistry.StableProcessIdentity, error),
	pid int,
	receipt PreparedReceipt,
) (pidregistry.StableProcessIdentity, error) {
	if err := contextError(ctx); err != nil {
		return pidregistry.StableProcessIdentity{}, err
	}
	captured, err := capture(pid)
	if err != nil {
		return pidregistry.StableProcessIdentity{}, classifyCaptureError(err)
	}
	if err := contextError(ctx); err != nil {
		return pidregistry.StableProcessIdentity{}, err
	}
	if err := matchCapturedIdentity(receipt, captured); err != nil {
		return pidregistry.StableProcessIdentity{}, err
	}
	return captured, nil
}

// verifyExecutableDigest 通过显式摘要 owner 验证可执行文件内容，不对路径字符串猜测摘要。
func verifyExecutableDigest(
	ctx context.Context,
	verifier ExecutableDigestVerifier,
	identity pidregistry.StableProcessIdentity,
	expected string,
) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := verifier.VerifyExecutableDigest(ctx, identity, expected); err != nil {
		return classifyDigestError(err)
	}
	return contextError(ctx)
}

func productionCaptureStableProcessIdentity(pid int) (pidregistry.StableProcessIdentity, error) {
	if runtime.GOOS == "windows" {
		return pidregistry.StableProcessIdentity{}, ErrProcessReceiptCaptureUnsupported
	}
	return pidregistry.CaptureStableProcessIdentity(pid)
}

// receiptPID 将合同中的 int64 PID 安全收窄为平台 int，拒绝溢出和无效 child PID。
func receiptPID(value int64) (int, error) {
	pid := int(value)
	if value <= 1 || int64(pid) != value {
		return 0, ErrProcessReceiptIdentityMismatch
	}
	return pid, nil
}

// matchCapturedIdentity 要求 capture 返回的 PID、start token 和 executable identity 全部精确命中。
func matchCapturedIdentity(receipt PreparedReceipt, captured pidregistry.StableProcessIdentity) error {
	want := receipt.Process
	pid, err := receiptPID(want.PID)
	if err != nil {
		return err
	}
	if captured.PID != pid ||
		captured.ProcessStartToken != want.ProcessStartToken ||
		captured.ExecutableIdentity != want.ExecutableIdentity {
		return errors.Join(ErrProcessReceiptIdentityMismatch, pidregistry.ErrStableProcessIdentityMismatch)
	}
	return nil
}

// sameCapturedIdentity 比较两次 capture 是否属于同一稳定进程代际。
func sameCapturedIdentity(left, right pidregistry.StableProcessIdentity) bool {
	return left.PID == right.PID &&
		left.ProcessStartToken == right.ProcessStartToken &&
		left.ExecutableIdentity == right.ExecutableIdentity
}

// classifyCaptureError 将底层 capture 错误归类为不泄漏路径的稳定哨兵。
func classifyCaptureError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, ErrProcessReceiptCaptureUnsupported):
		return ErrProcessReceiptCaptureUnsupported
	case errors.Is(err, pidregistry.ErrStableProcessNotFound):
		return pidregistry.ErrStableProcessNotFound
	case errors.Is(err, pidregistry.ErrStableProcessIdentityMismatch):
		return errors.Join(ErrProcessReceiptIdentityMismatch, pidregistry.ErrStableProcessIdentityMismatch)
	case errors.Is(err, pidregistry.ErrStableProcessIdentityRead):
		return pidregistry.ErrStableProcessIdentityRead
	default:
		return pidregistry.ErrStableProcessIdentityRead
	}
}

// classifyDigestError 将摘要 owner 错误归类为统一摘要不匹配错误。
func classifyDigestError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, ErrProductionNoGo) {
		return ErrProductionNoGo
	}
	return ErrProcessReceiptExecutableDigestMismatch
}

// contextError 仅返回标准取消类型，避免传播 context cause 中的敏感文本。
func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return context.Canceled
	}
	return nil
}

// interfaceIsNil 同时识别 nil interface 与承载 nil 指针的 typed nil。
func interfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
