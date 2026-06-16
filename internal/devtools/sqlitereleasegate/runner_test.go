package sqlitereleasegate

import "testing"

// TestWriteGateLogReturnsWriteError verifies raw log persistence failures do not panic.
func TestWriteGateLogReturnsWriteError(t *testing.T) {
	t.Parallel()

	err := writeGateLog(t.TempDir(), []byte("raw log"))
	if err == nil {
		t.Fatal("writeGateLog() error = nil, want write error")
	}
}
