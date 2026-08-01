package main

import "testing"

func TestRemoteBaselineToolchainDigestIncludesProjectGoVersion(t *testing.T) {
	lock := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first := remoteBaselineToolchainDigest(lock, "go1.25.6")
	second := remoteBaselineToolchainDigest(lock, "go1.26.5")
	if first == second {
		t.Fatal("project Go version change did not invalidate baseline toolchain identity")
	}
	if second != remoteBaselineToolchainDigest(lock, "go1.26.5") {
		t.Fatal("baseline toolchain identity is not deterministic")
	}
}
