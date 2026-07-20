package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportWorkflowTrustedMirrorImportsCurrentEventBeyondBaseline(t *testing.T) {
	fixture := newProductionTestFixture(t)
	eventSHA, eventTree := commitWorkflowCandidate(t, fixture.sourceRepo)
	if eventSHA == fixture.commit {
		t.Fatal("candidate SHA unexpectedly equals signed baseline")
	}
	root, err := loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	root.RemoteURL = fixture.sourceRepo
	mirror := workflowPrivateDirectory(t)
	if err := importWorkflowTrustedMirror(context.Background(), root, mirror, "refs/heads/candidate", eventSHA, eventTree); err != nil {
		t.Fatal(err)
	}
	assertImportedWorkflowMirror(t, mirror, root, eventSHA)
	trustedSource, cleanup, err := checkoutWorkflowTrustedSource(context.Background(), mirror, root.ObjectFormat, eventSHA, eventTree)
	if err != nil {
		t.Fatal(err)
	}
	trustedSourceRoot := filepath.Dir(trustedSource)
	assertWorkflowTrustedSourceIdentity(t, trustedSource, eventSHA, eventTree)
	assertWorkflowTrustedSourceFilesystem(t, trustedSource)
	assertWorkflowTrustedSourceCleanup(t, trustedSourceRoot, cleanup)
}

func TestCheckoutWorkflowTrustedSourceRejectsNonCanonicalEventSHA(t *testing.T) {
	fixture := newProductionTestFixture(t)
	root, err := loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := checkoutWorkflowTrustedSource(context.Background(), "", root.ObjectFormat, "--orphan", ""); err == nil {
		t.Fatal("checkout accepted non-canonical event SHA")
	}
}

