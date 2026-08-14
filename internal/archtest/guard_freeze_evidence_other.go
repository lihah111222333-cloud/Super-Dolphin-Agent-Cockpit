//go:build !windows

package archtest

// normalizeGuardFreezeEvidenceBytes 在非 Windows 平台保持原始字节哈希语义。
func normalizeGuardFreezeEvidenceBytes(body []byte) []byte {
	return body
}
