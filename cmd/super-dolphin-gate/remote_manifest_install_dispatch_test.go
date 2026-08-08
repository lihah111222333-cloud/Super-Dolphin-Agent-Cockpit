package main

import (
	"io"
	"strings"
	"testing"
)

func TestDispatchPrimaryCLIRoutesRemoteManifestInstaller(t *testing.T) {
	handled, err := dispatchPrimaryCLI([]string{"_remote-install-manifest", "unexpected"}, io.Discard)
	if !handled {
		t.Fatal("_remote-install-manifest was not handled")
	}
	if err == nil || !strings.Contains(err.Error(), "does not accept arguments") {
		t.Fatalf("_remote-install-manifest error = %v", err)
	}
}
