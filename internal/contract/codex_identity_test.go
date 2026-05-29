package contract

import (
	"strings"
	"testing"
)

func TestAppManagedCodexHomeRequiresRuntimeHome(t *testing.T) {
	t.Setenv(SuperDolphinHomeEnv, "")

	got, err := AppManagedCodexHome()
	if err == nil || !strings.Contains(err.Error(), SuperDolphinHomeEnv) {
		t.Fatalf("AppManagedCodexHome() = %q, %v; want missing runtime home error", got, err)
	}
}
