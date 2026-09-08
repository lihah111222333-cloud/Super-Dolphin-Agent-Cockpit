package standalone

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigStrictContract(t *testing.T) {
	valid := validTestConfig(t)
	raw := marshalConfigTest(t, valid)
	parsed, err := ParseConfig(raw)
	if err != nil || !reflect.DeepEqual(parsed, valid) {
		t.Fatalf("valid config roundtrip = %#v, %v", parsed, err)
	}
	t.Run("invalid_json_and_fields", func(t *testing.T) { testInvalidConfigJSON(t, raw) })
	t.Run("required_fields_dynamic", func(t *testing.T) { testRequiredConfigFields(t, raw) })
	t.Run("policy_validation", func(t *testing.T) { testConfigPolicy(t, valid) })
	t.Run("load_explicit_file_only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "orch.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadConfig(path)
		if err != nil || !reflect.DeepEqual(loaded, valid) {
			t.Fatalf("LoadConfig = %#v, %v", loaded, err)
		}
		for _, invalid := range []string{"", "orch.json", t.TempDir(), filepath.Join(t.TempDir(), "missing.json")} {
			if _, err := LoadConfig(invalid); err == nil {
				t.Fatalf("LoadConfig accepted invalid path %q", invalid)
			}
		}
	})
}

func TestParseConfigAllowsOmittedClaudeSandbox(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		SchemaVersion: 1, WorkspaceRoot: t.TempDir(), StateDir: t.TempDir(),
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(t.TempDir(), "token"),
		PrincipalRef: "operator/test", ScopeRef: "workspace/test",
		Providers: []Provider{{Name: "claude", Executable: executable, Home: t.TempDir(), Models: []string{"claude-model"}}},
	}
	parsed, err := ParseConfig(marshalConfigTest(t, cfg))
	if err != nil {
		t.Fatalf("ParseConfig(Claude without sandbox) error = %v", err)
	}
	if parsed.Providers[0].Sandbox != "" {
		t.Fatalf("Claude sandbox = %q, want empty", parsed.Providers[0].Sandbox)
	}
}

func TestParseConfigStillRequiresCodexSandbox(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Providers[0].Sandbox = ""
	if _, err := ParseConfig(marshalConfigTest(t, cfg)); err == nil {
		t.Fatal("ParseConfig accepted Codex config without sandbox")
	}
}

func TestClaudeAuthModeContract(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := Config{SchemaVersion: 1, WorkspaceRoot: t.TempDir(), StateDir: t.TempDir(),
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(t.TempDir(), "token"),
		PrincipalRef: "operator/test", ScopeRef: "workspace/test",
		Providers: []Provider{{Name: "claude", Executable: executable, Home: t.TempDir(), Models: []string{"claude-model"}}}}
	for _, mode := range []string{ClaudeAuthProviderHome, ClaudeAuthHostDefault} {
		cfg := base
		cfg.Providers = []Provider{{Name: "claude", Executable: executable, Home: base.Providers[0].Home, Models: []string{"claude-model"}, AuthMode: mode}}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Claude auth_mode %q rejected: %v", mode, err)
		}
	}
	invalid := base
	invalid.Providers = []Provider{{Name: "claude", Executable: executable, Home: base.Providers[0].Home, Models: []string{"claude-model"}, AuthMode: "implicit"}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid Claude auth_mode accepted")
	}
}

func validTestConfig(t *testing.T) Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		SchemaVersion: 1, WorkspaceRoot: t.TempDir(), StateDir: t.TempDir(),
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(t.TempDir(), "token"),
		PrincipalRef: "operator/test", ScopeRef: "workspace/test",
		Providers: []Provider{{Name: "codex", Executable: executable, Home: t.TempDir(), Models: []string{"test-model"}, Sandbox: "read-only"}},
	}
}

func TestValidateServeIdentityRejectsMissingOrNonCanonicalRefs(t *testing.T) {
	valid := validTestConfig(t)
	for name, mutate := range map[string]func(*Config){
		"missing principal": func(c *Config) { c.PrincipalRef = "" },
		"missing scope":     func(c *Config) { c.ScopeRef = "" },
		"untrimmed":         func(c *Config) { c.ScopeRef = " workspace/test" },
		"nul":               func(c *Config) { c.PrincipalRef = "operator\x00test" },
		"oversized":         func(c *Config) { c.ScopeRef = strings.Repeat("s", 513) },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			if err := cfg.ValidateServeIdentity(); err == nil {
				t.Fatal("invalid serve identity accepted")
			}
		})
	}
}

