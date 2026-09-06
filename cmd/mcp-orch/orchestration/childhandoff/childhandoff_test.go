package childhandoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/taskcredential"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type testVerifier struct{}

func (testVerifier) Verify(context.Context, common.TaskSessionIdentity) error { return nil }

type revokeSpy struct {
	mu    sync.Mutex
	count int
	lease string
}

func (s *revokeSpy) Revoke(_ context.Context, lease string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	s.lease = lease
	return nil
}

func TestMaterializeProviderConfigsArePrivateAndRedacted(t *testing.T) {
	for _, provider := range []Provider{ProviderClaude, ProviderCodex} {
		t.Run(string(provider), func(t *testing.T) {
			assertProviderMaterialized(t, provider)
		})
	}
}

func assertProviderMaterialized(t *testing.T, provider Provider) {
	t.Helper()
	lease := "lease-" + string(provider)
	owner, err := New(Config{Provider: provider, Endpoint: "http://127.0.0.1:43123/mcp", LeaseID: lease, TempRoot: privateTempDir(t), Credential: issueCredential(t, lease), Revoker: &revokeSpy{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	material, err := owner.Materialize(context.Background())
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	assertPrivateMaterial(t, material)
	env := material.Environment()
	if len(env) != 1 || !strings.Contains(env[0], "=") || material.Environment() != nil {
		t.Fatalf("environment one-shot contract failed: %q", env)
	}
	assertRedactedMaterial(t, material, env[0])
	if err := owner.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(material.ConfigPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config still exists after Close: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func assertPrivateMaterial(t *testing.T, material Materialized) {
	t.Helper()
	path := material.ConfigPath()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config metadata invalid: info=%v err=%v", info, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("temp directory mode = %v, want 700", info)
	}
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), "test-bearer-secret") {
		t.Fatalf("provider config persisted secret: err=%v", err)
	}
}

func assertRedactedMaterial(t *testing.T, material Materialized, env string) {
	t.Helper()
	encoded, err := json.Marshal(material)
	if err != nil || strings.Contains(string(encoded), env) || strings.Contains(string(encoded), material.ConfigPath()) {
		t.Fatalf("materialized JSON leaked secret/path: %s (err=%v)", encoded, err)
	}
	if strings.Contains(material.String(), material.ConfigPath()) || strings.Contains(material.String(), env) {
		t.Fatalf("materialized String leaked secret/path: %s", material)
	}
}

func TestMaterializeIsOneShotAndCancellationRevokes(t *testing.T) {
	credential := issueCredential(t, "lease-cancel")
	revoker := &revokeSpy{}
	owner, err := New(Config{Provider: ProviderClaude, Endpoint: "http://127.0.0.1:43123", LeaseID: "lease-cancel", TempRoot: privateTempDir(t), Credential: credential, Revoker: revoker})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := owner.Materialize(ctx); !errors.Is(err, ErrMaterialization) {
		t.Fatalf("canceled Materialize() error = %v, want ErrMaterialization", err)
	}
	if _, err := owner.Materialize(context.Background()); !errors.Is(err, ErrAlreadyMaterialized) {
		t.Fatalf("second Materialize() error = %v, want ErrAlreadyMaterialized", err)
	}
	if _, err := credential.TakeBearer(); !errors.Is(err, taskcredential.ErrCredentialUnavailable) {
		t.Fatalf("credential remained consumable after cancellation: %v", err)
	}
	revoker.mu.Lock()
	count := revoker.count
	revoker.mu.Unlock()
	if count != 1 {
		t.Fatalf("revoke count = %d, want 1", count)
	}
}

func TestCloseFencesLaterMaterialization(t *testing.T) {
	credential := issueCredential(t, "lease-close-fence")
	revoker := &revokeSpy{}
	owner, err := New(Config{Provider: ProviderCodex, Endpoint: "http://127.0.0.1:43123", LeaseID: "lease-close-fence", TempRoot: privateTempDir(t), Credential: credential, Revoker: revoker})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Materialize(context.Background()); !errors.Is(err, ErrAlreadyMaterialized) {
		t.Fatalf("Materialize after Close() error = %v, want ErrAlreadyMaterialized", err)
	}
	revoker.mu.Lock()
	count := revoker.count
	revoker.mu.Unlock()
	if count != 1 {
		t.Fatalf("revoke count = %d, want 1", count)
	}
}

func TestNewRejectsNonLoopbackAndDiscardsCredential(t *testing.T) {
	credential := issueCredential(t, "lease-invalid")
	revoker := &revokeSpy{}
	_, err := New(Config{Provider: ProviderCodex, Endpoint: "https://example.com/mcp", LeaseID: "unrelated-lease", TempRoot: privateTempDir(t), Credential: credential, Revoker: revoker})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
	if _, err := credential.TakeBearer(); !errors.Is(err, taskcredential.ErrCredentialUnavailable) {
		t.Fatalf("invalid config did not discard credential: %v", err)
	}
	revoker.mu.Lock()
	count := revoker.count
	revoker.mu.Unlock()
	if count != 0 {
		t.Fatalf("invalid config revoked unbound lease: %d calls", count)
	}
}

func TestValidateTempRootRejectsNonPrivateModes(t *testing.T) {
	for _, mode := range []os.FileMode{0o750, 0o702, 0o1777} {
		t.Run(fmt.Sprintf("mode_%04o", mode), func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, mode); err != nil {
				t.Fatal(err)
			}
			if err := validateTempRoot(root); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateTempRoot(%04o) error = %v, want ErrInvalidConfig", mode, err)
			}
		})
	}
}

