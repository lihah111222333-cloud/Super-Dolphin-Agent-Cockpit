//go:build windows && e2e

package archtest

import (
	"bytes"
	"testing"
)

// TestWindowsGuardFreezeEvidenceNormalizesGitCRLFE2E 锁定 Windows checkout
// 只消除 Git CRLF 展开，不吞掉孤立 CR 或其他内容变化。
func TestWindowsGuardFreezeEvidenceNormalizesGitCRLFE2E(t *testing.T) {
	canonical := []byte("source_head: fixture\nexpected_exit: 1\n")
	checkout := []byte("source_head: fixture\r\nexpected_exit: 1\r\n")
	if got := normalizeGuardFreezeEvidenceBytes(checkout); !bytes.Equal(got, canonical) {
		t.Fatalf("normalizeGuardFreezeEvidenceBytes() = %q, want %q", got, canonical)
	}
	isolatedCR := []byte("source_head: fixture\rexpected_exit: 1\n")
	if got := normalizeGuardFreezeEvidenceBytes(isolatedCR); !bytes.Equal(got, isolatedCR) {
		t.Fatalf("isolated CR was silently normalized: got %q", got)
	}
}