func marshalConfigTest(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testInvalidConfigJSON(t *testing.T, raw []byte) {
	t.Helper()
	tests := map[string]string{
		"empty": "", "null": "null", "array": "[]", "empty_object": "{}", "malformed": "{",
		"trailing":         string(raw) + "{}",
		"duplicate":        strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"case_alias":       strings.Replace(string(raw), `"listen"`, `"Listen"`, 1),
		"unknown":          strings.Replace(string(raw), `"listen"`, `"surprise"`, 1),
		"nested_duplicate": strings.Replace(string(raw), `"name":"codex"`, `"name":"codex","name":"claude"`, 1),
		"nested_unknown":   strings.Replace(string(raw), `"name":"codex"`, `"name":"codex","extra":true`, 1),
		"null_provider":    strings.Replace(string(raw), `"providers":[{`, `"providers":[null,{`, 1),
		"wrong_type":       strings.Replace(string(raw), `"schema_version":1`, `"schema_version":"1"`, 1),
		"oversized":        strings.Repeat(" ", maxConfigBytes+1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(input)); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

// testRequiredConfigFields 动态移除 producer 的每个字段，避免新增字段未验证仍报绿。
func testRequiredConfigFields(t *testing.T, raw []byte) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	assertRequiredConfigObject(t, top, reflect.TypeFor[Config](), func(item map[string]json.RawMessage) []byte {
		return marshalConfigTest(t, item)
	})
	assertProviderConfigFieldMatrix(t, validTestConfig(t))
}

func assertRequiredConfigObject(t *testing.T, object map[string]json.RawMessage, producer reflect.Type, encode func(map[string]json.RawMessage) []byte) {
	t.Helper()
	for i := 0; i < producer.NumField(); i++ {
		name := strings.Split(producer.Field(i).Tag.Get("json"), ",")[0]
		saved, exists := object[name]
		if !exists {
			t.Fatalf("producer %s missing in fixture", name)
		}
		delete(object, name)
		if _, err := ParseConfig(encode(object)); err == nil {
			t.Fatalf("missing producer field %s accepted", name)
		}
		object[name] = json.RawMessage("null")
		if _, err := ParseConfig(encode(object)); err == nil {
			t.Fatalf("null producer field %s accepted", name)
		}
		object[name] = saved
	}
}

func testConfigPolicy(t *testing.T, valid Config) {
	t.Helper()
	tests := map[string]func(*Config){
		"schema":                func(c *Config) { c.SchemaVersion = 2 },
		"relative_workspace":    func(c *Config) { c.WorkspaceRoot = "." },
		"missing_state":         func(c *Config) { c.StateDir = filepath.Join(c.StateDir, "missing") },
		"public_listen":         func(c *Config) { c.Listen = "0.0.0.0:1234" },
		"hostname_listen":       func(c *Config) { c.Listen = "localhost:1234" },
		"invalid_port":          func(c *Config) { c.Listen = "127.0.0.1:65536" },
		"empty_token_path":      func(c *Config) { c.CredentialFile = "" },
		"missing_principal":     func(c *Config) { c.PrincipalRef = "" },
		"missing_scope":         func(c *Config) { c.ScopeRef = "" },
		"untrimmed_scope":       func(c *Config) { c.ScopeRef = " workspace/test" },
		"empty_providers":       func(c *Config) { c.Providers = []Provider{} },
		"duplicate_provider":    func(c *Config) { c.Providers = append(c.Providers, c.Providers[0]) },
		"unknown_provider":      func(c *Config) { c.Providers[0].Name = "other" },
		"path_lookup_forbidden": func(c *Config) { c.Providers[0].Executable = "codex" },
		"missing_home":          func(c *Config) { c.Providers[0].Home = "" },
		"missing_model":         func(c *Config) { c.Providers[0].Models = nil },
		"duplicate_model":       func(c *Config) { c.Providers[0].Models = []string{"model", "model"} },
		"empty_model":           func(c *Config) { c.Providers[0].Models = []string{" "} },
		"missing_sandbox":       func(c *Config) { c.Providers[0].Sandbox = "" },
		"invalid_sandbox":       func(c *Config) { c.Providers[0].Sandbox = "network" },
		"untrimmed_sandbox":     func(c *Config) { c.Providers[0].Sandbox = " workspace-write" },
		"claude_sandbox": func(c *Config) {
			c.Providers[0] = Provider{Name: "claude", Executable: c.Providers[0].Executable, Home: c.Providers[0].Home, Models: []string{"claude-model"}, Sandbox: "workspace-write"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			cfg.Providers = append([]Provider(nil), valid.Providers...)
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}
