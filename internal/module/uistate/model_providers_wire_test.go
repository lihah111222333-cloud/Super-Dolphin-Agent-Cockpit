package uistate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestModelProviderOptionalObjectsOmitZeroAndKeepNonZero(t *testing.T) {
	tests := []struct {
		name    string
		vendor  modelProviderVendor
		field   string
		present bool
	}{
		{name: "zero budget", field: "budget"},
		{name: "nonzero budget", vendor: modelProviderVendor{Budget: modelProviderBudget{DailyUSD: 1}}, field: "budget", present: true},
		{name: "zero token pool", field: "tokenPool"},
		{name: "nonzero token pool", vendor: modelProviderVendor{TokenPool: modelProviderTokenPool{Priority: 1}}, field: "tokenPool", present: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.vendor)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if _, got := fields[tt.field]; got != tt.present {
				t.Fatalf("field %q presence = %v, want %v; JSON = %s", tt.field, got, tt.present, raw)
			}
		})
	}
}

func TestModelProviderGoAndFrontendValidatorFieldsStayInSync(t *testing.T) {
	validatorPath := filepath.Join("..", "..", "..", "frontend-app", "src", "shared", "api", "response-validators", "mcp-model-provider.js")
	source, err := os.ReadFile(validatorPath)
	if err != nil {
		t.Fatalf("read frontend validator source: %v", err)
	}
	tests := []struct {
		name       string
		producer   reflect.Type
		consumerID string
	}{
		{name: "vendor", producer: reflect.TypeFor[modelProviderVendor](), consumerID: "MODEL_PROVIDER_VENDOR_KEYS"},
		{name: "budget", producer: reflect.TypeFor[modelProviderBudget](), consumerID: "MODEL_PROVIDER_BUDGET_KEYS"},
		{name: "token pool", producer: reflect.TypeFor[modelProviderTokenPool](), consumerID: "MODEL_PROVIDER_TOKEN_POOL_KEYS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer := jsonFieldSet(t, tt.producer)
			consumer := javascriptSet(t, source, tt.consumerID)
			if missing, stale := fieldSetDiff(producer, consumer); len(missing) > 0 || len(stale) > 0 {
				t.Fatalf("wire fields drifted: missing=%v stale=%v", missing, stale)
			}
		})
	}
}

func jsonFieldSet(t *testing.T, typ reflect.Type) map[string]struct{} {
	t.Helper()
	fields := make(map[string]struct{}, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("%s.%s lacks a wire JSON field", typ.Name(), typ.Field(i).Name)
		}
		if _, exists := fields[name]; exists {
			t.Fatalf("%s repeats JSON field %q", typ.Name(), name)
		}
		fields[name] = struct{}{}
	}
	return fields
}

func javascriptSet(t *testing.T, source []byte, identifier string) map[string]struct{} {
	t.Helper()
	declaration := regexp.MustCompile(`const\s+` + regexp.QuoteMeta(identifier) + `\s*=\s*new Set\(\[([^\]]*)\]\);`)
	match := declaration.FindSubmatch(source)
	if match == nil {
		t.Fatalf("frontend validator set %s is missing or not statically enumerable", identifier)
	}
	quoted := regexp.MustCompile(`['\"]([^'\"]+)['\"]`)
	fields := make(map[string]struct{})
	for _, item := range quoted.FindAllSubmatch(match[1], -1) {
		name := string(item[1])
		if _, exists := fields[name]; exists {
			t.Fatalf("frontend validator set %s repeats %q", identifier, name)
		}
		fields[name] = struct{}{}
	}
	if len(fields) == 0 {
		t.Fatalf("frontend validator set %s is empty", identifier)
	}
	return fields
}

func fieldSetDiff(producer, consumer map[string]struct{}) (missing, stale []string) {
	for field := range producer {
		if _, ok := consumer[field]; !ok {
			missing = append(missing, field)
		}
	}
	for field := range consumer {
		if _, ok := producer[field]; !ok {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}
