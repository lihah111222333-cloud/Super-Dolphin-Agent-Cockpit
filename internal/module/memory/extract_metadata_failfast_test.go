package memory

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestDecodeStoredThreadRuntimeFailsClosedOnMalformedConfigOverride(t *testing.T) {
	t.Parallel()

	_, err := decodeStoredThreadRuntime(&contract.ThreadMetadata{
		ThreadID:       "thread-1",
		ConfigOverride: []byte(`{"runtime":`),
	})
	if err == nil || !strings.Contains(err.Error(), "config_override.runtime") {
		t.Fatalf("decodeStoredThreadRuntime() error = %v, want malformed config_override.runtime error", err)
	}
}
