//go:build darwin

package pidregistry

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

type cooperativeServerStartResult struct {
	server *CooperativeTerminationServer
	err    error
}

func awaitCooperativeTestValue[T any](t *testing.T, ctx context.Context, values <-chan T, timeoutMessage string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-ctx.Done():
		t.Fatal(timeoutMessage)
		var zero T
		return zero
	}
}

func TestCooperativeTerminationAuthenticatesBeforeACK(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	terminated := make(chan struct{}, 1)
	server, err := StartCooperativeTerminationServer(endpoint, "exact-secret", func() { terminated <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	info, err := os.Stat(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("termination endpoint mode = %o, want 600", info.Mode().Perm())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := requestCooperativeTermination(ctx, endpoint, "wrong-secret", os.Getpid(), CooperativeEndpointIdentity{}); err == nil {
		t.Fatal("wrong termination token received ACK")
	}
	select {
	case <-terminated:
		t.Fatal("wrong termination token invoked callback")
	default:
	}
	if err := requestCooperativeTermination(ctx, endpoint, "exact-secret", os.Getpid(), CooperativeEndpointIdentity{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-terminated:
	case <-ctx.Done():
		t.Fatal("authenticated termination ACK did not invoke callback")
	}
}

func TestCooperativeTerminationEndpointPublishesOnlyAfterOwnerMode(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	identity, err := CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	identity.TerminationEndpoint = endpoint
	identity.TerminationToken = "publish-secret"

	staged := make(chan string, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	result := make(chan cooperativeServerStartResult, 1)
	safego.Go(context.Background(), nil, "pidregistry.test-cooperative-endpoint-publication", func(context.Context) {
		server, startErr := startCooperativeTerminationServerWithPublishHook(
			endpoint,
			identity.TerminationToken,
			func() {},
			func(staging string) error {
				if chmodErr := os.Chmod(staging, 0o777); chmodErr != nil {
					return chmodErr
				}
				staged <- staging
				<-release
				return nil
			},
		)
		result <- cooperativeServerStartResult{server: server, err: startErr}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	awaitCooperativeTestValue(t, ctx, staged, "cooperative endpoint did not reach pre-publication barrier")
	if err := ProbeExactProcessEndpoint(ctx, identity); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-publication probe error = %v, want os.ErrNotExist", err)
	}
	releaseOnce.Do(func() { close(release) })
	started := awaitCooperativeTestValue(t, ctx, result, "cooperative endpoint publication did not complete")
	if started.err != nil {
		t.Fatal(started.err)
	}
	defer started.server.Close()
	if err := ProbeExactProcessEndpoint(ctx, identity); err != nil {
		t.Fatalf("published endpoint READY: %v", err)
	}
}

func TestCooperativeTerminationEndpointPublicationRejectsReplacement(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	t.Cleanup(func() { _ = os.Remove(endpoint) })
	staged := make(chan string, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	result := make(chan cooperativeServerStartResult, 1)
	safego.Go(context.Background(), nil, "pidregistry.test-cooperative-endpoint-replacement", func(context.Context) {
		server, startErr := startCooperativeTerminationServerWithPublishHook(
			endpoint,
			"replacement-secret",
			func() {},
			func(staging string) error {
				staged <- staging
				<-release
				return nil
			},
		)
		result <- cooperativeServerStartResult{server: server, err: startErr}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stagingEndpoint := awaitCooperativeTestValue(t, ctx, staged, "cooperative endpoint did not reach replacement barrier")
	const replacement = "occupied"
	if err := os.WriteFile(endpoint, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	started := awaitCooperativeTestValue(t, ctx, result, "cooperative endpoint replacement check did not complete")
	if started.server != nil {
		_ = started.server.Close()
		t.Fatalf("replacement publication returned server %v", started.server)
	}
	if started.err == nil {
		t.Fatal("replacement publication returned nil error")
	}
	contents, err := os.ReadFile(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != replacement {
		t.Fatalf("replacement contents = %q, want %q", contents, replacement)
	}
	if _, err := os.Lstat(stagingEndpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging endpoint cleanup error = %v, want os.ErrNotExist", err)
	}
}

func TestTerminateExactProcessUsesAuthenticatedCooperativeExit(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	ready := endpoint + ".ready"
	cmd := exec.Command(os.Args[0], "-test.run=TestTerminateExactProcessCooperativeChild")
	cmd.Env = append(os.Environ(),
		"SUPER_DOLPHIN_TEST_TERMINATION_CHILD=1",
		"SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT="+endpoint,
		"SUPER_DOLPHIN_TEST_TERMINATION_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("cooperative termination child did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	identity, err := CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	identity.TerminationEndpoint = endpoint
	identity.TerminationToken = "child-exact-secret"
	waited := make(chan error, 1)
	var waiter sync.WaitGroup
	waiter.Go(func() { waited <- cmd.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := TerminateExactProcess(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := <-waited; err != nil {
		t.Fatalf("cooperative child exit: %v", err)
	}
}

func TestRequestExactProcessTerminationRejectsPIDReuseSentinel(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	terminated := make(chan struct{}, 1)
	server, err := StartCooperativeTerminationServer(endpoint, "reuse-secret", func() {
		terminated <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	identity, err := CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	identity.ProcessStartToken += "-reused"
	identity.TerminationEndpoint = endpoint
	identity.TerminationToken = "reuse-secret"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := RequestExactProcessTermination(ctx, identity); !errors.Is(err, ErrStableProcessIdentityMismatch) {
		t.Fatalf("RequestExactProcessTermination() error = %v, want identity mismatch", err)
	}
	select {
	case <-terminated:
		t.Fatal("PID reuse sentinel received a termination request")
	default:
	}
}

func TestCooperativeTerminationNoResponseHonorsDeadline(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(endpoint, 0o600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	serverDone := make(chan error, 1)
	var server sync.WaitGroup
	server.Go(func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()
		<-release
		serverDone <- nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	if err := requestCooperativeTermination(ctx, endpoint, "no-response-token", os.Getpid(), CooperativeEndpointIdentity{}); err == nil {
		t.Fatal("termination request without ACK returned success")
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("termination no-response elapsed %s", elapsed)
	}
	close(release)
	server.Wait()
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestParkedCooperativeServerPreparesAndActivatesIdempotently(t *testing.T) {
	server, identity, endpointIdentity := newParkedProtocolTestServer(t, "parked-secret")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wrong := identity
	wrong.TerminationToken = "wrong-secret"
	if err := ProbeExactProcessEndpoint(ctx, wrong); err == nil {
		t.Fatal("unauthenticated READY succeeded")
	}
	if err := ProbeExactProcessEndpoint(ctx, identity); err != nil {
		t.Fatalf("authenticated READY: %v", err)
	}
	if err := PrepareExactProcessStartup(ctx, identity, endpointIdentity); err != nil {
		t.Fatalf("PREPARE: %v", err)
	}
	select {
	case <-server.activation:
		t.Fatal("PREPARE released parked helper before durable ACK")
	default:
	}
	if err := ActivateExactProcessStartup(ctx, identity, endpointIdentity); err != nil {
		t.Fatalf("first ACTIVATE: %v", err)
	}
	if err := server.WaitForActivation(ctx); err != nil {
		t.Fatalf("WaitForActivation(): %v", err)
	}
	if err := ActivateExactProcessStartup(ctx, identity, endpointIdentity); err != nil {
		t.Fatalf("replayed ACTIVATE: %v", err)
	}
}

func TestParkedServerReplaysActivationAfterResponseLoss(t *testing.T) {
	server, identity, endpointIdentity := newParkedProtocolTestServer(t, "lost-response-secret")
	defer server.Close()
	if err := ProbeExactProcessEndpointInstance(t.Context(), identity, endpointIdentity); err != nil {
		t.Fatal(err)
	}
	if err := PrepareExactProcessStartup(t.Context(), identity, endpointIdentity); err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: identity.TerminationEndpoint, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("COMMIT lost-response-secret\n")); err != nil {
		t.Fatal(err)
	}
	discarded := make([]byte, len("COMMITTED\n"))
	if _, err := connection.Read(discarded); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := server.WaitForActivation(ctx); err != nil {
		t.Fatalf("activation after lost response: %v", err)
	}
	if err := ActivateExactProcessStartup(ctx, identity, endpointIdentity); err != nil {
		t.Fatalf("replayed ACTIVATE: %v", err)
	}
}

func newParkedProtocolTestServer(
	t *testing.T,
	token string,
) (*CooperativeTerminationServer, StableProcessIdentity, CooperativeEndpointIdentity) {
	t.Helper()
	endpoint := testTerminationSocketPath(t)
	server, err := StartParkedCooperativeTerminationServer(endpoint, token, func() {})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	identity.TerminationEndpoint = endpoint
	identity.TerminationToken = token
	endpointIdentity, err := CaptureCooperativeEndpointIdentity(endpoint)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return server, identity, endpointIdentity
}

func TestParkedHelperExitAfterReadyCannotCommit(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_TEST_PARKED_EXIT_CHILD") == "1" {
		runParkedExitChild()
		return
	}
	endpoint := testTerminationSocketPath(t)
	ready := endpoint + ".ready"
	exit := endpoint + ".exit"
	cmd := exec.Command(os.Args[0], "-test.run=^TestParkedHelperExitAfterReadyCannotCommit$")
	cmd.Env = append(os.Environ(),
		"SUPER_DOLPHIN_TEST_PARKED_EXIT_CHILD=1",
		"SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT="+endpoint,
		"SUPER_DOLPHIN_TEST_TERMINATION_READY="+ready,
		"SUPER_DOLPHIN_TEST_TERMINATION_EXIT="+exit,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(exit)
	})
	waitForTerminationTestPath(t, ready)
	identity, err := CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	identity.TerminationEndpoint = endpoint
	identity.TerminationToken = "parked-exit-secret"
	if err := ProbeExactProcessEndpoint(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exit, []byte("exit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	endpointIdentity, err := CaptureCooperativeEndpointIdentity(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareExactProcessStartup(t.Context(), identity, endpointIdentity); err == nil {
		t.Fatal("PREPARE succeeded after parked helper exited")
	}
	if err := CleanupCooperativeTerminationEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
}

func TestActivateRejectsSocketReplacedByOrdinaryFile(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	server, err := StartParkedCooperativeTerminationServer(endpoint, "replace-secret", func() {})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	identity.TerminationEndpoint = endpoint
	identity.TerminationToken = "replace-secret"
	if err := ProbeExactProcessEndpoint(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	endpointIdentity, err := CaptureCooperativeEndpointIdentity(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareExactProcessStartup(t.Context(), identity, endpointIdentity); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(endpoint, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ActivateExactProcessStartup(t.Context(), identity, endpointIdentity); err == nil {
		t.Fatal("ACTIVATE accepted an ordinary-file endpoint replacement")
	}
	if _, err := os.Stat(endpoint); err != nil {
		t.Fatalf("ordinary-file replacement was removed: %v", err)
	}
}

func runParkedExitChild() {
	server, err := StartParkedCooperativeTerminationServer(
		os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT"), "parked-exit-secret", func() {},
	)
	if err != nil {
		os.Exit(2)
	}
	defer server.Close()
	if err := os.WriteFile(os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(3)
	}
	for {
		if _, err := os.Stat(os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_EXIT")); err == nil {
			os.Exit(0)
		} else if !errors.Is(err, os.ErrNotExist) {
			os.Exit(4)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForTerminationTestPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestCleanupCooperativeTerminationEndpointRejectsOrdinaryFile(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	if err := os.WriteFile(endpoint, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupCooperativeTerminationEndpoint(endpoint); err == nil {
		t.Fatal("ordinary file was accepted as a cooperative endpoint")
	}
	if _, err := os.Stat(endpoint); err != nil {
		t.Fatalf("ordinary file was removed: %v", err)
	}
}

func TestCaptureCooperativeEndpointIdentityReportsOwnedSocketNotReady(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(endpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureCooperativeEndpointIdentity(endpoint); !errors.Is(err, ErrCooperativeEndpointNotReady) {
		t.Fatalf("CaptureCooperativeEndpointIdentity() error = %v, want not ready", err)
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureCooperativeEndpointIdentity(endpoint); err != nil {
		t.Fatalf("CaptureCooperativeEndpointIdentity() after chmod: %v", err)
	}
}

func TestServerClosePreservesSocketReplacement(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	original, err := StartParkedCooperativeTerminationServer(endpoint, "same-token", func() {})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(endpoint); err != nil {
		t.Fatal(err)
	}
	replacement, err := StartParkedCooperativeTerminationServer(endpoint, "same-token", func() {})
	if err != nil {
		t.Fatal(err)
	}
	replacementIdentity := replacement.endpointIdentity
	if err := original.Close(); !errors.Is(err, ErrCooperativeEndpointIdentityMismatch) {
		t.Fatalf("original Close error = %v, want endpoint identity mismatch", err)
	}
	got, err := CaptureCooperativeEndpointIdentity(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if got != replacementIdentity {
		t.Fatalf("replacement identity = %+v, want %+v", got, replacementIdentity)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStaleCleanupPreservesLiveSameTokenEndpoint(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	server, err := StartParkedCooperativeTerminationServer(endpoint, "same-token", func() {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := CleanupStaleCooperativeTerminationEndpoint(ctx, endpoint); err == nil {
		t.Fatal("stale cleanup removed a live same-token endpoint")
	}
	if got, err := CaptureCooperativeEndpointIdentity(endpoint); err != nil || got != server.endpointIdentity {
		t.Fatalf("live endpoint after stale cleanup = %+v error=%v", got, err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplacementPeerPIDFailsClosedForAllControls(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_TEST_REPLACEMENT_PEER") == "1" {
		runReplacementPeerChild()
		return
	}
	endpoint := testTerminationSocketPath(t)
	original, err := StartParkedCooperativeTerminationServer(endpoint, "replacement-token", func() {})
	if err != nil {
		t.Fatal(err)
	}
	originalEndpoint := original.endpointIdentity
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	ready := endpoint + ".replacement-ready"
	cmd := exec.Command(os.Args[0], "-test.run=^TestReplacementPeerPIDFailsClosedForAllControls$")
	cmd.Env = append(os.Environ(),
		"SUPER_DOLPHIN_TEST_REPLACEMENT_PEER=1",
		"SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT="+endpoint,
		"SUPER_DOLPHIN_TEST_TERMINATION_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(ready)
		_ = os.Remove(endpoint)
	})
	waitForTerminationTestPath(t, ready)
	identity, err := CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	identity.TerminationEndpoint = endpoint
	identity.TerminationToken = "replacement-token"
	if err := ProbeExactProcessEndpoint(t.Context(), identity); !errors.Is(err, ErrStableProcessIdentityMismatch) {
		t.Fatalf("replacement READY error = %v, want peer PID mismatch", err)
	}
	if err := ActivateExactProcessStartup(t.Context(), identity, originalEndpoint); !errors.Is(err, ErrCooperativeEndpointIdentityMismatch) {
		t.Fatalf("replacement ACTIVATE error = %v, want endpoint mismatch", err)
	}
	if err := RequestExactProcessTermination(t.Context(), identity); !errors.Is(err, ErrStableProcessIdentityMismatch) {
		t.Fatalf("replacement TERMINATE error = %v, want peer PID mismatch", err)
	}
}

func runReplacementPeerChild() {
	server, err := StartParkedCooperativeTerminationServer(
		os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT"), "replacement-token", func() {},
	)
	if err != nil {
		os.Exit(2)
	}
	defer server.Close()
	if err := os.WriteFile(os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(3)
	}
	select {}
}

func TestCooperativeEndpointIdentityFieldGuard(t *testing.T) {
	identityType := reflect.TypeFor[CooperativeEndpointIdentity]()
	want := map[string]reflect.Kind{
		"Device": reflect.Uint64,
		"Inode":  reflect.Uint64,
		"UID":    reflect.Uint32,
		"Mode":   reflect.Uint32,
	}
	if identityType.NumField() != len(want) {
		t.Fatalf("endpoint identity field count = %d, want %d", identityType.NumField(), len(want))
	}
	for index := range identityType.NumField() {
		field := identityType.Field(index)
		kind, ok := want[field.Name]
		if !ok || field.Type.Kind() != kind {
			t.Fatalf("unregistered endpoint identity field %s %s", field.Name, field.Type)
		}
		delete(want, field.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing endpoint identity field coverage: %v", want)
	}
}

func TestTerminateExactProcessCooperativeChild(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_CHILD") != "1" {
		return
	}
	terminated := make(chan struct{})
	server, err := StartCooperativeTerminationServer(
		os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT"),
		"child-exact-secret",
		func() { close(terminated) },
	)
	if err != nil {
		os.Exit(2)
	}
	defer server.Close()
	if err := os.WriteFile(os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(3)
	}
	<-terminated
}

func testTerminationSocketPath(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp("/tmp", "sd-term-test-")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name() + ".sock"
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file.Name()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(path + ".ready")
	})
	return path
}
