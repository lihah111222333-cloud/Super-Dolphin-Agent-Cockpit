package heterocitransport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestSSHRequesterAcquiresLiveSnapshotThroughStrictTransport(t *testing.T) {
	fixture := newSSHRequesterFixture(t, 0)
	requester, err := NewSSHRequester(fixture.config)
	if err != nil {
		t.Fatalf("NewSSHRequester() error = %v", err)
	}
	transport, err := New(requester)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, err := heteroci.AcquireSnapshot(context.Background(), fixture.manifest, 1, transport, fixture.now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireSnapshot() error = %v", err)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Status != heteroci.NodeStatusLive || snapshot.Nodes[0].Available.CPUMilli != 4_000 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	argv, err := os.ReadFile(fixture.argvLog)
	if err != nil {
		t.Fatalf("read fake SSH argv: %v", err)
	}
	if !strings.Contains(string(argv), "linux-a") || !strings.Contains(string(argv), "--probe-request-base64") {
		t.Fatalf("fake SSH argv = %q", argv)
	}
}

func TestSSHRequesterProjectsConnectionUnavailableAsOffline(t *testing.T) {
	fixture := newSSHRequesterFixture(t, 255)
	requester, err := NewSSHRequester(fixture.config)
	if err != nil {
		t.Fatalf("NewSSHRequester() error = %v", err)
	}
	transport, err := New(requester)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, err := heteroci.AcquireSnapshot(context.Background(), fixture.manifest, 1, transport, fixture.now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireSnapshot() error = %v", err)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Status != heteroci.NodeStatusOffline {
		t.Fatalf("offline snapshot = %#v", snapshot)
	}
}

func TestSSHRequesterRejectsFatalCommandAndDependencyDrift(t *testing.T) {
	fixture := newSSHRequesterFixture(t, 42)
	requester, err := NewSSHRequester(fixture.config)
	if err != nil {
		t.Fatalf("NewSSHRequester() error = %v", err)
	}
	transport, err := New(requester)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := heteroci.AcquireSnapshot(context.Background(), fixture.manifest, 1, transport, fixture.now, time.Minute); err == nil {
		t.Fatal("AcquireSnapshot() accepted fatal SSH probe failure")
	}
	bad := fixture.config
	bad.NodeCommands = map[string][]string{"stale": {"/opt/super-dolphin/node-probe"}}
	if _, err := NewSSHRequester(bad); err == nil {
		t.Fatal("NewSSHRequester() accepted stale node command coverage")
	}
	bad = fixture.config
	bad.Manifest.Nodes[0].Endpoint = "https://linux-a"
	if _, err := NewSSHRequester(bad); err == nil {
		t.Fatal("NewSSHRequester() accepted a non-SSH endpoint")
	}
}

func TestSSHRequesterRejectsNonCanonicalOrMismatchedProbeRequest(t *testing.T) {
	fixture := newSSHRequesterFixture(t, 0)
	requester, err := NewSSHRequester(fixture.config)
	if err != nil {
		t.Fatalf("NewSSHRequester() error = %v", err)
	}
	for name, request := range map[string][]byte{
		"unknown":  []byte(`{"schema_version":"heteroci-probe-request/v1","node_id":"linux-a","probe_identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","owner_epoch":1,"extra":true}`),
		"mismatch": []byte(`{"schema_version":"heteroci-probe-request/v1","node_id":"other","probe_identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","owner_epoch":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := requester.Do(context.Background(), fixture.manifest.Nodes[0].Endpoint, request); err == nil {
				t.Fatal("Do() accepted invalid request")
			}
		})
	}
}

type sshRequesterFixture struct {
	manifest heteroci.Manifest
	config   SSHRequesterConfig
	now      time.Time
	argvLog  string
}

func newSSHRequesterFixture(t *testing.T, exitCode int) sshRequesterFixture {
	t.Helper()
	now := time.UnixMilli(1_780_000_000_000)
	manifest := sshRequesterManifest()
	response := sshRequesterResponse(manifest, now)
	root := t.TempDir()
	responsePath := filepath.Join(root, "response.json")
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if err := os.WriteFile(responsePath, encoded, 0o600); err != nil {
		t.Fatalf("write response: %v", err)
	}
	argvLog := filepath.Join(root, "argv.log")
	executable := filepath.Join(root, "ssh")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" > %s\n", shellQuoteSSHArgument(argvLog))
	if exitCode == 0 {
		script += fmt.Sprintf("cat %s\n", shellQuoteSSHArgument(responsePath))
	} else {
		script += fmt.Sprintf("exit %d\n", exitCode)
	}
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake SSH: %v", err)
	}
	return sshRequesterFixture{
		manifest: manifest, now: now, argvLog: argvLog,
		config: SSHRequesterConfig{
			SSHExecutable: executable, Manifest: manifest,
			NodeCommands: map[string][]string{"linux-a": {"/opt/super-dolphin/node-probe"}},
			Timeout:      5 * time.Second, WaitDelay: time.Second,
		},
	}
}

func sshRequesterManifest() heteroci.Manifest {
	return heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative,
		CloudOverflow: heteroci.CloudOverflowConfig{},
		Nodes: []heteroci.NodeConfig{{
			NodeID: "linux-a", Endpoint: "ssh://linux-a",
			ProbeIdentity: "sha256:" + strings.Repeat("a", 64), Platform: heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{{
				Runtime: heteroci.RuntimeUbuntuContainer, Image: "example.invalid/ubuntu",
				ImageDigest: "sha256:" + strings.Repeat("b", 64),
			}},
		}},
	}
}

func sshRequesterResponse(manifest heteroci.Manifest, now time.Time) heteroci.ProbeResponse {
	node := manifest.Nodes[0]
	capacity := heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 * 1024 * 1024 * 1024, MaxConcurrency: 2}
	return heteroci.ProbeResponse{
		SchemaVersion: heteroci.ProbeResponseSchemaVersion, OwnerEpoch: 1,
		CapacityBasis: heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		NodeID:        node.NodeID, Endpoint: node.Endpoint, Platform: node.Platform,
		ProbeIdentity: node.ProbeIdentity, ProbeBootID: "boot-linux-a",
		ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: node.Capabilities[0].ImageDigest}},
		Status:             heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity,
	}
}
