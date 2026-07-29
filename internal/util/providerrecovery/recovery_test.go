package providerrecovery

import (
	"errors"
	"fmt"
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

	home := t.TempDir()
	path := writeClaudeRecoveryArtifact(t, home, testProviderUUID, testProviderUUID)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod history: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, err := Resolve(Request{
		Provider:         "claude",
		ProviderThreadID: testProviderUUID,
		RolloutPath:      path,
		ClaudeHome:       home,
	})
	if !IsKind(err, ErrorKindPermission) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindPermission)
	}
}

func TestResolvePropagatesCorruptJSONL(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := writeClaudeRecoveryArtifact(t, home, testProviderUUID, testProviderUUID)
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	_, err := Resolve(Request{
		Provider:         "claude",
		ProviderThreadID: testProviderUUID,
		RolloutPath:      path,
		ClaudeHome:       home,
	})
	if !IsKind(err, ErrorKindParse) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindParse)
	}
}

func TestResolvePropagatesNonRegularArtifactAsIO(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := filepath.Join(home, "projects", "project", testProviderUUID+".jsonl")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir non-regular artifact: %v", err)
	}
	_, err := Resolve(Request{
		Provider:         "claude",
		ProviderThreadID: testProviderUUID,
		RolloutPath:      path,
		ClaudeHome:       home,
	})
	if !IsKind(err, ErrorKindIO) {
		t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindIO)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("non-regular artifact error = %v, must not downgrade to not found", err)
	}
}

func TestResolveClassifiesArtifactDeletionAfterValidationAsIO(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := writeClaudeRecoveryArtifact(t, home, testProviderUUID, testProviderUUID)
	req := Request{
		Provider:         "claude",
		ProviderThreadID: testProviderUUID,
		RolloutPath:      path,
		ClaudeHome:       home,
	}
	if _, err := Resolve(req); err != nil {
		t.Fatalf("prime Resolve() error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove validated artifact: %v", err)
	}
	_, err := Resolve(req)
	if !IsKind(err, ErrorKindIO) {
		t.Fatalf("Resolve() after deletion error = %v, want %q", err, ErrorKindIO)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("validated artifact deletion error = %v, must not downgrade to not found", err)
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

func TestResolveCanonicalizesCompatibleHistoricUUIDForms(t *testing.T) {
	t.Parallel()

	for _, candidate := range []string{
		"019e218fb5147733be85b3ee7f6a78a6",
		"019E218F-B514-7733-BE85-B3EE7F6A78A6",
		"019E218FB5147733BE85B3EE7F6A78A6",
	} {
		result, err := Resolve(Request{
			Provider:         "codex",
			ProviderThreadID: candidate,
			CodexHome:        t.TempDir(),
		})
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", candidate, err)
		}
		if result.ProviderThreadID != testProviderUUID {
			t.Fatalf("Resolve(%q) ProviderThreadID = %q, want %q", candidate, result.ProviderThreadID, testProviderUUID)
		}
	}
}

func TestResolveOptionalRejectsMalformedPersistedIdentity(t *testing.T) {
	t.Parallel()

	for _, candidate := range []string{
		"not-a-uuid",
		" " + testProviderUUID,
		"00000000-0000-0000-0000-000000000000",
	} {
		_, err := ResolveOptional(Request{
			Provider:         "codex",
			ProviderThreadID: candidate,
			CodexHome:        t.TempDir(),
		})
		if !IsKind(err, ErrorKindInvalidIdentity) {
			t.Fatalf("Resolve(%q) error = %v, want %q", candidate, err, ErrorKindInvalidIdentity)
		}
	}
}

func TestResolveRecoveryArtifactIdentityAndContainmentMatrix(t *testing.T) {
	t.Parallel()

	const otherUUID = "119e218f-b514-7733-be85-b3ee7f6a78a6"
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	matchingCodex := writeCodexRecoveryArtifact(t, codexHome, testProviderUUID, testProviderUUID)
	mismatchedCodex := writeCodexRecoveryArtifact(t, codexHome, otherUUID, otherUUID)
	crossProvider := writeClaudeRecoveryArtifact(t, claudeHome, testProviderUUID, testProviderUUID)
	symlinkPath := filepath.Join(codexHome, "sessions", "linked-"+testProviderUUID+".jsonl")
	if err := os.Symlink(matchingCodex, symlinkPath); err != nil {
		t.Fatalf("symlink recovery artifact: %v", err)
	}

	tests := []struct {
		name string
		req  Request
	}{
		{
			name: "artifact UUID mismatch",
			req: Request{
				Provider:         "codex",
				ProviderThreadID: testProviderUUID,
				RolloutPath:      mismatchedCodex,
				CodexHome:        codexHome,
			},
		},
		{
			name: "cross provider root",
			req: Request{
				Provider:         "codex",
				ProviderThreadID: testProviderUUID,
				RolloutPath:      crossProvider,
				CodexHome:        codexHome,
			},
		},
		{
			name: "symlink",
			req: Request{
				Provider:         "codex",
				ProviderThreadID: testProviderUUID,
				RolloutPath:      symlinkPath,
				CodexHome:        codexHome,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tc.req)
			if !IsKind(err, ErrorKindInvalidIdentity) {
				t.Fatalf("Resolve() error = %v, want %q", err, ErrorKindInvalidIdentity)
			}
			if errors.Is(err, ErrNotFound) {
				t.Fatalf("Resolve() error = %v, must not downgrade to not found", err)
			}
		})
	}
}

func TestResolveDoesNotUsePublicThreadAsRecoveryCandidate(t *testing.T) {
	t.Parallel()

	codexHome := t.TempDir()
	const publicThreadUUID = "219e218f-b514-7733-be85-b3ee7f6a78a6"
	writeCodexRecoveryArtifact(t, codexHome, publicThreadUUID, publicThreadUUID)
	result, err := Resolve(Request{
		Provider:         "codex",
		ProviderThreadID: testProviderUUID,
		CodexHome:        codexHome,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.ArtifactPolicy != ArtifactPolicyOptionalMissing || result.ArtifactPath != "" {
		t.Fatalf("Resolve() result = %#v, want selected UUID optional-missing", result)
	}
}

func writeCodexRecoveryArtifact(t *testing.T, home, fileUUID, artifactUUID string) string {
	t.Helper()
	path := filepath.Join(home, "sessions", "2026", "07", "29", "rollout-test-"+fileUUID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir Codex recovery root: %v", err)
	}
	raw := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q}}`+"\n", artifactUUID)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write Codex recovery artifact: %v", err)
	}
	return path
}

func writeClaudeRecoveryArtifact(t *testing.T, home, fileUUID, artifactUUID string) string {
	t.Helper()
	path := filepath.Join(home, "projects", "project", fileUUID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir Claude recovery root: %v", err)
	}
	raw := fmt.Sprintf(`{"type":"user","sessionId":%q,"message":{"role":"user","content":[]}}`+"\n", artifactUUID)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write Claude recovery artifact: %v", err)
	}
	return path
}
