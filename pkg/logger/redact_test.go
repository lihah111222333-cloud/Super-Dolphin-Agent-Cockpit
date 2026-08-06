package logger

import "testing"

func TestRedactLogStringDatabasePathBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "db file",
			input: "/Users/alice/private/super-dolphin.db",
			want:  redactedValue,
		},
		{
			name:  "db wal file",
			input: "/Users/alice/private/super-dolphin.db-wal",
			want:  redactedValue,
		},
		{
			name:  "db shm file",
			input: "/Users/alice/private/super-dolphin.db-shm",
			want:  redactedValue,
		},
		{
			name:  "ordinary adobe path",
			input: "/workspace/adobe/file.txt",
			want:  "/workspace/adobe/file.txt",
		},
		{
			name:  "dbg directory",
			input: "/workspace/name.dbg/file.txt",
			want:  "/workspace/name.dbg/file.txt",
		},
		{
			name:  "db backup directory",
			input: "/workspace/name.db.backup/file.txt",
			want:  "/workspace/name.db.backup/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactLogString(tt.input); got != tt.want {
				t.Fatalf("redactLogString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRuntimeRedactorsAreMutationIsolated(t *testing.T) {
	first := NewRuntime(RuntimeConfig{})
	second := NewRuntime(RuntimeConfig{})

	if first.redactor == second.redactor || first.redactor.patterns[0].pattern == second.redactor.patterns[0].pattern {
		t.Fatal("logger runtimes share mutable redactor state")
	}
	first.redactor.patterns[0].replacement = "MUTATED"
	first.redactor.markers[0] = "not_token"

	if got := second.redactor.redactString("Authorization: Bearer private-token"); got != "Authorization: Bearer "+redactedValue {
		t.Fatalf("second runtime redaction = %q, want isolated replacement", got)
	}
	if !second.redactor.secretLikeKey("access_token") {
		t.Fatal("second runtime marker set was mutated through first runtime")
	}
}
