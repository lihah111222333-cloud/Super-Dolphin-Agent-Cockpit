package godistribution

import "testing"

func TestLockedAssetsMatchOfficialGo1265Archives(t *testing.T) {
	want := map[string]Asset{
		"darwin/arm64": {Version: Version, GOOS: "darwin", GOARCH: "arm64", URL: "https://go.dev/dl/go1.26.5.darwin-arm64.tar.gz", SHA256: "efb87ff28af9a188d0536ef5d42e63dd52ba8263cd7344a993cc48dd11dedb6a", Size: 64738542},
		"darwin/amd64": {Version: Version, GOOS: "darwin", GOARCH: "amd64", URL: "https://go.dev/dl/go1.26.5.darwin-amd64.tar.gz", SHA256: "6231d8d3b8f5552ec6cbf6d685bdd5482e1e703214b120e89b3bf0d7bf1ef725", Size: 67836304},
		"linux/amd64":  {Version: Version, GOOS: "linux", GOARCH: "amd64", URL: "https://go.dev/dl/go1.26.5.linux-amd64.tar.gz", SHA256: "5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053", Size: 66879095},
	}
	for platform, expected := range want {
		got, err := Lookup(expected.GOOS, expected.GOARCH)
		if err != nil {
			t.Fatalf("Lookup(%s): %v", platform, err)
		}
		if got != expected {
			t.Fatalf("Lookup(%s) = %#v, want %#v", platform, got, expected)
		}
	}
}

func TestLookupRejectsUnlockedPlatform(t *testing.T) {
	if _, err := Lookup("linux", "arm64"); err == nil {
		t.Fatal("Lookup(linux, arm64) unexpectedly succeeded")
	}
}

func TestRemoteCIOnlyAcceptsLinuxAMD64Asset(t *testing.T) {
	remote, err := RemoteCIAsset()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRemoteCIAsset(remote); err != nil {
		t.Fatalf("ValidateRemoteCIAsset(remote): %v", err)
	}
	darwin, err := Lookup("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRemoteCIAsset(darwin); err == nil {
		t.Fatal("ValidateRemoteCIAsset(darwin) unexpectedly succeeded")
	}
}

func TestParseRejectsNonOfficialOrIncompleteLock(t *testing.T) {
	if _, err := parse("go1.26.5\tlinux\tamd64\thttps://example.invalid/go.tar.gz\t5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053\t66879095\n"); err == nil {
		t.Fatal("parse(non-official URL) unexpectedly succeeded")
	}
}
