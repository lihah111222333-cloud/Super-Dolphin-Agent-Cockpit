package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSchemasAcceptsCanonicalSupportedContract(t *testing.T) {
	t.Parallel()
	schemas, err := loadSchemas(filepath.Join("..", "..", "internal", "dto", "turn", "schema"))
	if err != nil {
		t.Fatalf("load canonical schemas: %v", err)
	}
	if len(schemas) != 3 {
		t.Fatalf("canonical schema count = %d, want 3", len(schemas))
	}
}

func TestLoadSchemaRejectsUnsupportedKeywordsRecursively(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		keyword string
		mutate  func(map[string]any)
	}{
		{
			name:    "format in property",
			keyword: "format",
			mutate: func(schema map[string]any) {
				property(schema, "name")["format"] = "uuid"
			},
		},
		{
			name:    "minimum in nested array item",
			keyword: "minimum",
			mutate: func(schema map[string]any) {
				property(schema, "values")["items"].(map[string]any)["minimum"] = float64(1)
			},
		},
		{
			name:    "maximum in anyOf branch",
			keyword: "maximum",
			mutate: func(schema map[string]any) {
				schema["anyOf"].([]any)[0].(map[string]any)["maximum"] = float64(10)
			},
		},
		{
			name:    "oneOf at root",
			keyword: "oneOf",
			mutate: func(schema map[string]any) {
				schema["oneOf"] = []any{map[string]any{"required": []any{"name"}}}
			},
		},
		{
			name:    "local defs",
			keyword: "$defs",
			mutate: func(schema map[string]any) {
				schema["$defs"] = map[string]any{"identifier": map[string]any{"type": "string"}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := supportedTestSchema()
			test.mutate(schema)
			_, err := loadTestSchema(t, schema)
			if err == nil {
				t.Fatalf("generator accepted unsupported keyword %q", test.keyword)
			}
			if !strings.Contains(err.Error(), test.keyword) {
				t.Fatalf("error %q does not identify keyword %q", err, test.keyword)
			}
		})
	}
}

func TestLoadSchemaRejectsMalformedSupportedKeywords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "minLength beyond implemented subset",
			mutate: func(schema map[string]any) {
				property(schema, "name")["minLength"] = float64(2)
			},
			want: "only supports the exact value 1",
		},
		{
			name: "maxLength is not an integer",
			mutate: func(schema map[string]any) {
				property(schema, "name")["maxLength"] = 1.5
			},
			want: "maxLength must be a non-negative integer",
		},
		{
			name: "maxItems is negative",
			mutate: func(schema map[string]any) {
				property(schema, "values")["maxItems"] = float64(-1)
			},
			want: "maxItems must be a non-negative integer",
		},
		{
			name: "noncanonical pattern",
			mutate: func(schema map[string]any) {
				property(schema, "name")["pattern"] = "^turn-"
			},
			want: "only supports the canonical diagnostic ID pattern",
		},
		{
			name: "schema additional properties",
			mutate: func(schema map[string]any) {
				schema["additionalProperties"] = map[string]any{"type": "string"}
			},
			want: "must be boolean",
		},
		{
			name: "conditional outside allOf",
			mutate: func(schema map[string]any) {
				schema["if"] = map[string]any{"required": []any{"name"}}
				schema["then"] = map[string]any{"required": []any{"values"}}
			},
			want: "outside an allOf entry",
		},
		{
			name: "ref with sibling",
			mutate: func(schema map[string]any) {
				schema["properties"].(map[string]any)["external"] = map[string]any{
					"$ref": "OtherV1",
					"type": "object",
				}
			},
			want: "$ref with unsupported sibling",
		},
		{
			name: "unsupported type",
			mutate: func(schema map[string]any) {
				property(schema, "name")["type"] = "number"
			},
			want: "must be one of",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := supportedTestSchema()
			test.mutate(schema)
			_, err := loadTestSchema(t, schema)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadSchema() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLoadSchemaRejectsNonEquivalentRuntimeConstraints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "complex const parity",
			mutate: func(schema map[string]any) {
				property(schema, "name")["const"] = map[string]any{"value": "name"}
			},
			want: "const only supports JSON scalar",
		},
		{
			name: "complex enum parity",
			mutate: func(schema map[string]any) {
				property(schema, "name")["enum"] = []any{map[string]any{"value": "name"}}
			},
			want: "enum[0] only supports JSON scalar",
		},
		{
			name: "complex unique items parity",
			mutate: func(schema map[string]any) {
				property(schema, "values")["items"] = map[string]any{
					"type":       "object",
					"properties": map[string]any{"value": map[string]any{"type": "string"}},
				}
			},
			want: "uniqueItems only supports string or boolean items",
		},
		{
			name: "missing dialect",
			mutate: func(schema map[string]any) {
				delete(schema, "$schema")
			},
			want: "must declare $schema",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := supportedTestSchema()
			test.mutate(schema)
			_, err := loadTestSchema(t, schema)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadSchema() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLoadSchemaKeepsSupportedAnyOfAndNestedSchemas(t *testing.T) {
	t.Parallel()
	loaded, err := loadTestSchema(t, supportedTestSchema())
	if err != nil {
		t.Fatalf("load supported schema: %v", err)
	}
	if loaded.name != "MutationV1" {
		t.Fatalf("loaded schema name = %q, want MutationV1", loaded.name)
	}
}

func TestValidateSchemaReferencesRejectsUnknownGeneratedSchema(t *testing.T) {
	t.Parallel()
	err := validateSchemaReferences([]renderedSchema{
		{name: "KnownV1", references: []string{"MissingV1"}},
	})
	if err == nil || !strings.Contains(err.Error(), "MissingV1") {
		t.Fatalf("validateSchemaReferences() error = %v, want unknown reference", err)
	}
}

func supportedTestSchema() map[string]any {
	return map[string]any{
		"$schema":              canonicalSchemaDialect,
		"$id":                  "MutationV1",
		"title":                "MutationV1",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"name"},
		"properties": map[string]any{
			"name": map[string]any{
				"type":      "string",
				"minLength": float64(1),
			},
			"values": map[string]any{
				"type":        "array",
				"uniqueItems": true,
				"items":       map[string]any{"type": "string", "minLength": float64(1)},
			},
		},
		"anyOf": []any{
			map[string]any{"required": []any{"name"}},
			map[string]any{"not": map[string]any{"required": []any{"values"}}},
		},
		"allOf": []any{
			map[string]any{
				"if":   map[string]any{"properties": map[string]any{"name": map[string]any{"const": "blocked"}}},
				"then": map[string]any{"not": map[string]any{"required": []any{"values"}}},
			},
		},
	}
}

func property(schema map[string]any, name string) map[string]any {
	return schema["properties"].(map[string]any)[name].(map[string]any)
}

func loadTestSchema(t *testing.T, schema map[string]any) (renderedSchema, error) {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("encode test schema: %v", err)
	}
	path := filepath.Join(t.TempDir(), "mutation.v1.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write test schema: %v", err)
	}
	return loadSchema(path, filepath.Base(path), map[string]bool{})
}
