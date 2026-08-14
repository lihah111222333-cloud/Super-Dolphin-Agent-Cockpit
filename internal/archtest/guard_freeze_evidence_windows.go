//go:build windows

package archtest

import "bytes"

// normalizeGuardFreezeEvidenceBytes 还原 Git 文本证据的规范 LF 字节。
// Windows checkout 可能按 core.autocrlf 展开为 CRLF，但 acceptance 哈希绑定的是
// 仓库对象中的 LF 内容；只规范 CRLF，不接受孤立 CR 或其他字节漂移。
func normalizeGuardFreezeEvidenceBytes(body []byte) []byte {
	return bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
}
