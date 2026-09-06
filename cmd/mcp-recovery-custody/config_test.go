package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeConfigRejectsNestedDuplicateFields(t *testing.T) {
	raw := []byte(`{"schema_version":1,"instance_id":"instance-1","listen":"127.0.0.1:1","credential_file":"/tmp/credential","bootstrap":true,"max_body_bytes":1,"max_concurrent":1,"authority":{"schema_version":1,"path":"/tmp/authority.db","path":"/tmp/other.db","external_service":true}}`)
	if _, err := decodeConfig(raw); err == nil {
		t.Fatal("decodeConfig accepted duplicate field nested under authority")
	} else if strings.Contains(err.Error(), "other.db") {
		t.Fatalf("decodeConfig error leaked config content: %v", err)
	}
}

func TestDuplicateTopLevelFieldScansNestedValuesWithBounds(t *testing.T) {
	deep := `{"items":` + strings.Repeat(`[`, maxConfigJSONDepth-1) + `0` + strings.Repeat(`]`, maxConfigJSONDepth-1) + `}`
	tooDeep := `{"items":` + strings.Repeat(`[`, maxConfigJSONDepth) + `0` + strings.Repeat(`]`, maxConfigJSONDepth) + `}`
	tooManyMembers := `{"items":[` + strings.TrimSuffix(strings.Repeat(`0,`, maxConfigJSONMembers+1), `,`) + `]}`
	oversized := strings.Repeat(" ", int(maxConfigBytes)+1)
	tests := []struct {
		name      string
		raw       string
		duplicate bool
		wantErr   bool
	}{
		{name: "top-level object", raw: `{"path":"one","path":"two"}`, duplicate: true},
		{name: "nested object", raw: `{"authority":{"path":"one","path":"two"}}`, duplicate: true},
		{name: "nested array object", raw: `{"items":[{"name":"one","name":"two"}]}`, duplicate: true},
		{name: "escaped equivalent keys", raw: `{"authority":{"path":"one","\u0070ath":"two"}}`, duplicate: true},
		{name: "unique nested object", raw: `{"authority":{"path":"one","external_service":true}}`},
		{name: "depth bound", raw: tooDeep, wantErr: true},
		{name: "member bound", raw: tooManyMembers, wantErr: true},
		{name: "depth at bound", raw: deep},
		{name: "size bound", raw: oversized, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			duplicate, err := duplicateTopLevelField([]byte(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatal("duplicateTopLevelField accepted over-budget JSON")
				}
				return
			}
			if err != nil {
				t.Fatalf("duplicateTopLevelField() error = %v", err)
			}
			if duplicate != tc.duplicate {
				t.Fatalf("duplicateTopLevelField() = %v, want %v", duplicate, tc.duplicate)
			}
		})
	}
}

func TestLoadConfigEnforcesPrivateBoundedFile(t *testing.T) {
	tests := map[string]struct {
		prepare func(t *testing.T, path, parent string)
	}{
		"oversized": {
			prepare: func(t *testing.T, path, _ string) {
				t.Helper()
				payload := strings.Repeat("oversized-secret-", int(maxConfigBytes/int64(len("oversized-secret-")))+1)
				if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		"symlink": {
			prepare: func(t *testing.T, path, parent string) {
				t.Helper()
				target := filepath.Join(parent, "target.json")
				writeValidConfigFile(t, target)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		"file_mode": {
			prepare: func(t *testing.T, path, _ string) {
				t.Helper()
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		"parent_mode": {
			prepare: func(t *testing.T, _, parent string) {
				t.Helper()
				if err := os.Chmod(parent, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "config.json")
			writeValidConfigFile(t, path)
			tc.prepare(t, path, parent)
			if _, err := loadConfig(path); err == nil {
				t.Fatal("loadConfig accepted insecure configuration file")
			} else if strings.Contains(err.Error(), "oversized-secret") {
				t.Fatalf("loadConfig error leaked config content: %v", err)
			}
		})
	}
}

func TestLoadConfigRejectsParentReplacement(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "config")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "config.json")
	writeValidConfigFile(t, path)
	backup := filepath.Join(root, "config-old")
	_, err := loadConfigWithOpener(path, func(path string) (*os.File, error) {
		if err := os.Rename(parent, backup); err != nil {
			return nil, err
		}
		if err := os.Mkdir(parent, 0o700); err != nil {
			return nil, err
		}
		writeValidConfigFile(t, path)
		return os.Open(path)
	})
	if err == nil {
		t.Fatal("loadConfig accepted a replaced config parent")
	}
}

func TestDecodeConfigRejectsGenericDatabasePaths(t *testing.T) {
	raw, err := json.Marshal(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeConfig(raw); err != nil {
		t.Fatalf("decodeConfig rejected private-only service config: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	authority, ok := object["authority"].(map[string]any)
	if !ok {
		t.Fatal("authority config is not an object")
	}
	authority["product_sqlite_path"] = "/tmp/product.db"
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeConfig(raw); err == nil {
		t.Fatal("decodeConfig accepted generic product path in external service schema")
	}
}

func TestReadCredentialRejectsOversizedAndSymlinkParent(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "credential")
		if err := os.WriteFile(path, []byte(strings.Repeat("credential-secret-", int(maxCredentialBytes/int64(len("credential-secret-")))+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCredential(path); err == nil {
			t.Fatal("readCredential accepted oversized credential")
		} else if strings.Contains(err.Error(), "credential-secret") {
			t.Fatalf("readCredential error leaked credential: %v", err)
		}
	})
	t.Run("symlink_parent", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "credential"), []byte("test-token"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := readCredential(filepath.Join(link, "credential")); err == nil {
			t.Fatal("readCredential accepted symlink parent")
		}
	})
}

func writeValidConfigFile(t *testing.T, path string) {
	t.Helper()
	raw, err := json.Marshal(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
