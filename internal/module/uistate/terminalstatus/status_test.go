package terminalstatus

import "testing"

// TestTurnTerminalStatus 固定 turn 完成事件在主投影和 timeline 中共享的终态语义。
func TestTurnTerminalStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		success bool
		status  string
		reason  string
		err     string
		want    string
	}{
		{
			name:    "successful turn defaults to completed",
			success: true,
			want:    "completed",
		},
		{
			name:    "failed turn without provider diagnostic is still failed",
			success: false,
			want:    "failed",
		},
		{
			name:    "failed turn with error is failed",
			success: false,
			err:     "boom",
			want:    "failed",
		},
		{
			name:    "explicit provider status is preserved",
			success: false,
			status:  "interrupted",
			want:    "interrupted",
		},
		{
			name:    "explicit provider status is trimmed",
			success: true,
			status:  " completed ",
			want:    "completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Status(tt.success, tt.status, tt.reason, tt.err); got != tt.want {
				t.Fatalf("Status(%v, %q, %q, %q) = %q, want %q",
					tt.success, tt.status, tt.reason, tt.err, got, tt.want)
			}
		})
	}
}
