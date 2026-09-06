package managedruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	// PrepareRequestDigestDomain 隔离 PREPARE request 摘要。
	PrepareRequestDigestDomain = "orch.managed-runtime.prepare-request/v1"
	// PreparedReceiptDigestDomain 隔离 prepared receipt 摘要。
	PreparedReceiptDigestDomain = "orch.managed-runtime.prepared-receipt/v1"
	// CommitRequestDigestDomain 隔离 COMMIT request 摘要。
	CommitRequestDigestDomain = "orch.managed-runtime.commit-request/v1"
	// AbortRequestDigestDomain 隔离 ABORT request 摘要。
	AbortRequestDigestDomain = "orch.managed-runtime.abort-request/v1"
	// ClosedReceiptDigestDomain 隔离 COMMIT/ABORT closed receipt 摘要。
	ClosedReceiptDigestDomain = "orch.managed-runtime.closed-receipt/v1"
	// QueryResultDigestDomain 隔离 QUERY result 摘要。
	QueryResultDigestDomain = "orch.managed-runtime.query-result/v1"
)

// FinalizePrepareRequestDigest 清空旧自摘要并写回 PREPARE request digest。
func FinalizePrepareRequestDigest(request PrepareRequest) (PrepareRequest, error) {
	request.RequestDigest = ""
	digest, err := ComputePrepareRequestDigest(request)
	if err != nil {
		return PrepareRequest{}, err
	}
	request.RequestDigest = digest
	return request, nil
}

// ComputePrepareRequestDigest 绑定 bootstrap operation、全部启动面摘要与 writer epoch。
func ComputePrepareRequestDigest(request PrepareRequest) (string, error) {
	request.RequestDigest = ""
	return digestCanonical(PrepareRequestDigestDomain, request)
}

// FinalizePreparedReceiptDigest 清空旧自摘要并写回 prepared receipt digest。
func FinalizePreparedReceiptDigest(receipt PreparedReceipt) (PreparedReceipt, error) {
	receipt.ReceiptDigest = ""
	digest, err := ComputePreparedReceiptDigest(receipt)
	if err != nil {
		return PreparedReceipt{}, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

// ComputePreparedReceiptDigest 绑定 runtime、provider、稳定进程身份和 receipt ref。
func ComputePreparedReceiptDigest(receipt PreparedReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return digestCanonical(PreparedReceiptDigestDomain, receipt)
}

// FinalizeCommitRequestDigest 清空旧自摘要并写回 COMMIT request digest。
func FinalizeCommitRequestDigest(request CommitRequest) (CommitRequest, error) {
	request.RequestDigest = ""
	digest, err := ComputeCommitRequestDigest(request)
	if err != nil {
		return CommitRequest{}, err
	}
	request.RequestDigest = digest
	return request, nil
}

// ComputeCommitRequestDigest 绑定原 prepared receipt、active binding 与 credential 摘要。
func ComputeCommitRequestDigest(request CommitRequest) (string, error) {
	request.RequestDigest = ""
	return digestCanonical(CommitRequestDigestDomain, request)
}

// FinalizeAbortRequestDigest 清空旧自摘要并写回 ABORT request digest。
func FinalizeAbortRequestDigest(request AbortRequest) (AbortRequest, error) {
	request.RequestDigest = ""
	digest, err := ComputeAbortRequestDigest(request)
	if err != nil {
		return AbortRequest{}, err
	}
	request.RequestDigest = digest
	return request, nil
}

// ComputeAbortRequestDigest 绑定原 prepared receipt 与 cleanup evidence。
func ComputeAbortRequestDigest(request AbortRequest) (string, error) {
	request.RequestDigest = ""
	return digestCanonical(AbortRequestDigestDomain, request)
}

// FinalizeClosedReceiptDigest 清空旧自摘要并写回 closed receipt digest。
func FinalizeClosedReceiptDigest(receipt ClosedReceipt) (ClosedReceipt, error) {
	receipt.ReceiptDigest = ""
	digest, err := ComputeClosedReceiptDigest(receipt)
	if err != nil {
		return ClosedReceipt{}, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

// ComputeClosedReceiptDigest 绑定 prepared、closure evidence 与确定终态。
func ComputeClosedReceiptDigest(receipt ClosedReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return digestCanonical(ClosedReceiptDigestDomain, receipt)
}

// FinalizeQueryResultDigest 清空旧自摘要并写回 QUERY result digest。
func FinalizeQueryResultDigest(result QueryResult) (QueryResult, error) {
	result.ResultDigest = ""
	digest, err := ComputeQueryResultDigest(result)
	if err != nil {
		return QueryResult{}, err
	}
	result.ResultDigest = digest
	return result, nil
}

// ComputeQueryResultDigest 绑定原 operation/digest、关闭状态与其唯一 receipt 分支。
func ComputeQueryResultDigest(result QueryResult) (string, error) {
	result.ResultDigest = ""
	return digestCanonical(QueryResultDigestDomain, result)
}

func digestCanonical(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", fmt.Errorf("encode managed runtime digest payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
