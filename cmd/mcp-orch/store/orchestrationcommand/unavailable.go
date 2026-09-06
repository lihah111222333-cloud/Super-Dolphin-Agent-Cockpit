package orchestrationcommand

import (
	"context"
	"errors"
)

var (
	// ErrReceiptVerifierUnavailable 表示当前进程没有权威 command receipt verifier。
	ErrReceiptVerifierUnavailable = errors.New("command receipt verifier is unavailable")
	// ErrContextRequired 表示命令 owner 的任何读写或验证调用都必须提供非 nil context。
	ErrContextRequired = errors.New("command intent context is required")
)

// UnavailableReceiptVerifier 是默认的 command receipt verifier seam；它永不放行转换。
// 真实 verifier 必须由独立 receipt authority 提供，并显式替换此候选实现。
type UnavailableReceiptVerifier struct{}

// NewUnavailableReceiptVerifier 构造默认 fail-closed verifier，不读取数据库或调用 provider。
func NewUnavailableReceiptVerifier() *UnavailableReceiptVerifier {
	return &UnavailableReceiptVerifier{}
}

// VerifyCommandTransition 仅检查 context，然后拒绝所有没有权威回执的转换。
func (UnavailableReceiptVerifier) VerifyCommandTransition(ctx context.Context, _ Verification) error {
	if ctx == nil {
		return ErrContextRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrReceiptVerifierUnavailable
}

var _ ReceiptVerifier = UnavailableReceiptVerifier{}
