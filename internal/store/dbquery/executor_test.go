package dbquery

import "testing"

func TestValidateQuery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		argCount  int
		wantError bool
	}{
		{
			name:     "allows whitelisted cte query",
			query:    "WITH running AS (SELECT thread_id FROM agent_threads WHERE status = $1) SELECT thread_id FROM running",
			argCount: 1,
		},
		{
			name:      "rejects mutation statement",
			query:     "UPDATE agent_threads SET status = $1 WHERE thread_id = $2",
			argCount:  2,
			wantError: true,
		},
		{
			name:      "rejects disallowed table",
			query:     "SELECT * FROM pg_stat_activity WHERE pid = $1",
			argCount:  1,
			wantError: true,
		},
		{
			name:      "rejects placeholder mismatch",
			query:     "SELECT * FROM agent_threads WHERE status = $2",
			argCount:  1,
			wantError: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateQuery(tc.query, tc.argCount)
			if (err != nil) != tc.wantError {
				t.Fatalf("validateQuery() error = %v, wantError %v", err, tc.wantError)
			}
		})
	}
}
