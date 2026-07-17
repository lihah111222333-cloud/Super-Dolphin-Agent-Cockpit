//go:build darwin

package pidregistry

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

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
	if err := requestCooperativeTermination(ctx, endpoint, "wrong-secret"); err == nil {
		t.Fatal("wrong termination token received ACK")
	}
	select {
	case <-terminated:
		t.Fatal("wrong termination token invoked callback")
	default:
	}
	if err := requestCooperativeTermination(ctx, endpoint, "exact-secret"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-terminated:
	case <-ctx.Done():
		t.Fatal("authenticated termination ACK did not invoke callback")
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
	if err := requestCooperativeTermination(ctx, endpoint, "no-response-token"); err == nil {
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

func TestParkedCooperativeServerAuthenticatesReadyAndCommitReplay(t *testing.T) {
	endpoint := testTerminationSocketPath(t)
	server, err := StartParkedCooperativeTerminationServer(endpoint, "parked-secret", func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	identity, err := CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	identity.TerminationEndpoint = endpoint
	identity.TerminationToken = "parked-secret"

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
	if err := CommitExactProcessStartup(ctx, identity); err != nil {
		t.Fatalf("first COMMIT: %v", err)
	}
	if err := server.WaitForCommit(ctx); err != nil {
		t.Fatalf("WaitForCommit(): %v", err)
	}
	if err := CommitExactProcessStartup(ctx, identity); err != nil {
		t.Fatalf("replayed COMMIT: %v", err)
	}
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
	if err := CommitExactProcessStartup(t.Context(), identity); err == nil {
		t.Fatal("COMMIT succeeded after parked helper exited")
	}
	if err := CleanupCooperativeTerminationEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
}

func TestCommitRejectsSocketReplacedByOrdinaryFile(t *testing.T) {
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
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(endpoint, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CommitExactProcessStartup(t.Context(), identity); err == nil {
		t.Fatal("COMMIT accepted an ordinary-file endpoint replacement")
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
