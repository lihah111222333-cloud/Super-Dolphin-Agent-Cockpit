package standalone

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// assertProviderConfigFieldMatrix 动态枚举每个 provider 字段，明确校验必填、可省略和禁止非空的不同语义。
func assertProviderConfigFieldMatrix(t *testing.T, base Config) {
	t.Helper()
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			cfg := base
			cfg.Providers = append([]Provider(nil), base.Providers...)
			cfg.Providers[0].Name = provider
			if provider == "claude" {
				cfg.Providers[0].Sandbox = ""
				cfg.Providers[0].AuthMode = ClaudeAuthProviderHome
			}
			object := providerConfigFixture(t, cfg.Providers[0])
			assertProviderOptionalRegistry(t)
			producer := reflect.TypeFor[Provider]()
			for i := 0; i < producer.NumField(); i++ {
				field := producer.Field(i)
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				t.Run(name, func(t *testing.T) { assertProviderFieldMutation(t, cfg, object, name) })
			}
		})
	}
}

// providerConfigFixture 显式序列化全部生产字段，包括仅允许空值的 provider 专属字段。
func providerConfigFixture(t *testing.T, provider Provider) map[string]json.RawMessage {
	t.Helper()
	value := reflect.ValueOf(provider)
	object := make(map[string]json.RawMessage, value.NumField())
	for i := 0; i < value.NumField(); i++ {
		name := strings.Split(value.Type().Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("unregistered producer field %s", value.Type().Field(i).Name)
		}
		object[name] = marshalConfigTest(t, value.Field(i).Interface())
	}
	return object
}

// assertProviderOptionalRegistry 让新增或删除可选字段必须同步登记其行为，未知字段不得自动跳过。
func assertProviderOptionalRegistry(t *testing.T) {
	t.Helper()
	expected := map[string]string{
		"auth_mode": "config.go Provider.validate: Claude optional explicit mode; Codex only empty/omitted",
		"sandbox":   "config.go Provider.validate: Codex required; Claude only empty/omitted",
	}
	producer := reflect.TypeFor[Provider]()
	for i := 0; i < producer.NumField(); i++ {
		tag := strings.Split(producer.Field(i).Tag.Get("json"), ",")
		if len(tag) < 2 || tag[1] != "omitempty" {
			continue
		}
		if expected[tag[0]] == "" {
			t.Fatalf("optional producer field %s lacks policy evidence", tag[0])
		}
		delete(expected, tag[0])
	}
	if len(expected) != 0 {
		t.Fatalf("stale optional field policies: %v", expected)
	}
}

// assertProviderFieldMutation 对全部字段检查缺失、null、错误类型，并依据真实 provider 规则判断省略。
func assertProviderFieldMutation(t *testing.T, cfg Config, object map[string]json.RawMessage, name string) {
	t.Helper()
	saved, exists := object[name]
	if !exists {
		t.Fatalf("producer %s missing in fixture", name)
	}
	omitAllowed := name == "auth_mode" || name == "sandbox" && cfg.Providers[0].Name == "claude"
	delete(object, name)
	_, err := ParseConfig(providerConfigJSON(t, cfg, object))
	if (err == nil) != omitAllowed {
		t.Fatalf("provider=%s omitted %s: err=%v allowed=%v", cfg.Providers[0].Name, name, err, omitAllowed)
	}
	for _, invalid := range []json.RawMessage{json.RawMessage("null"), json.RawMessage("{}")} {
		object[name] = invalid
		if _, err := ParseConfig(providerConfigJSON(t, cfg, object)); err == nil {
			t.Fatalf("provider field %s accepted %s", name, invalid)
		}
	}
	object[name] = saved
	if _, err := ParseConfig(providerConfigJSON(t, cfg, object)); err != nil {
		t.Fatalf("restored provider field %s rejected: %v", name, err)
	}
}

// providerConfigJSON 只替换当前 provider 对象，保留有效路径和其余配置以隔离字段验证。
func providerConfigJSON(t *testing.T, cfg Config, object map[string]json.RawMessage) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(marshalConfigTest(t, cfg), &root); err != nil {
		t.Fatal(err)
	}
	root["providers"] = marshalConfigTest(t, []map[string]json.RawMessage{object})
	return marshalConfigTest(t, root)
}

func TestProviderAuthAndSandboxModes(t *testing.T) {
	for _, tc := range []struct {
		provider, field, value string
		valid                  bool
	}{
		{"codex", "auth_mode", "", true}, {"codex", "auth_mode", ClaudeAuthProviderHome, false}, {"codex", "auth_mode", ClaudeAuthHostDefault, false},
		{"claude", "auth_mode", "", true}, {"claude", "auth_mode", ClaudeAuthProviderHome, true}, {"claude", "auth_mode", ClaudeAuthHostDefault, true},
		{"claude", "auth_mode", "implicit", false}, {"claude", "auth_mode", " provider_home", false}, {"claude", "auth_mode", "host_default ", false},
		{"codex", "sandbox", "", false}, {"codex", "sandbox", "read-only", true}, {"codex", "sandbox", "workspace-write", true}, {"codex", "sandbox", "danger-full-access", true},
		{"claude", "sandbox", "", true}, {"claude", "sandbox", "read-only", false},
	} {
		t.Run(tc.provider+"/"+tc.field+"/"+tc.value, func(t *testing.T) {
			cfg := validTestConfig(t)
			cfg.Providers[0].Name = tc.provider
			if tc.provider == "claude" {
				cfg.Providers[0].Sandbox = ""
			}
			object := providerConfigFixture(t, cfg.Providers[0])
			object[tc.field] = marshalConfigTest(t, tc.value)
			parsed, err := ParseConfig(providerConfigJSON(t, cfg, object))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if !tc.valid {
				return
			}
			actual := providerConfigFixture(t, parsed.Providers[0])
			if string(actual[tc.field]) != string(object[tc.field]) {
				t.Fatalf("field %s silently changed", tc.field)
			}
		})
	}
}