func TestMaterializeRejectsTempRootReplacementBeforeCredentialUse(t *testing.T) {
	root := privateTempDir(t)
	lease := "lease-root-replacement"
	credential := issueCredential(t, lease)
	owner, err := New(Config{Provider: ProviderClaude, Endpoint: "http://127.0.0.1:43123", LeaseID: lease, TempRoot: root, Credential: credential, Revoker: &revokeSpy{}})
	if err != nil {
		t.Fatal(err)
	}
	backup := root + ".moved"
	if err := os.Rename(root, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Materialize(context.Background()); !errors.Is(err, ErrMaterialization) {
		t.Fatalf("Materialize() error = %v, want ErrMaterialization", err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("replacement root was used: entries=%d err=%v", len(entries), err)
	}
}

func TestCloseRefusesReplacedTempDirectory(t *testing.T) {
	root := privateTempDir(t)
	lease := "lease-dir-replacement"
	revoker := &revokeSpy{}
	owner, err := New(Config{Provider: ProviderCodex, Endpoint: "http://127.0.0.1:43123", LeaseID: lease, TempRoot: root, Credential: issueCredential(t, lease), Revoker: revoker})
	if err != nil {
		t.Fatal(err)
	}
	material, err := owner.Materialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	original := material.ConfigPath()
	dir := filepath.Dir(original)
	backup := dir + ".moved"
	if err := os.Rename(dir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "attacker-owned")
	if err := os.WriteFile(marker, []byte("must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); !errors.Is(err, ErrCleanup) {
		t.Fatalf("Close() error = %v, want ErrCleanup", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("replacement directory was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, filepath.Base(original))); err != nil {
		t.Fatalf("moved original material missing: %v", err)
	}
	revoker.mu.Lock()
	count := revoker.count
	revoker.mu.Unlock()
	if count != 1 {
		t.Fatalf("revoke count = %d, want 1", count)
	}
}

func TestRenderConfigRejectsProviderOutsideClosedSet(t *testing.T) {
	if _, _, _, err := renderConfig(Provider("agy"), "http://127.0.0.1:43123", "ENV", t.TempDir()); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("renderConfig unsupported provider error = %v, want ErrProviderDisabled", err)
	}
}

func TestSecretValueFormattingNeverExposesBearer(t *testing.T) {
	secret := &secretValue{key: "CHILD", value: []byte("raw-secret")}
	for _, rendered := range []string{secret.String(), fmt.Sprintf("%#v", secret), fmt.Sprintf("%v", secret)} {
		if strings.Contains(rendered, "raw-secret") {
			t.Fatalf("secret rendering leaked bearer: %q", rendered)
		}
	}
	encoded, err := json.Marshal(secret)
	if err != nil || strings.Contains(string(encoded), "raw-secret") {
		t.Fatalf("secret JSON leaked bearer: %s (err=%v)", encoded, err)
	}
}

func issueCredential(t *testing.T, lease string) *taskcredential.IssuedCredential {
	t.Helper()
	registry, err := taskcredential.New(testVerifier{})
	if err != nil {
		t.Fatalf("new credential registry: %v", err)
	}
	credential, err := registry.IssuePending(context.Background(), lease, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	return credential
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod temp root: %v", err)
	}
	return dir
}
