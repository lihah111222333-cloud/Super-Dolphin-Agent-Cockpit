package main

import (
	"strings"
	"testing"
)

func TestSelectDirectReverseDependentPackages(t *testing.T) {
	const modulePath = "example.com/project"
	listing := strings.Join([]string{
		"example.com/project/internal/changed\t\t\t",
		"example.com/project/internal/direct\texample.com/project/internal/changed\t\t",
		"example.com/project/internal/testonly\t\texample.com/project/internal/changed\t",
		"example.com/project/internal/transitive\texample.com/project/internal/direct\t\t",
		"example.com/project/internal/archtest\texample.com/project/internal/changed\t\t",
		"example.net/external\texample.com/project/internal/changed\t\t",
	}, "\n")

	got, err := selectDirectReverseDependentPackages(modulePath, []string{"./internal/changed"}, []byte(listing))
	if err != nil {
		t.Fatal(err)
	}
	assertStringSetContains(t, got, "./internal/changed", "./internal/direct", "./internal/testonly")
	assertStringSetOmits(t, got, "./internal/transitive", "./internal/archtest", "example.net/external")
}

func TestSelectDirectReverseDependentPackagesFailsClosedOnMalformedListing(t *testing.T) {
	if _, err := selectDirectReverseDependentPackages("example.com/project", []string{"./internal/changed"}, []byte("malformed")); err == nil {
		t.Fatal("malformed go list output was accepted")
	}
}
