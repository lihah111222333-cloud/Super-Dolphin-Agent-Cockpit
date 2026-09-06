package managedlaunch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	BootstrapRequestDigestDomain = "orch.managed-launch.bootstrap-request/v1"
	BootstrapResultDigestDomain  = "orch.managed-launch.bootstrap-result/v1"
	CommitOperationDigestDomain  = "orch.managed-launch.commit-operation/v1"
	AbortOperationDigestDomain   = "orch.managed-launch.abort-operation/v1"
)

// FinalizeBootstrapRequestDigest 计算并写回 bootstrap request 摘要；旧 self-digest 不参与计算。
func FinalizeBootstrapRequestDigest(request BootstrapRequest) (BootstrapRequest, error) {
	request.BootstrapRequestDigest = ""
	digest, err := ComputeBootstrapRequestDigest(request)
	if err != nil {
		return BootstrapRequest{}, err
	}
	request.BootstrapRequestDigest = digest
	return request, nil
}

// ComputeBootstrapRequestDigest 使用固定 struct JSON 和独立 domain 绑定全部 bootstrap 字段。
func ComputeBootstrapRequestDigest(request BootstrapRequest) (string, error) {
	request.BootstrapRequestDigest = ""
	return digestCanonical(BootstrapRequestDigestDomain, request)
}

// FinalizeBootstrapResultDigest 计算并写回 prepared result 摘要。
func FinalizeBootstrapResultDigest(result BootstrapResult) (BootstrapResult, error) {
	result.ResultDigest = ""
	digest, err := ComputeBootstrapResultDigest(result)
	if err != nil {
		return BootstrapResult{}, err
	}
	result.ResultDigest = digest
	return result, nil
}

// ComputeBootstrapResultDigest 绑定 receipt owner 声明，不对声明做外部验真。
func ComputeBootstrapResultDigest(result BootstrapResult) (string, error) {
	result.ResultDigest = ""
	return digestCanonical(BootstrapResultDigestDomain, result)
}

// FinalizeCommitOperationDigest 计算并写回 commit operation 摘要。
func FinalizeCommitOperationDigest(request CommitRequest) (CommitRequest, error) {
	request.CommitOperationDigest = ""
	digest, err := ComputeCommitOperationDigest(request)
	if err != nil {
		return CommitRequest{}, err
	}
	request.CommitOperationDigest = digest
	return request, nil
}

// ComputeCommitOperationDigest 以独立 domain 绑定原 prepared request/result、current binding 与 activated credential。
func ComputeCommitOperationDigest(request CommitRequest) (string, error) {
	request.CommitOperationDigest = ""
	return digestCanonical(CommitOperationDigestDomain, request)
}

// FinalizeAbortOperationDigest 计算并写回 abort operation 摘要。
func FinalizeAbortOperationDigest(request AbortRequest) (AbortRequest, error) {
	request.AbortOperationDigest = ""
	digest, err := ComputeAbortOperationDigest(request)
	if err != nil {
		return AbortRequest{}, err
	}
	request.AbortOperationDigest = digest
	return request, nil
}

// ComputeAbortOperationDigest 以独立 domain 绑定原 prepared request/result 与 cleanup operation/evidence。
func ComputeAbortOperationDigest(request AbortRequest) (string, error) {
	request.AbortOperationDigest = ""
	return digestCanonical(AbortOperationDigestDomain, request)
}

func digestCanonical(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", fmt.Errorf("encode managed launch digest payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
