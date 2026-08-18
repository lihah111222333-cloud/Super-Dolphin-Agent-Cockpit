//go:build windows

package tools

import "testing"

func TestFileReadKeepsNormalTimeoutTier(t *testing.T) {
	if _, ok := fileToolDeadlineForAction(t, "read_file"); ok {
		t.Fatal("Windows file read_file received an outer tool deadline")
	}
}

func TestSameDiagnosticURIUsesWindowsPathIdentity(t *testing.T) {
	if !sameDiagnosticURI("file:///c%3A/Work/main.mq4", "file:///C:/work/main.mq4") {
		t.Fatal("equivalent Windows diagnostic URIs did not match")
	}
}