func commitWorkflowCandidate(t *testing.T, repository string) (string, string) {
	t.Helper()
	productionGitLine(t, repository, "checkout", "-qb", "candidate")
	if err := os.WriteFile(filepath.Join(repository, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("candidate.txt", filepath.Join(repository, "candidate-link")); err != nil {
		t.Fatal(err)
	}
	productionGitLine(t, repository, "add", "--", "candidate.txt", "candidate-link")
	productionGitLine(t, repository, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "candidate")
	return productionGitLine(t, repository, "rev-parse", "HEAD^{commit}"), productionGitLine(t, repository, "rev-parse", "HEAD^{tree}")
}

func assertImportedWorkflowMirror(t *testing.T, mirror string, root productionBootstrapRoot, eventSHA string) {
	t.Helper()
	if observed := productionGitLine(t, mirror, "rev-parse", "refs/heads/candidate^{commit}"); observed != eventSHA {
		t.Fatalf("imported event SHA = %q, want %q", observed, eventSHA)
	}
	if observed := productionGitLine(t, mirror, "rev-parse", root.TrustedRef+"^{commit}"); observed != root.BaselineCommit {
		t.Fatalf("imported signed baseline = %q, want %q", observed, root.BaselineCommit)
	}
}

func assertWorkflowTrustedSourceIdentity(t *testing.T, trustedSource, eventSHA, eventTree string) {
	t.Helper()
	if observed := productionGitLine(t, trustedSource, "rev-parse", "HEAD^{commit}"); observed != eventSHA {
		t.Fatalf("trusted source HEAD = %q, want %q", observed, eventSHA)
	}
	if observed := productionGitLine(t, trustedSource, "rev-parse", "HEAD^{tree}"); observed != eventTree {
		t.Fatalf("trusted source tree = %q, want %q", observed, eventTree)
	}
}

func assertWorkflowTrustedSourceFilesystem(t *testing.T, trustedSource string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(trustedSource, "candidate.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("trusted source file mode = %o, want read-only", info.Mode().Perm())
	}
	link, err := os.Lstat(filepath.Join(trustedSource, "candidate-link"))
	if err != nil {
		t.Fatal(err)
	}
	if link.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("trusted source link mode = %v, want symlink", link.Mode())
	}
	target, err := os.Readlink(filepath.Join(trustedSource, "candidate-link"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "candidate.txt" {
		t.Fatalf("trusted source link target = %q", target)
	}
}

func assertWorkflowTrustedSourceCleanup(t *testing.T, trustedSourceRoot string, cleanup func() error) {
	t.Helper()
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(trustedSourceRoot); !os.IsNotExist(err) {
		t.Fatalf("trusted source root remains after cleanup: %v", err)
	}
}

func TestImportWorkflowTrustedMirrorFailsClosedOnEventDrift(t *testing.T) {
	fixture := newProductionTestFixture(t)
	root, err := loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	root.RemoteURL = fixture.sourceRepo
	err = importWorkflowTrustedMirror(
		context.Background(), root, workflowPrivateDirectory(t), root.TrustedRef, strings.Repeat("f", 40), fixture.tree,
	)
	if err == nil || !strings.Contains(err.Error(), "event ref does not match workflow event SHA") {
		t.Fatalf("event SHA drift error = %v", err)
	}
}

func TestValidateWorkflowEventAcceptsOnlyCanonicalBranchOrStrictPullRequestRefs(t *testing.T) {
	fixture := newProductionTestFixture(t)
	root, err := loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflowEvent(workflowHostOptions{
		eventRepository: "attacker/repository", eventRef: root.TrustedRef, eventSHA: fixture.commit,
	}, fixture.config, root); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("repository drift error = %v", err)
	}
	for _, test := range []struct {
		ref   string
		valid bool
	}{
		{ref: "refs/heads/release/v1", valid: true},
		{ref: "refs/pull/1/head", valid: true},
		{ref: "refs/pull/0/head"},
		{ref: "refs/pull/01/head"},
		{ref: "refs/pull/not-a-number/head"},
		{ref: "refs/pull/1/merge"},
		{ref: "refs/heads/release..v1"},
		{ref: "refs/heads/@"},
		{ref: "refs/heads/release/"},
		{ref: "refs/heads/release//v1"},
		{ref: "refs/tags/v1"},
	} {
		err := validateWorkflowEvent(workflowHostOptions{
			eventRepository: fixture.config.RepoID, eventRef: test.ref, eventSHA: fixture.commit,
		}, fixture.config, root)
		if (err == nil) != test.valid {
			t.Fatalf("validate event ref %q error = %v, valid = %v", test.ref, err, test.valid)
		}
	}
}

func TestMaterializeWorkflowAuthorityBundleRejectsStaticMirror(t *testing.T) {
	_, err := materializeWorkflowAuthorityBundle(workflowAuthorityBundleForTest(t, "trusted.git/HEAD"))
	if err == nil || !strings.Contains(err.Error(), "unsafe or unexpected") {
		t.Fatalf("static trusted mirror bundle error = %v", err)
	}
}

func TestWorkflowOIDCAttestationFailsClosedWithoutDeploymentCredentials(t *testing.T) {
	t.Setenv(workflowOIDCAudienceEnv, "")
	t.Setenv(workflowOIDCRequestURLEnv, "")
	t.Setenv(workflowOIDCRequestTokenEnv, "")
	_, err := workflowOIDCAttestationDigest(context.Background(), workflowHostOptions{})
	if err == nil || !strings.Contains(err.Error(), workflowOIDCAudienceEnv) {
		t.Fatalf("missing OIDC deployment configuration error = %v", err)
	}
}

func TestWorkflowOIDCAttestationBindsEventAndGitHubToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("audience") != "super-dolphin-gate-workflow" {
			t.Fatalf("OIDC audience = %q", request.URL.Query().Get("audience"))
		}
		if request.Header.Get("Authorization") != "Bearer request-token" {
			t.Fatalf("OIDC authorization = %q", request.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`{"value":"header.payload.signature"}`))
	}))
	defer server.Close()
	t.Setenv(workflowOIDCAudienceEnv, "super-dolphin-gate-workflow")
	t.Setenv(workflowOIDCRequestURLEnv, server.URL)
	t.Setenv(workflowOIDCRequestTokenEnv, "request-token")
	first, err := workflowOIDCAttestationDigestWithClient(context.Background(), workflowHostOptions{
		eventRepository: "example/repository", eventRef: "refs/heads/candidate", eventSHA: strings.Repeat("a", 40),
	}, server.Client())
	if err != nil || !validWorkflowAttestationDigest(first) {
		t.Fatalf("OIDC attestation=%q error=%v", first, err)
	}
	second, err := workflowOIDCAttestationDigestWithClient(context.Background(), workflowHostOptions{
		eventRepository: "example/repository", eventRef: "refs/heads/candidate", eventSHA: strings.Repeat("b", 40),
	}, server.Client())
	if err != nil || first == second {
		t.Fatalf("OIDC event binding first=%q second=%q error=%v", first, second, err)
	}
}

func workflowAuthorityBundleForTest(t *testing.T, name string) string {
	t.Helper()
	buffer := &bytes.Buffer{}
	gzipWriter := gzip.NewWriter(buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}
