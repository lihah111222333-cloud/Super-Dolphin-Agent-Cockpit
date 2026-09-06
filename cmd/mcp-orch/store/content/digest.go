package content

import (
	"crypto/sha256"
	"encoding/hex"
)

const prepareOperationDomain = "orch.content.prepare-operation/v1"

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// StablePrepareOperationID 从已提交ticket派生唯一外部I/O operation，阻止崩溃重试换nonce/object operation。
func StablePrepareOperationID(ticketID string) (string, error) {
	if err := validateContentText("ticket_id", ticketID, 128); err != nil {
		return "", err
	}
	return "content-prepare-v1:" + digestBytes([]byte(prepareOperationDomain+"\x00"+ticketID)), nil
}
