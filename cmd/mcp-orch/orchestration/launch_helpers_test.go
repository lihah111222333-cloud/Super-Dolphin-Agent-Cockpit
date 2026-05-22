package orchestration

import "testing"

func TestValidateLaunchRequestForLauncher(t *testing.T) {
	cwd := testCWD(t, "repo")
	tests := []struct {
		name     string
		req      LaunchRequest
		launcher AgentLauncher
		wantErr  string
	}{
		{
			name:     "remote launcher skips command",
			req:      LaunchRequest{AgentID: "agent-1", Cwd: cwd},
			launcher: &remoteLauncher{},
		},
		{
			name:     "remote launcher still requires agent id",
			req:      LaunchRequest{},
			launcher: &remoteLauncher{},
			wantErr:  "agent id is required",
		},
		{
			name:     "local launcher requires command",
			req:      LaunchRequest{AgentID: "agent-1", Cwd: cwd},
			launcher: &localLauncher{},
			wantErr:  "command is required",
		},
		{
			name:    "nil launcher still validates agent id",
			req:     LaunchRequest{},
			wantErr: "agent id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLaunchRequestForLauncher(tt.req, tt.launcher)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateLaunchRequestForLauncher() error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("validateLaunchRequestForLauncher() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
