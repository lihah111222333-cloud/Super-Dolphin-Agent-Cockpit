package contentkeychain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeychainRecordFieldGuard(t *testing.T) {
	p, f := fixture(t)
	request := requestFixture()
	createOK(t, p, request)
	original := f.records[p.config.Namespace+accountFor(request.OperationID)]
	record, err := decodeRecord(original)
	require.NoError(t, err)
	defer clear(record.Key)
	typ := reflect.TypeOf(request)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !reflect.DeepEqual(reflect.ValueOf(record.Request).Field(i).Interface(), reflect.ValueOf(request).Field(i).Interface()) {
			t.Fatalf("missing roundtrip field %s", name)
		}
	}
	// 记录层动态枚举，删除任一字段都必须损坏；不复制生产字段清单。
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(original, &fields))
	rt := reflect.TypeOf(record)
	if len(fields) != rt.NumField() {
		t.Fatal("record fields not serialized")
	}
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		t.Run(name, func(t *testing.T) {
			value, present := fields[name]
			if !present {
				t.Fatal("missing record field")
			}
			delete(fields, name)
			mutated, err := json.Marshal(fields)
			require.NoError(t, err)
			defer clear(mutated)
			if _, err := decodeRecord(mutated); !errors.Is(err, ErrCorrupt) {
				t.Fatal("missing record field accepted")
			}
			fields[name] = value
		})
	}
	for _, value := range []string{fmt.Sprintf("%v", record), fmt.Sprintf("%+v", record), fmt.Sprintf("%#v", record)} {
		if value != "<private keychain record>" {
			t.Fatal("record formatting exposed fields")
		}
	}
}
func TestKeychainCorruptRecords(t *testing.T) {
	p, f := fixture(t)
	r := requestFixture()
	createOK(t, p, r)
	original := f.records[p.config.Namespace+accountFor(r.OperationID)]
	for _, data := range [][]byte{nil, []byte("{"), []byte("{}"), bytes.Repeat([]byte("x"), maxRecordBytes+1), append(append([]byte(nil), original...), []byte(" {}")...)} {
		if lease, err := leaseFrom(data, r); lease != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("corrupt record accepted")
		}
	}
	for _, field := range []string{"Key", "Nonce", "Schema"} {
		t.Run(field, func(t *testing.T) {
			record, err := decodeRecord(original)
			require.NoError(t, err)
			defer clear(record.Key)
			switch field {
			case "Key":
				record.Key = record.Key[:2]
			case "Nonce":
				record.Nonce = record.Nonce[:2]
			case "Schema":
				record.Schema = 2
			}
			data, err := json.Marshal(record)
			require.NoError(t, err)
			if lease, err := leaseFrom(data, r); lease != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("malformed material accepted")
			}
		})
	}
}
func TestKeychainPathValidation(t *testing.T) {
	p, _ := fixture(t)
	for _, path := range []string{"", "relative", p.config.KeychainPath + "/../invalid", filepath.Dir(p.config.KeychainPath)} {
		if _, err := privatePath(path); !errors.Is(err, ErrUnsafePath) {
			t.Fatal("unsafe path accepted")
		}
	}
	link := p.config.KeychainPath + "-link"
	require.NoError(t, os.Symlink(p.config.KeychainPath, link))
	if _, err := privatePath(link); !errors.Is(err, ErrUnsafePath) {
		t.Fatal("symlink accepted")
	}
	require.NoError(t, os.Chmod(p.config.KeychainPath, 0644))
	if _, err := privatePath(p.config.KeychainPath); !errors.Is(err, ErrUnsafePath) {
		t.Fatal("public file accepted")
	}
	require.NoError(t, os.Chmod(p.config.KeychainPath, 0600))
	require.NoError(t, os.Chmod(filepath.Dir(p.config.KeychainPath), 0755))
	if _, err := privatePath(p.config.KeychainPath); !errors.Is(err, ErrUnsafePath) {
		t.Fatal("public parent accepted")
	}
	if nativeSupported {
		// 构造失败必须发生在路径验证，绝不打开fixture作为真实钥匙串。
		if _, err := New(Config{KeychainPath: p.config.KeychainPath, Namespace: "unit"}); !errors.Is(err, ErrUnsafePath) {
			t.Fatal(err)
		}
	} else if _, err := New(Config{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestKeychainSourceBoundaries(t *testing.T) {
	native, err := os.ReadFile("native_darwin.go")
	require.NoError(t, err)
	for _, required := range []string{"#cgo LDFLAGS: -framework Security -framework CoreFoundation", "kSecUseKeychain, keychain", "kSecMatchSearchList, list", "kSecUseAuthenticationUI, kSecUseAuthenticationUIFail", "SecKeychainGetPath", "SecItemAdd(query, NULL)", "SecItemCopyMatching", "defer C.memset(secret, 0", "C.CFRelease(result)"} {
		if !bytes.Contains(native, []byte(required)) {
			t.Fatalf("missing native boundary %s", required)
		}
	}
	for _, forbidden := range []string{"SecKeychainSetDefault", "SecKeychainSetSearchList", "SecKeychainSetUserInteractionAllowed", "SecKeychainCreate(", "SecKeychainUnlock(", "SecItemUpdate(", "SecItemDelete(", "os/exec"} {
		if bytes.Contains(native, []byte(forbidden)) {
			t.Fatalf("forbidden native mutation %s", forbidden)
		}
	}
	unsupported, err := os.ReadFile("native_unsupported.go")
	require.NoError(t, err)
	if !bytes.Contains(unsupported, []byte("return nil, ErrUnsupported")) {
		t.Fatal("unsupported platform fallback")
	}
	configType := reflect.TypeOf(Config{})
	if configType.NumField() != 2 {
		t.Fatal("configuration boundary grew; review explicit provisioning contract")
	}
}
