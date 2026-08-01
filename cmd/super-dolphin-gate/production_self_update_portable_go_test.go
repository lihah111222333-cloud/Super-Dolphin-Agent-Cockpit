package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveProductionGoToolchainBootstrapsOnlyAfterCandidateExhaustion(t *testing.T) {
	fixture := newProductionGoResolverFixture(t)
	fixture.environment["PATH"] = fixture.brokenDirectory
	deps := fixture.deps()
	called := 0
	deps.bootstrap = func(requirement productionGoRequirement) (productionGoToolchain, error) {
		called++
		if requirement != fixture.requirement() {
			t.Fatalf("bootstrap requirement = %#v", requirement)
		}
		return productionGoToolchain{Executable: fixture.executable, Version: "go version go1.26.5 " + portableGoTestPlatform()}, nil
	}
	if _, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), deps); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("bootstrap calls = %d, want 1", called)
	}

	fixture.environment["SUPER_DOLPHIN_GATE_GO"] = fixture.brokenExecutable
	called = 0
	if _, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), deps); err == nil || called != 0 {
		t.Fatalf("explicit candidate called bootstrap=%d, err=%v", called, err)
	}
}

func TestExtractPortableGoArchiveRejectsUnsafeEntries(t *testing.T) {
	cases := []struct {
		name    string
		header  tar.Header
		wantErr bool
	}{
		{name: "root directory", header: tar.Header{Name: "go/", Mode: 0o700, Typeflag: tar.TypeDir}},
		{name: "regular", header: tar.Header{Name: "go/VERSION", Mode: 0o600, Typeflag: tar.TypeReg}},
		{name: "wrong top level", header: tar.Header{Name: "other/VERSION", Mode: 0o600, Typeflag: tar.TypeReg}, wantErr: true},
		{name: "escape", header: tar.Header{Name: "go/../../escape", Mode: 0o600, Typeflag: tar.TypeReg}, wantErr: true},
		{name: "symlink", header: tar.Header{Name: "go/link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"}, wantErr: true},
		{name: "device", header: tar.Header{Name: "go/device", Typeflag: tar.TypeChar}, wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			archive := writePortableGoTestArchive(t, test.header)
			destination := filepath.Join(t.TempDir(), "destination")
			err := extractPortableGoArchive(archive, destination)
			if (err != nil) != test.wantErr {
				t.Fatalf("extract error = %v, wantErr %v", err, test.wantErr)
			}
			if !test.wantErr && test.header.Typeflag == tar.TypeReg {
				data, readErr := os.ReadFile(filepath.Join(destination, "go", "VERSION"))
				if readErr != nil || string(data) != "fixture" {
					t.Fatalf("extracted VERSION = %q, error = %v", data, readErr)
				}
			}
		})
	}
}

func TestPortableGoManifestSupportsOnlyProductionPlatforms(t *testing.T) {
	want := map[string]portableGoAsset{
		"darwin/amd64": {"https://go.dev/dl/go1.26.5.darwin-amd64.tar.gz", "6231d8d3b8f5552ec6cbf6d685bdd5482e1e703214b120e89b3bf0d7bf1ef725", 67836304},
		"darwin/arm64": {"https://go.dev/dl/go1.26.5.darwin-arm64.tar.gz", "efb87ff28af9a188d0536ef5d42e63dd52ba8263cd7344a993cc48dd11dedb6a", 64738542},
		"linux/amd64":  {"https://go.dev/dl/go1.26.5.linux-amd64.tar.gz", "5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053", 66879095},
	}
	if len(portableGoAssets) != len(want) {
		t.Fatalf("portable Go manifest has %d assets, want %d", len(portableGoAssets), len(want))
	}
	for platform, expected := range want {
		if asset := portableGoAssets[platform]; asset != expected {
			t.Fatalf("manifest asset for %s = %#v, want %#v", platform, asset, expected)
		}
	}
	if _, err := portableGoAssetForPlatform("windows", "amd64"); err == nil {
		t.Fatal("Windows unexpectedly has a portable archive")
	}
	if _, err := portableGoAssetForPlatform("darwin", "386"); err == nil {
		t.Fatal("unsupported architecture unexpectedly has a portable archive")
	}
	if _, err := portableGoAssetForPlatform("linux", "arm64"); err == nil {
		t.Fatal("linux/arm64 unexpectedly has a portable archive")
	}
}

func TestPortableGoRedirectAllowsOnlyOfficialExactArtifact(t *testing.T) {
	asset := portableGoAssets["darwin/arm64"]
	originURL, err := url.Parse(asset.URL)
	if err != nil {
		t.Fatal(err)
	}
	origin := &http.Request{URL: originURL}
	for name, target := range map[string]string{
		"http downgrade": "http://dl.google.com/go/go1.26.5.darwin-arm64.tar.gz",
		"wrong host":     "https://example.com/go/go1.26.5.darwin-arm64.tar.gz",
		"wrong artifact": "https://dl.google.com/go/go1.26.5.darwin-amd64.tar.gz",
		"query":          "https://dl.google.com/go/go1.26.5.darwin-arm64.tar.gz?mirror=1",
	} {
		t.Run(name, func(t *testing.T) {
			targetURL, parseErr := url.Parse(target)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if portableGoRedirectAllowed(asset, &http.Request{URL: targetURL}, []*http.Request{origin}) {
				t.Fatalf("redirect target %q was accepted", target)
			}
		})
	}
	targetURL, err := url.Parse("https://dl.google.com/go/go1.26.5.darwin-arm64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if !portableGoRedirectAllowed(asset, &http.Request{URL: targetURL}, []*http.Request{origin}) {
		t.Fatal("exact official redirect was rejected")
	}
}

func TestPublishPortableGoRollsBackAndRecoversInterruptedBackup(t *testing.T) {
	root := t.TempDir()
	asset := portableGoAssets["darwin/arm64"]
	requirement := productionGoRequirement{Minimum: portableGoVersion, Preferred: portableGoVersion}
	install := filepath.Join(root, "go1.26.5", "darwin-arm64")
	if err := os.MkdirAll(install, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(install, "old-marker")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishPortableGo(filepath.Join(root, "missing-content"), install, asset, requirement); err == nil {
		t.Fatal("publish unexpectedly succeeded with missing content")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "old" {
		t.Fatalf("rollback marker = %q, error = %v", data, err)
	}

	backup := install + ".previous"
	if err := os.Rename(install, backup); err != nil {
		t.Fatal(err)
	}
	if err := recoverPortableGoBackup(install, asset, requirement); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "old" {
		t.Fatalf("recovered marker = %q, error = %v", data, err)
	}
	if err := os.Rename(install, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(install, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "damaged"), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recoverPortableGoBackup(install, asset, requirement); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "old" {
		t.Fatalf("damaged install recovery marker = %q, error = %v", data, err)
	}
}

func writePortableGoTestArchive(t *testing.T, header tar.Header) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "fixture.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	if header.Typeflag == tar.TypeReg {
		header.Size = int64(len("fixture"))
	}
	if err := tarWriter.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if header.Typeflag == tar.TypeReg {
		if _, err := tarWriter.Write([]byte("fixture")); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tarWriter.Close(), gzipWriter.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	return archive
}

func portableGoTestPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }
