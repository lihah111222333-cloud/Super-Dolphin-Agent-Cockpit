package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestInstallCurrentRemoteShardManifestReplacesAcceptedManifest(t *testing.T) {
	fixture := newRemoteManifestInstallFixture(t)
	chmodCalls, chownCalls, err := installRemoteManifestWithRecordingOwnership(fixture)
	if err != nil {
		t.Fatalf("installCurrentRemoteShardManifestWithOps() error = %v", err)
	}
	assertRemoteManifestInstalled(t, fixture, chmodCalls, chownCalls)
}

func installRemoteManifestWithRecordingOwnership(fixture *remoteManifestInstallFixture) ([]string, []string, error) {
	var chmodCalls []string
	var chownCalls []string
	err := installCurrentRemoteShardManifestWithOps(
		context.Background(), fixture.config, fixture.workRoot, fixture.download,
		func(path string, mode os.FileMode) error {
			chmodCalls = append(chmodCalls, path+":"+mode.String())
			return nil
		},
		func(path string, uid, gid int) error {
			chownCalls = append(chownCalls, path+":"+itoa(uid)+":"+itoa(gid))
			return nil
		},
	)
	return chmodCalls, chownCalls, err
}

func assertRemoteManifestInstalled(t *testing.T, fixture *remoteManifestInstallFixture, chmodCalls, chownCalls []string) {
	t.Helper()
	updated, err := os.ReadFile(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(updated, fixture.oldManifest) {
		t.Fatal("current v2 manifest did not replace accepted v1 manifest")
	}
	assertRemoteManifestPayload(t, updated, fixture.current.ShardExecutionManifestDigest)
	assertRemoteManifestOwnershipCalls(t, chmodCalls, chownCalls)
	assertRemoteManifestMode(t, fixture.manifestPath)
}

func assertRemoteManifestPayload(t *testing.T, data []byte, expectedDigest string) {
	t.Helper()
	var manifest gatecontract.ShardExecutionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode current manifest: %v", err)
	}
	if manifest.ManifestDigest != expectedDigest {
		t.Fatalf("current manifest digest = %q, want %q", manifest.ManifestDigest, expectedDigest)
	}
}

func assertRemoteManifestOwnershipCalls(t *testing.T, chmodCalls, chownCalls []string) {
	t.Helper()
	if len(chmodCalls) != 1 || len(chownCalls) != 2 {
		t.Fatalf("ownership calls chmod=%v chown=%v", chmodCalls, chownCalls)
	}
	if !strings.Contains(chmodCalls[0], ":-rwx------") {
		t.Fatalf("ownership modes = %v", chmodCalls)
	}
	for _, call := range chownCalls {
		if !strings.HasSuffix(call, ":65532:65532") {
			t.Fatalf("ownership identity = %v", chownCalls)
		}
	}
}

func assertRemoteManifestMode(t *testing.T, manifestPath string) {
	t.Helper()
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifestInfo.Mode().Perm() != 0o400 {
		t.Fatalf("current manifest mode = %o, want 0400", manifestInfo.Mode().Perm())
	}
}

func TestInstallCurrentRemoteShardManifestRejectsInvalidInputsAndPreservesOld(t *testing.T) {
	for _, name := range []string{"bootstrap sha mismatch", "unknown current request field", "identity drift", "fixed manifest symlink", "extraneous work root entry", "accepted manifest drift"} {
		t.Run(name, func(t *testing.T) {
			fixture := newRemoteManifestInstallFixture(t)
			runInvalidRemoteManifestInstallCase(t, fixture, name)
		})
	}
}

func runInvalidRemoteManifestInstallCase(t *testing.T, fixture *remoteManifestInstallFixture, name string) {
	t.Helper()
	if err := mutateRemoteManifestInstallFixture(fixture, name); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	err = installCurrentRemoteShardManifestWithOps(
		context.Background(), fixture.config, fixture.workRoot, fixture.download,
		func(string, os.FileMode) error { return nil },
		func(string, int, int) error { return nil },
	)
	if err == nil {
		t.Fatal("invalid candidate manifest input unexpectedly passed")
	}
	after, readErr := os.ReadFile(fixture.manifestPath)
	if readErr == nil && !bytes.Equal(before, after) {
		t.Fatalf("failed install changed old manifest: before=%q after=%q", before, after)
	}
}

func mutateRemoteManifestInstallFixture(fixture *remoteManifestInstallFixture, name string) error {
	switch name {
	case "bootstrap sha mismatch":
		fixture.config.Bootstrap.RequestSHA256 = strings.Repeat("0", 64)
	case "unknown current request field":
		fixture.currentBytes = insertUnknownJSONField(fixture.currentBytes)
		fixture.config.CurrentRequestSHA256 = digestBytes(fixture.currentBytes)
	case "identity drift":
		return driftRemoteManifestInstallIdentity(fixture)
	case "fixed manifest symlink":
		if err := os.Remove(fixture.manifestPath); err != nil {
			return err
		}
		return os.Symlink(fixture.oldManifestPath, fixture.manifestPath)
	case "extraneous work root entry":
		return os.WriteFile(filepath.Join(fixture.workRoot, "unexpected"), []byte("stale"), 0o600)
	case "accepted manifest drift":
		if err := os.Chmod(fixture.manifestPath, 0o600); err != nil {
			return err
		}
		return os.WriteFile(fixture.manifestPath, []byte(`{"schema_version":1}`), 0o400)
	default:
		return fmt.Errorf("unknown manifest install test case %q", name)
	}
	return nil
}

