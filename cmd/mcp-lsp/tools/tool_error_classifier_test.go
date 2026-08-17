package tools

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestToolErrorClassifierWindowsPermissionCodes 验证 Windows 5/1314 只沿 typed 错误链编码授权请求。
func TestToolErrorClassifierWindowsPermissionCodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "lsp-state.db")
	for _, tc := range []struct {
		name string
		code uint32
		kind string
	}{
		{name: "access denied", code: 5, kind: "access_denied"},
		{name: "privilege not held", code: 1314, kind: "privilege_not_held"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := fmt.Errorf("ACL failure at %s: %w", path, syscall.Errno(tc.code))
			typed := securefs.NewWindowsPermissionError("check ACL", path, cause)
			wrapped := fmt.Errorf("runtime wrapper: %w", typed)

			classification, ok := ToolErrorClassifier("lsp/inspect", wrapped)
			if !ok {
				t.Fatal("ToolErrorClassifier() did not classify typed Windows permission error")
			}
			if classification.Code != toolErrorCodeAuthorizationRequired || classification.Retryable {
				t.Fatalf("classification = %+v, want non-retryable authorization_required", classification)
			}
			if strings.Contains(strings.ToLower(classification.Hint), "popup") ||
				strings.Contains(strings.ToLower(classification.Hint), "elevat") ||
				strings.Contains(strings.ToLower(classification.Hint), "prompt") {
				t.Fatalf("classification hint makes an unsupported UI/elevation claim: %q", classification.Hint)
			}
			if got := classification.Meta[toolErrorMetaAuthorizationRequired]; got != true {
				t.Fatalf("authorization meta = %#v, want true", got)
			}
			if got := classification.Meta[toolErrorMetaWindowsErrorCode]; got != tc.code {
				t.Fatalf("Windows error code meta = %#v, want %d", got, tc.code)
			}
			if got := classification.Meta[toolErrorMetaWindowsPermissionKind]; got != tc.kind {
				t.Fatalf("Windows permission kind meta = %#v, want %q", got, tc.kind)
			}
			serializedMeta := fmt.Sprintf("%v", classification.Meta)
			if strings.Contains(serializedMeta, path) || strings.Contains(serializedMeta, filepath.Dir(path)) {
				t.Fatalf("classification meta leaked path: %q", serializedMeta)
			}

			envelope := newToolErrorEnvelope("inspect", "go", wrapped)
			if envelope.Code != toolErrorCodeAuthorizationRequired || envelope.Meta[toolErrorMetaAuthorizationRequired] != true {
				t.Fatalf("envelope = %+v, want authorization_required classifier output", envelope)
			}
			if strings.Contains(envelope.Error, path) || strings.Contains(envelope.Error, filepath.Dir(path)) {
				t.Fatalf("envelope leaked path: %q", envelope.Error)
			}
		})
	}
}

// TestToolErrorClassifierIgnoresUnclassifiedErrors 验证 raw errno、其他码和退出文本不会触发授权分类。
func TestToolErrorClassifierIgnoresUnclassifiedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lsp-state.db")
	typedUnknown := securefs.NewWindowsPermissionError("check ACL", path, syscall.Errno(13))
	for _, err := range []error{
		syscall.Errno(5),
		typedUnknown,
		errors.New("process exited with code 5"),
		errors.New("windows_error_code=1314"),
	} {
		if classification, ok := ToolErrorClassifier("inspect", err); ok {
			t.Fatalf("error %T classified as authorization: %+v", err, classification)
		}
	}
}

// TestWindowsAuthorizationMetaFieldCoverageGuard 动态枚举 meta producer 字段，阻断缺失或陈旧 wire key。
func TestWindowsAuthorizationMetaFieldCoverageGuard(t *testing.T) {
	value := windowsAuthorizationMeta{
		AuthorizationRequired: true,
		WindowsErrorCode:      1314,
		WindowsPermissionKind: "privilege_not_held",
	}
	meta := value.asMap()
	typeOfValue := reflect.TypeOf(value)
	producerFields := make(map[string]reflect.Value, typeOfValue.NumField())
	producerKeys := make(map[string]struct{}, typeOfValue.NumField())
	for index := 0; index < typeOfValue.NumField(); index++ {
		field := typeOfValue.Field(index)
		if !field.IsExported() {
			continue
		}
		key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if key == "" || key == "-" {
			t.Fatalf("meta field %s has no stable json key", field.Name)
		}
		producerFields[key] = reflect.ValueOf(value).Field(index)
		producerKeys[key] = struct{}{}
	}

	missing := make([]string, 0)
	for key, fieldValue := range producerFields {
		got, ok := meta[key]
		if !ok {
			missing = append(missing, key)
			continue
		}
		if !reflect.DeepEqual(got, fieldValue.Interface()) {
			t.Errorf("meta key %q = %#v, want producer value %#v", key, got, fieldValue.Interface())
		}
	}
	stale := make([]string, 0)
	for key := range meta {
		if _, ok := producerKeys[key]; !ok {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("Windows authorization meta field coverage drift: missing=%v stale=%v", missing, stale)
	}
}
