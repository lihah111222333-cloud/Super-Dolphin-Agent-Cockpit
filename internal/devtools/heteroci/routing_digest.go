package heteroci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// CanonicalDigest 返回清空 DecisionDigest 后冻结路由决策 wire 结构的 SHA-256 摘要。
func (decision RoutingDecision) CanonicalDigest() (string, error) {
	material := decision
	material.DecisionDigest = ""
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal routing decision for canonical digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
