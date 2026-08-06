package runtimeenv

import (
	"reflect"
	"testing"
)

func TestDetachedCommandEnvironmentKeepsOnlyAllowlistedVariables(t *testing.T) {
	got, err := DetachedCommandEnvironment([]string{
		"HOME=/Users/alice",
		"SECRET=must-not-leak",
		"LANG=en_US.UTF-8",
		"LC_ALL=C",
		"PATH=/usr/bin",
		"TMPDIR=/tmp/super-dolphin",
	})
	if err != nil {
		t.Fatalf("DetachedCommandEnvironment() error = %v", err)
	}
	want := []string{
		"HOME=/Users/alice",
		"LANG=en_US.UTF-8",
		"LC_ALL=C",
		"PATH=/usr/bin",
		"TMPDIR=/tmp/super-dolphin",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetachedCommandEnvironment() = %v, want %v", got, want)
	}
}

func TestDetachedCommandEnvironmentRejectsMalformedAndDuplicateAllowlistedVariables(t *testing.T) {
	for _, environ := range [][]string{
		{"HOME=/Users/alice", "HOME=/Users/bob"},
		{"not-an-environment-entry"},
	} {
		if _, err := DetachedCommandEnvironment(environ); err == nil {
			t.Fatalf("DetachedCommandEnvironment(%v) error = nil, want error", environ)
		}
	}
}
