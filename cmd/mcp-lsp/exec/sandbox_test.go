package exec

import (
	"strings"
	"testing"
)

func TestLimitedBufferKeepsTailAfterTruncation(t *testing.T) {
	buf := newLimitedBuffer(16)
	_, _ = buf.Write([]byte("0123456789abcdef"))
	_, _ = buf.Write([]byte("FATAL_SENTINEL"))

	out := buf.String()
	if !strings.Contains(out, "FATAL_SENTINEL") {
		t.Fatalf("limitedBuffer output = %q, want tail sentinel", out)
	}
}
