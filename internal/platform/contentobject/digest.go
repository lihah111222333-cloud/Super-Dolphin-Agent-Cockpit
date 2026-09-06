package contentobject

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const putRequestDigestDomain = "orch.content-object.put/v1"
const putReceiptDigestDomain = "orch.content-object.receipt/v1"

// FinalizePutRequest 计算并写回不含密文字节、但绑定密文hash/size的request digest。
func FinalizePutRequest(request PutRequest) (PutRequest, error) {
	request.RequestDigest = ""
	digest, err := ComputePutRequestDigest(request)
	if err != nil {
		return PutRequest{}, err
	}
	request.RequestDigest = digest
	return request, nil
}

// ComputePutRequestDigest 计算原object operation的domain-separated digest。
func ComputePutRequestDigest(request PutRequest) (string, error) {
	request.RequestDigest = ""
	request.Ciphertext = nil
	return digestObject(putRequestDigestDomain, request)
}

func computeReceiptDigest(receipt PutReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return digestObject(putReceiptDigestDomain, receipt)
}

func digestObject(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", fmt.Errorf("encode content object digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func hashCiphertext(ciphertext []byte) string {
	sum := sha256.Sum256(ciphertext)
	return hex.EncodeToString(sum[:])
}
