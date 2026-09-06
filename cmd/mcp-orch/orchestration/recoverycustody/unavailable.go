package recoverycustody

import (
	"context"
)

// UnavailableClient 是唯一当前可用于 production composition 的 adapter；它不持久化或伪造 receipt。
type UnavailableClient struct{}

// NewUnavailableClient 构造显式不可用 adapter，不读取本地 SQLite、file、WAL 或 heteroci authority。
func NewUnavailableClient() *UnavailableClient { return &UnavailableClient{} }

// Submit 返回稳定 handle + indeterminate，并明确报 authority unavailable。
func (*UnavailableClient) Submit(ctx context.Context, request SubmissionRequest) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	handle, err := NewSubmissionHandle(request)
	if err != nil {
		return Outcome{}, err
	}
	result := Outcome{Handle: handle, Status: StatusIndeterminate, ReasonCode: "recovery_custody_unavailable"}
	return result, ErrCustodyUnavailable
}

// Query 只回显原 handle 的 indeterminate，不创建新 operation 或推断 abort。
func (*UnavailableClient) Query(ctx context.Context, handle SubmissionHandle) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if err := handle.Validate(); err != nil {
		return Outcome{}, err
	}
	result := Outcome{Handle: handle, Status: StatusIndeterminate, ReasonCode: "recovery_custody_unavailable"}
	return result, ErrCustodyUnavailable
}

// RejectAllReceiptVerifier 禁止当前进程把任何本地/远端形状数据提升为 authoritative receipt。
type RejectAllReceiptVerifier struct{}

// VerifyReceipt 永远失败；未来 external verifier 必须由独立 authority package 实现并显式装配。
func (RejectAllReceiptVerifier) VerifyReceipt(context.Context, SubmissionHandle, ExternalReceipt) error {
	return ErrCustodyUnavailable
}

var _ Client = (*UnavailableClient)(nil)
var _ ReceiptVerifier = RejectAllReceiptVerifier{}
