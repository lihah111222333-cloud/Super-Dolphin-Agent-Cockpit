package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type skillInfoConsumerField struct {
	Field     string `json:"field"`
	Validator string `json:"validator"`
	Consumer  string `json:"consumer"`
}

// TestSkillInfoConsumerRegistryMatchesProducerJSONFields 将 SkillInfo producer 字段绑定到前端 validator registry。
func TestSkillInfoConsumerRegistryMatchesProducerJSONFields(t *testing.T) {
	t.Parallel()

	fields := collectRegisteredWireDTOJSONFields(t, reflect.TypeFor[contract.SkillInfo]())
	registry := loadSkillInfoConsumerRegistry(t)
	registered := make(map[string]string, len(registry))
	for _, entry := range registry {
		if strings.TrimSpace(entry.Field) == "" || strings.TrimSpace(entry.Validator) == "" || strings.TrimSpace(entry.Consumer) == "" {
			t.Fatalf("SkillInfo consumer registry contains incomplete entry: %+v", entry)
		}
		if _, exists := registered[entry.Field]; exists {
			t.Fatalf("SkillInfo consumer registry contains duplicate field %q", entry.Field)
		}
		registered[entry.Field] = entry.Validator
	}

	assertRegisteredWireDTOFieldsCovered(t, "internal/contract.SkillInfo -> skillSlashCommandAdapter", fields, registered)
	assertRegisteredWireDTORegistryCurrent(t, "internal/contract.SkillInfo -> skillSlashCommandAdapter", fields, registered)
}

func loadSkillInfoConsumerRegistry(t *testing.T) []skillInfoConsumerField {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve wire DTO field registry test path")
	}
	path := filepath.Join(
		filepath.Dir(currentFile),
		"..", "..", "frontend-app", "src", "features", "slash-commands", "adapters", "skillInfoFieldRegistry.json",
	)
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read SkillInfo consumer registry: %v", err)
	}
	var registry []skillInfoConsumerField
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatalf("decode SkillInfo consumer registry: %v", err)
	}
	if len(registry) == 0 {
		t.Fatal("SkillInfo consumer registry must not be empty")
	}
	return registry
}

func collectRegisteredWireDTOJSONFields(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		t.Fatalf("wire DTO producer %s must be a struct", typ)
	}
	fields := make(map[string]bool)
	collectRegisteredWireDTOJSONFieldsInto(t, typ, fields)
	if len(fields) == 0 {
		t.Fatalf("wire DTO producer %s has zero JSON fields", typ)
	}
	return fields
}

func collectRegisteredWireDTOJSONFieldsInto(t *testing.T, typ reflect.Type, fields map[string]bool) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous && field.Tag.Get("json") == "" {
			collectRegisteredWireDTOJSONFieldsInto(t, field.Type, fields)
			continue
		}
		tag := strings.TrimSpace(field.Tag.Get("json"))
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		if tag == "" || tag == "-" {
			continue
		}
		if fields[tag] {
			t.Fatalf("wire DTO producer %s duplicates JSON field %q", typ, tag)
		}
		fields[tag] = true
	}
}

func assertRegisteredWireDTOFieldsCovered(t *testing.T, name string, fields map[string]bool, registered map[string]string) {
	t.Helper()
	var missing []string
	for field := range fields {
		if _, ok := registered[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%s has JSON fields missing from consumer registry: %v", name, missing)
	}
}

func assertRegisteredWireDTORegistryCurrent(t *testing.T, name string, fields map[string]bool, registered map[string]string) {
	t.Helper()
	var stale []string
	for field := range registered {
		if !fields[field] {
			stale = append(stale, field)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("%s consumer registry references fields that no longer exist: %v", name, stale)
	}
}
