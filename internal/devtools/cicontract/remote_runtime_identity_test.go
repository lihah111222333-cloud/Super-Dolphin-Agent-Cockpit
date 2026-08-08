package cicontract

import (
	"strings"
	"testing"
)

func TestRemoteRuntimeIdentityIsFrozen(t *testing.T) {
	if RemoteRegistryServer != "ghcr.io" {
		t.Fatalf("RemoteRegistryServer = %q, want ghcr.io", RemoteRegistryServer)
	}
	if RemoteRegistryUsernameEnvironment != "SUPER_DOLPHIN_CI_GHCR_USERNAME" || RemoteRegistryTokenEnvironment != "SUPER_DOLPHIN_CI_GHCR_TOKEN" {
		t.Fatalf("GHCR environment identities drifted: %q/%q", RemoteRegistryUsernameEnvironment, RemoteRegistryTokenEnvironment)
	}
	if RemoteWorkerUID != 65532 || RemoteWorkerGID != 65532 {
		t.Fatalf("remote worker identity = %d:%d, want 65532:65532", RemoteWorkerUID, RemoteWorkerGID)
	}
}

func TestValidateRemoteRegistryCredentialFailsClosed(t *testing.T) {
	if err := ValidateRemoteRegistryCredential(RemoteRegistryServer, "ci-user", "ci-token"); err != nil {
		t.Fatalf("valid credential rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		server string
		user   string
		token  string
	}{
		{name: "missing user", server: RemoteRegistryServer, token: "ci-token"},
		{name: "wrong server", server: "registry.example", user: "ci-user", token: "ci-token"},
		{name: "leading whitespace", server: RemoteRegistryServer, user: " ci-user", token: "ci-token"},
		{name: "control token", server: RemoteRegistryServer, user: "ci-user", token: "ci\ntoken"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRemoteRegistryCredential(test.server, test.user, test.token)
			if err == nil {
				t.Fatal("invalid credential unexpectedly passed")
			}
			if (test.user != "" && strings.Contains(err.Error(), test.user)) || (test.token != "" && strings.Contains(err.Error(), test.token)) {
				t.Fatalf("credential value leaked in error: %v", err)
			}
		})
	}
}