func driftRemoteManifestInstallIdentity(fixture *remoteManifestInstallFixture) error {
	request := fixture.current
	request.PlanDigest = "sha256:" + strings.Repeat("7", 64)
	manifestDigest, err := request.ComputeShardExecutionManifestDigest()
	if err != nil {
		return fmt.Errorf("compute identity drift manifest digest: %w", err)
	}
	request.ShardExecutionManifestDigest = manifestDigest
	data, digest, err := remoteci.EncodeShardRequest(request)
	if err != nil {
		return fmt.Errorf("encode identity drift request: %w", err)
	}
	fixture.currentBytes, fixture.config.CurrentRequestSHA256 = data, digest
	return nil
}

type remoteManifestInstallFixture struct {
	t                   *testing.T
	config              remoteManifestInstallConfig
	workRoot            string
	manifestPath        string
	oldManifestPath     string
	oldManifest         []byte
	current             remoteci.ShardRequest
	currentBytes        []byte
	bootstrapBytes      []byte
	bootstrapRequestKey string
	currentRequestKey   string
}

func newRemoteManifestInstallFixture(t *testing.T) *remoteManifestInstallFixture {
	t.Helper()
	current := validRemoteMaterializeShardRequest(t)
	bootstrapBytes, bootstrapSHA, err := remoteci.EncodeBootstrapShardRequest(current)
	if err != nil {
		t.Fatalf("encode bootstrap request: %v", err)
	}
	currentBytes, currentSHA, err := remoteci.EncodeShardRequest(current)
	if err != nil {
		t.Fatalf("encode current request: %v", err)
	}
	bootstrapRequestKey := "source-bundles/" + current.JobID + "/" + bootstrapSHA + ".bootstrap.request.json"
	currentRequestKey := "source-bundles/" + current.JobID + "/" + currentSHA + ".request.json"
	workRoot := t.TempDir()
	manifestPath := filepath.Join(workRoot, filepath.Base(gatecontract.ExecutorShardExecutionManifestPath))
	for _, directory := range []string{"bin", "go-cache"} {
		if err := os.Mkdir(filepath.Join(workRoot, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bootstrap, err := remoteci.DecodeBootstrapShardRequest(bootstrapBytes)
	if err != nil {
		t.Fatalf("decode bootstrap fixture: %v", err)
	}
	oldManifest, oldDigest, err := remoteci.EncodeAcceptedBootstrapManifestForRequest(bootstrap)
	if err != nil {
		t.Fatalf("encode accepted manifest: %v", err)
	}
	if oldDigest != bootstrap.ShardExecutionManifestDigest {
		t.Fatalf("accepted fixture digest = %q, request = %q", oldDigest, bootstrap.ShardExecutionManifestDigest)
	}
	if err := os.WriteFile(manifestPath, oldManifest, 0o400); err != nil {
		t.Fatal(err)
	}
	oldManifestPath := filepath.Join(t.TempDir(), "old-manifest-copy")
	if err := os.WriteFile(oldManifestPath, oldManifest, 0o400); err != nil {
		t.Fatal(err)
	}
	config := remoteManifestInstallConfig{
		Bootstrap: remoteMaterializeConfig{
			RequestKey:       bootstrapRequestKey,
			RequestSHA256:    bootstrapSHA,
			AgentTokenDigest: current.AgentTokenDigest,
		},
		CurrentRequestKey:     currentRequestKey,
		CurrentRequestSHA256:  currentSHA,
		CurrentManifestDigest: current.ShardExecutionManifestDigest,
	}
	return &remoteManifestInstallFixture{
		t: t, config: config, workRoot: workRoot, manifestPath: manifestPath,
		oldManifestPath: oldManifestPath, oldManifest: oldManifest,
		current: current, currentBytes: currentBytes, bootstrapBytes: bootstrapBytes,
		bootstrapRequestKey: bootstrapRequestKey, currentRequestKey: currentRequestKey,
	}
}

func (fixture *remoteManifestInstallFixture) download(_ context.Context, key string, _ int64, destination io.Writer) (int64, error) {
	var data []byte
	switch key {
	case fixture.bootstrapRequestKey:
		data = fixture.bootstrapBytes
	case fixture.currentRequestKey:
		data = fixture.currentBytes
	default:
		return 0, errors.New("unexpected object key")
	}
	count, err := destination.Write(data)
	return int64(count), err
}

func insertUnknownJSONField(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
		return append([]byte(nil), data...)
	}
	return append(append(append([]byte(nil), trimmed[:len(trimmed)-1]...), []byte(`,"unknown":true`)...), '}')
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}
