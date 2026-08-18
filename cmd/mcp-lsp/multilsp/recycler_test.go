package multilsp

import (
	"testing"
)

func TestRecyclerRSSLimitSelection(t *testing.T) {
	t.Setenv(lspGoRSSLimitEnv, "")
	want := expectedGoRSSLimit()
	if got := rssLimitBytesForLanguage("go"); got != want {
		t.Fatalf("gopls RSS limit=%d, want %d", got, want)
	}
}
