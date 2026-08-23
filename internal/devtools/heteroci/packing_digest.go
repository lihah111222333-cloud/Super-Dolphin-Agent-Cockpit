package heteroci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// CanonicalDigest 返回冻结装箱规划 wire JSON 的 SHA-256 摘要。
func (plan PackingPlan) CanonicalDigest() (string, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal packing plan: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
