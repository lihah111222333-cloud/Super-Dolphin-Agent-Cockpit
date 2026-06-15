package turn

import (
	"strings"
	"testing"
)

func TestRedactor_AllPatterns(t *testing.T) {
	r := NewDefaultRedactor()
	cases := []struct {
		name      string
		input     string
		wantHit   string // pattern name that must fire
		forbidden string // substring that must NOT survive in the output
	}{
		{"bearer", "Authorization: Bearer abc.def-secret_value", "bearer_token", "abc.def-secret_value"},
		{"jwt", "token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.signature", "jwt", "eyJhbGciOiJIUzI1NiJ9"},
		{"openai_key", "api key sk-proj-abcdefghijklmnopqrstuvwxyz1234567890", "openai_api_key", "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"},
		{"anthropic_key", "key sk-ant-abcdefghijklmnopqrstuvwxyz1234567890", "anthropic_api_key", "sk-ant-abcdefghijklmnopqrstuvwxyz1234567890"},
		{"github_direct", "token ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "github_token", "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"slack_token", "xoxb-123456789012-abcdefghijklmnop", "slack_token", "xoxb-123456789012-abcdefghijklmnop"},
		{"aws_key_id", "AWS id AKIAIOSFODNN7EXAMPLE", "aws_access_key_id", "AKIAIOSFODNN7EXAMPLE"},
		{"google_api_key", "AIzaSyD-abcdefghijklmnopqrstuvwxyz12345", "google_api_key", "AIzaSyD-abcdefghijklmnopqrstuvwxyz12345"},
		{"stripe_secret", "sk_live_abcdefghijklmnopqrstuvwxyz", "stripe_secret_key", "sk_live_abcdefghijklmnopqrstuvwxyz"},
		{"npm_token", "npm_abcdefghijklmnopqrstuvwxyzABCDEFGHIJ", "npm_token", "npm_abcdefghijklmnopqrstuvwxyzABCDEFGHIJ"},
		{"pypi_token", "pypi-abcdefghijklmnopqrstuvwxyz1234567890", "pypi_token", "pypi-abcdefghijklmnopqrstuvwxyz1234567890"},
		{"private_key_header", "-----BEGIN OPENSSH PRIVATE KEY-----", "private_key_header", "OPENSSH PRIVATE"},
		{"age_sops_header", "-----BEGIN AGE ENCRYPTED FILE-----", "age_sops_header", "AGE ENCRYPTED"},
		{"uri_credentials", "postgres://alice:secret@example.com/db", "uri_credentials", "alice:secret@"},
		{"ssh_public_key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKabcdefghijklmnopqrstuvwx1234567890", "ssh_public_key", "AAAAC3NzaC1lZDI1NTE5"},
		{"openai_env", "export OPENAI_API_KEY=sk-1234567890abcdef", "credential_env", "sk-1234567890abcdef"},
		{"anthropic_env", "ANTHROPIC_API_KEY: sk-ant-xxxxx", "credential_env", "sk-ant-xxxxx"},
		{"database_url_env", "DATABASE_URL=postgres://alice:secret@127.0.0.1:5432/super_dolphin?sslmode=disable", "credential_env", "alice:secret@"},
		{"postgres_connection_string_env", "POSTGRES_CONNECTION_STRING=postgres://alice:secret@127.0.0.1:5432/super_dolphin?sslmode=disable", "credential_env", "alice:secret@"},
		{"sqlite_path_env", "SUPER_DOLPHIN_SQLITE_PATH=/Users/alice/private/super-dolphin.db", "credential_env", "/Users/alice/private/super-dolphin.db"},
		{"internal_sqlite_path_env", "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH=/Users/alice/private/internal.db", "credential_env", "/Users/alice/private/internal.db"},
		{"cookie", "Cookie: session=abc; csrf=xyz", "http_cookie", "session=abc"},
		{"set_cookie", "Set-Cookie: foo=bar; Path=/", "http_cookie", "foo=bar"},
		{"long_base64", "key=dGVzdHRlc3R0ZXN0dGVzdHRlc3R0ZXN0dGVzdHRlc3R0ZXN0", "long_base64", "dGVzdHRlc3R0ZXN0dGVzdHRlc3R0ZXN0dGVzdHRlc3R0ZXN0"},
		{"long_hex", "hash=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "long_hex", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			out, hits, err := r.Redact(c.input)
			if err != nil {
				t.Fatalf("Redact returned err: %v", err)
			}
			if !containsHit(hits, c.wantHit) {
				t.Fatalf("hits %v do not contain %q", hits, c.wantHit)
			}
			if c.forbidden != "" && strings.Contains(out, c.forbidden) {
				t.Fatalf("output still contains forbidden %q: %s", c.forbidden, out)
			}
			if !strings.Contains(out, "[REDACTED:") {
				t.Fatalf("output missing [REDACTED: marker: %s", out)
			}
		})
	}
}

func TestRedactor_NoMatch(t *testing.T) {
	r := NewDefaultRedactor()
	out, hits, err := r.Redact("nothing sensitive here")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits, got %v", hits)
	}
	if out != "nothing sensitive here" {
		t.Fatalf("output mutated: %q", out)
	}
}

func TestRedactor_NilSafe(t *testing.T) {
	var r *DefaultRedactor
	out, hits, err := r.Redact("Bearer xxx")
	if err != nil || len(hits) != 0 || out != "Bearer xxx" {
		t.Fatalf("nil receiver should be no-op, got out=%q hits=%v err=%v", out, hits, err)
	}
}

func TestRepoFingerprint_DeterministicAndScoped(t *testing.T) {
	a := RepoFingerprint("/tmp/repo-a")
	b := RepoFingerprint("/tmp/repo-b")
	if a == b {
		t.Fatalf("different cwds produced same fingerprint: %s", a)
	}
	if a != RepoFingerprint("/tmp/repo-a") {
		t.Fatalf("not deterministic")
	}
	if len(a) != 32 {
		t.Fatalf("fingerprint length should be 32, got %d", len(a))
	}
}

func TestRepoFingerprint_EmptyCwd(t *testing.T) {
	if RepoFingerprint("") != "" {
		t.Fatal("empty cwd must produce empty fingerprint")
	}
	if RepoFingerprint("   ") != "" {
		t.Fatal("whitespace cwd must produce empty fingerprint")
	}
}

func containsHit(hits []string, want string) bool {
	for _, h := range hits {
		if h == want {
			return true
		}
	}
	return false
}

// Benchmarks — DefaultRedactor.Redact runs on every turn's content, applying
// 20+ regex patterns. These benchmarks capture both the fast path (no secret
// present) and the worst case (multiple secret types in a single input).

func BenchmarkRedact_NoMatch(b *testing.B) {
	r := NewDefaultRedactor()
	input := "This is a perfectly normal piece of text with no secrets, API keys, or tokens. " +
		"It just talks about building software and deploying to production. " +
		"No credentials were harmed in the making of this benchmark."
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Redact(input)
	}
}

func BenchmarkRedact_MultipleSecrets(b *testing.B) {
	r := NewDefaultRedactor()
	input := "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig " +
		"with key sk-ant-abcdefghijklmnopqrstuvwxyz1234567890 " +
		"and github token ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA " +
		"connecting to postgres://alice:secret@example.com/db"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Redact(input)
	}
}
