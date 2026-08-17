//go:build windows

package main

import "testing"

func TestRuntimeServerWindowsKotlinProcessBinaryLeavesExternalOtherBinaryUnchanged(t *testing.T) {
	got, handled, err := runtimeServerWindowsKotlinProcessBinary(`C:\external\other-language-server.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if handled || got != `C:\external\other-language-server.exe` {
		t.Fatalf("non-Kotlin binary changed: got=%q handled=%t", got, handled)
	}
}
