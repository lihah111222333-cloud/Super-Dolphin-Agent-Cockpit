//go:build linux

package multilsp

import "testing"

func closeResourceCohortClientForTest(t *testing.T, current *client) error {
	t.Helper()
	return current.Close()
}
