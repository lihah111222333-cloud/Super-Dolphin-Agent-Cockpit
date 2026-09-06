package rpcwire

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

// ValidateForClient 验证响应形状，并对 terminal outcome 强制执行调用方提供的 receipt verifier。
func (r Result) ValidateForClient(ctx context.Context, method Method, verifier recoverycustody.ReceiptVerifier) error {
	if ctx == nil {
		return recoverycustody.ErrContextRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.ValidateForMethod(method); err != nil {
		return err
	}
	if r.Outcome == nil || r.Outcome.Receipt == nil {
		return nil
	}
	pending := recoverycustody.Outcome{Handle: r.Outcome.Handle, Status: recoverycustody.StatusPending}
	verified, err := recoverycustody.ApplyVerifiedReceipt(ctx, pending, *r.Outcome.Receipt, verifier)
	if err != nil {
		return fmt.Errorf("%w: terminal receipt verifier rejected result", ErrInvalidWire)
	}
	if verified.Status != r.Outcome.Status || verified.Receipt == nil ||
		verified.Receipt.ReceiptDigest != r.Outcome.Receipt.ReceiptDigest {
		return fmt.Errorf("%w: terminal verifier result mismatch", ErrInvalidWire)
	}
	return nil
}
