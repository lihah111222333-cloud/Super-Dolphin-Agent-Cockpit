package appupdate

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

type updateOperationCounts struct {
	manifest atomic.Int32
	artifact atomic.Int32
	verify   atomic.Int32
	launch   atomic.Int32
	quit     atomic.Int32
}

type updateOperationFixture struct {
	svc             *service
	counts          *updateOperationCounts
	verifierEntered chan struct{}
	verifierRelease chan struct{}
	quitCalled      chan struct{}
}

func TestUpdateOperationsLeaseAndInstallStartedLatch(t *testing.T) {
	fixture := newUpdateOperationFixture(t)
	var group errgroup.Group
	group.Go(func() error {
		_, err := fixture.svc.InstallLatest(context.Background())
		return err
	})
	waitForSignal(t, fixture.verifierEntered, "first install verifier")
	assertUpdateOperationsRejected(t, fixture.svc, "concurrent")
	close(fixture.verifierRelease)
	if err := group.Wait(); err != nil {
		t.Fatalf("primary InstallLatest() error = %v", err)
	}
	assertUpdateOperationsRejected(t, fixture.svc, "after helper start")
	waitForSignal(t, fixture.quitCalled, "single RequestQuit")
	assertNoSecondUpdateQuit(t, fixture.quitCalled)
	assertUpdateOperationCounts(t, fixture.counts)
}

func TestUpdateOperationReleasesLeaseWhenHelperDoesNotStart(t *testing.T) {
	fixture := newUpdateOperationFixture(t)
	close(fixture.verifierRelease)
	fixture.svc.launchCommand = func(*exec.Cmd) (bool, error) {
		if fixture.counts.launch.Add(1) == 1 {
			return false, errors.New("injected start failure")
		}
		return true, nil
	}
	if _, err := fixture.svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "injected start failure") {
		t.Fatalf("first Install() error = %v, want injected start failure", err)
	}
	if _, err := fixture.svc.Install(context.Background()); err != nil {
		t.Fatalf("second Install() error = %v, want released lease", err)
	}
	assertUpdateOperationsRejected(t, fixture.svc, "after retry helper start")
	waitForSignal(t, fixture.quitCalled, "RequestQuit after retry")
	if got := fixture.counts.launch.Load(); got != 2 {
		t.Fatalf("helper launch attempts = %d, want 2", got)
	}
}

func TestUpdateOperationRetainsLatchWhenStartedHelperReleaseFails(t *testing.T) {
	fixture := newUpdateOperationFixture(t)
	close(fixture.verifierRelease)
	fixture.svc.launchCommand = func(*exec.Cmd) (bool, error) {
		fixture.counts.launch.Add(1)
		return true, errors.New("injected release failure")
	}
	if _, err := fixture.svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "injected release failure") {
		t.Fatalf("first Install() error = %v, want injected release failure", err)
	}
	assertUpdateOperationsRejected(t, fixture.svc, "after helper release failure")
	if got := fixture.counts.launch.Load(); got != 1 {
		t.Fatalf("helper launch attempts = %d, want 1", got)
	}
	assertNoUpdateQuit(t, fixture.quitCalled, "helper release failure")
}

type updateOperationCall struct {
	name string
	run  func() error
}

func updateOperationCalls(svc *service) []updateOperationCall {
	return []updateOperationCall{
		{name: "Download", run: func() error {
			_, err := svc.Download(context.Background())
			return err
		}},
		{name: "Install", run: func() error {
			_, err := svc.Install(context.Background())
			return err
		}},
		{name: "InstallLatest", run: func() error {
			_, err := svc.InstallLatest(context.Background())
			return err
		}},
	}
}

func assertUpdateOperationsRejected(t *testing.T, svc *service, phase string) {
	t.Helper()
	for _, call := range updateOperationCalls(svc) {
		if err := call.run(); err == nil || !strings.Contains(err.Error(), "already") {
			t.Errorf("%s %s error = %v, want fail-fast operation rejection", call.name, phase, err)
		}
	}
}

func assertUpdateOperationCounts(t *testing.T, counts *updateOperationCounts) {
	t.Helper()
	for name, got := range map[string]int32{
		"manifest requests":     counts.manifest.Load(),
		"artifact downloads":    counts.artifact.Load(),
		"install verifications": counts.verify.Load(),
		"helper launches":       counts.launch.Load(),
		"RequestQuit callbacks": counts.quit.Load(),
	} {
		if got != 1 {
			t.Errorf("%s = %d, want 1", name, got)
		}
	}
}

func assertNoSecondUpdateQuit(t *testing.T, quitCalled <-chan struct{}) {
	t.Helper()
	select {
	case <-quitCalled:
		t.Fatal("RequestQuit called more than once")
	case <-time.After(2 * installQuitDelay):
	}
}

func assertNoUpdateQuit(t *testing.T, quitCalled <-chan struct{}, phase string) {
	t.Helper()
	select {
	case <-quitCalled:
		t.Fatalf("RequestQuit called after %s", phase)
	case <-time.After(2 * installQuitDelay):
	}
}

func newUpdateOperationFixture(t *testing.T) updateOperationFixture {
	t.Helper()
	publicKey, privateKey := testManifestKeypair(t)
	artifactBody := []byte("windows installer")
	payload := testManifestPayload()
	payload.Artifacts[0] = UpdateArtifact{
		Platform: "windows-amd64",
		URL:      "https://updates.example.com/Super-Dolphin-windows-amd64.exe",
		SHA256:   sha256Hex(artifactBody),
		Size:     int64(len(artifactBody)),
	}
	rawManifest := signTestManifest(t, privateKey, payload)
	counts := &updateOperationCounts{}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://updates.example.test/manifest.json":
			counts.manifest.Add(1)
			return okResponse(req, bytes.NewReader(rawManifest)), nil
		case payload.Artifacts[0].URL:
			counts.artifact.Add(1)
			return okResponse(req, bytes.NewReader(artifactBody)), nil
		default:
			return notFoundResponse(req), nil
		}
	})}
	stageDir := appUpdateRealTempDir(t)
	cfg := testServiceConfig(publicKey, stageDir, "1.2.2")
	cfg.Platform = "windows-amd64"
	cfg.WindowsPublisher = "Super Dolphin Test Publisher"
	cfg.WindowsThumbprint = strings.Repeat("a", 40)
	quitCalled := make(chan struct{}, 8)
	svc := newService(cfg, client, func() {
		counts.quit.Add(1)
		quitCalled <- struct{}{}
	})
	verifierEntered := make(chan struct{})
	verifierRelease := make(chan struct{})
	svc.windowsSignatureVerifier = func(_, _, _ string) error {
		if counts.verify.Add(1) == 1 {
			close(verifierEntered)
			<-verifierRelease
		}
		return nil
	}
	svc.launchCommand = func(*exec.Cmd) (bool, error) {
		counts.launch.Add(1)
		return true, nil
	}
	writeSelectedInstallFixtureForPlatform(t, svc, "windows-amd64", filepath.Join(stageDir, exeFilename))
	return updateOperationFixture{
		svc:             svc,
		counts:          counts,
		verifierEntered: verifierEntered,
		verifierRelease: verifierRelease,
		quitCalled:      quitCalled,
	}
}
