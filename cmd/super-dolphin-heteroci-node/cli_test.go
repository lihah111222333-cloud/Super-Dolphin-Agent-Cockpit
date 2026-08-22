package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
)

func TestRunNodeCLIProbeEmitsCanonicalResponse(t *testing.T) {
	config, request := nodeCLIFixture(t)
	factory := func(config nodeRuntimeConfig) (heterocinode.FactsProvider, error) {
		return nodeFactsProvider(config), nil
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runNodeCLI([]string{"probe", "--config", config, "--probe-request-base64", request}, stdout, stderr, factory)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("runNodeCLI() code=%d stderr=%q", code, stderr.String())
	}
	response, err := heteroci.DecodeProbeResponse(bytes.NewReader(stdout.Bytes()))
	if err != nil || response.NodeID != "node-a" || response.OwnerEpoch != 7 {
		t.Fatalf("response=%#v error=%v", response, err)
	}
}

func TestRunNodeCLIRejectsConfigRequestAndUnavailableFacts(t *testing.T) {
	config, request := nodeCLIFixture(t)
	tests := []struct {
		name    string
		args    []string
		factory nodeProviderFactory
		code    int
	}{
		{name: "missing", args: nil, factory: defaultNodeProviderFactory, code: 2},
		{name: "relative config", args: []string{"probe", "--config", "node.json", "--probe-request-base64", request}, factory: defaultNodeProviderFactory, code: 2},
		{name: "bad request", args: []string{"probe", "--config", config, "--probe-request-base64", "bad"}, factory: defaultNodeProviderFactory, code: 2},
		{name: "unavailable", args: []string{"probe", "--config", config, "--probe-request-base64", request}, factory: func(nodeRuntimeConfig) (heterocinode.FactsProvider, error) {
			return nil, errors.New("unavailable")
		}, code: 255},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if got := runNodeCLI(test.args, stdout, stderr, test.factory); got != test.code || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("result code=%d stdout=%q stderr=%q", got, stdout.String(), stderr.String())
			}
		})
	}
}

func nodeCLIFixture(t *testing.T) (string, string) {
	t.Helper()
	node := heteroci.NodeConfig{
		NodeID: "node-a", Endpoint: "ssh://node-a", ProbeIdentity: "sha256:" + strings.Repeat("a", 64),
		Platform: heteroci.PlatformLinuxAMD64,
		Capabilities: []heteroci.CapabilityConfig{{
			Runtime: heteroci.RuntimeUbuntuContainer, Image: "example.invalid/ubuntu",
			ImageDigest: "sha256:" + strings.Repeat("b", 64),
		}},
	}
	config := nodeRuntimeConfig{
		SchemaVersion: heterocinode.ConfigSchemaVersion, Node: node,
		Linux: &nodeLinuxRuntimeConfig{DockerExecutable: "/usr/bin/docker", CgroupRoot: "/sys/fs/cgroup/test", BootIDPath: "/proc/boot-id", TimeoutMS: 5_000, WaitDelayMS: 1_000},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "node.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	requestBytes, err := json.Marshal(heteroci.ProbeRequest{
		SchemaVersion: heteroci.ProbeRequestSchemaVersion, NodeID: node.NodeID,
		ProbeIdentity: node.ProbeIdentity, OwnerEpoch: 7, CapturedAtUnixMilli: 1_780_000_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, base64.StdEncoding.EncodeToString(requestBytes)
}

func nodeFactsProvider(config nodeRuntimeConfig) heterocinode.FactsProvider {
	return factsProviderFunc(func(context.Context, heterocinode.Config, heteroci.ProbeRequest) (heterocinode.Facts, error) {
		capacity := heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 << 30, MaxConcurrency: 4}
		return heterocinode.Facts{
			BootID: "boot-a", Total: capacity, Available: capacity,
			Capabilities: []heteroci.ActiveCapabilitySnapshot{{
				Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: config.Node.Capabilities[0].ImageDigest,
			}},
		}, nil
	})
}

type factsProviderFunc func(context.Context, heterocinode.Config, heteroci.ProbeRequest) (heterocinode.Facts, error)

func (function factsProviderFunc) Collect(ctx context.Context, config heterocinode.Config, request heteroci.ProbeRequest) (heterocinode.Facts, error) {
	return function(ctx, config, request)
}
