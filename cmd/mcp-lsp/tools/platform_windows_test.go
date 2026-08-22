//go:build windows

package tools

import "testing"

func TestSameDiagnosticURIUsesWindowsPathIdentity(t *testing.T) {
	if !sameDiagnosticURI("file:///c%3A/Work/main.mq4", "file:///C:/work/main.mq4") {
		t.Fatal("equivalent Windows diagnostic URIs did not match")
	}
}
