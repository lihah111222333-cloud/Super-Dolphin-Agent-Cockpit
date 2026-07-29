package providerrecovery

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const testProviderUUID = "019e218f-b514-7733-be85-b3ee7f6a78a6"

func TestResolveCodexOfficialUUIDWithoutLocalRollout(t *testing.T) {
	t.Parallel()

	result, err := Resolve(Request{
		Provider:         "codex",
		ProviderThreadID: testProviderUUID,
		CodexHome:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.ProviderThreadID != testProviderUUID {
		t.Fatalf("ProviderThreadID = %q, want %s", result.ProviderThreadID, testProviderUUID)
	}
	if result.IdentitySource != IdentitySourceProviderThreadID {
		t.Fatalf("IdentitySource = %q, want %q", result.IdentitySource, IdentitySourceProviderThreadID)
	}
	if result.ArtifactPolicy != ArtifactPolicyOptionalMissing {
		t.Fatalf("ArtifactPolicy = %q, want %q", result.ArtifactPolicy, ArtifactPolicyOptionalMissing)
	}
}

func TestResolveClaudeMissingRootIsTypedNotFound(t *testing.T) {
	t.Parallel()

	_, err := Resolve(Request{
		Provider:         "claude",
		ProviderThreadID: testProviderUUID,
		ClaudeHome:       filepath.Join(t.TempDir(), "missing"),
	})
	if !IsKind(err, ErrorKindNotFound) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindNotFound)
	}
}

func TestResolvePropagatesPermissionError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod history: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, err := Resolve(Request{
		Provider:         "claude",
		ProviderThreadID: testProviderUUID,
		RolloutPath:      path,
	})
	if !IsKind(err, ErrorKindPermission) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindPermission)
	}
}

func TestResolvePropagatesCorruptJSONL(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	_, err := Resolve(Request{
		Provider:         "claude",
		ProviderThreadID: testProviderUUID,
		RolloutPath:      path,
	})
	if !IsKind(err, ErrorKindParse) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindParse)
	}
}

func TestResolvePropagatesNonRegularArtifactAsIO(t *testing.T) {
	t.Parallel()

	_, err := Resolve(Request{
		Provider:         "claude",
		ProviderThreadID: testProviderUUID,
		RolloutPath:      t.TempDir(),
	})
	if !IsKind(err, ErrorKindIO) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindIO)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("non-regular artifact error = %v, must not downgrade to not found", err)
	}
}

func TestResolveRejectsUnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := Resolve(Request{
		Provider:         "future-provider",
		ProviderThreadID: testProviderUUID,
	})
	if !IsKind(err, ErrorKindUnknownProvider) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindUnknownProvider)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown provider error = %v, must not downgrade to not found", err)
	}
}

func TestResolveOldSnapshotUsesSessionUUIDExplicitly(t *testing.T) {
	t.Parallel()

	result, err := Resolve(Request{
		Provider:         "codex",
		ProviderThreadID: "agent_1778679524655355000",
		SessionUUID:      testProviderUUID,
		CodexHome:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.ProviderThreadID != testProviderUUID {
		t.Fatalf("ProviderThreadID = %q, want %s", result.ProviderThreadID, testProviderUUID)
	}
	if result.IdentitySource != IdentitySourceLegacySessionUUID {
		t.Fatalf("IdentitySource = %q, want %q", result.IdentitySource, IdentitySourceLegacySessionUUID)
	}
}

func TestResolveRejectsPlaceholderWithoutCompatibleUUID(t *testing.T) {
	t.Parallel()

	_, err := Resolve(Request{
		Provider:         "codex",
		ProviderThreadID: "agent_1778679524655355000",
	})
	if !IsKind(err, ErrorKindInvalidIdentity) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindInvalidIdentity)
	}
}

func TestResolveOptionalOmitsPlaceholderButRejectsUnknownProvider(t *testing.T) {
	t.Parallel()

	result, err := ResolveOptional(Request{
		Provider:         "codex",
		ProviderThreadID: "agent_legacy",
		CodexHome:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ResolveOptional(codex placeholder) error = %v", err)
	}
	if result.ProviderThreadID != "" {
		t.Fatalf("ResolveOptional(codex placeholder) provider thread id = %q, want empty", result.ProviderThreadID)
	}
	if result.IdentitySource != IdentitySourceNoCandidate ||
		result.ArtifactPolicy != ArtifactPolicyNotApplicable {
		t.Fatalf("ResolveOptional(codex placeholder) result = %#v, want explicit no-candidate result", result)
	}

	_, err = ResolveOptional(Request{Provider: "future-provider"})
	if !IsKind(err, ErrorKindUnknownProvider) {
		t.Fatalf("ResolveOptional(unknown) error = %v, want %q", err, ErrorKindUnknownProvider)
	}
}
