package main

import (
	"strings"
	"testing"
)

func TestValidateManifestActionsAcceptsProductionSchemas(t *testing.T) {
	for _, manifest := range newLSPToolManifests() {
		t.Run(manifest.Name, func(t *testing.T) {
			if err := validateManifestActions(manifest); err != nil {
				t.Fatalf("validateManifestActions(%q) error = %v", manifest.Name, err)
			}
		})
	}
}

func TestValidateManifestActionsRejectsMalformedTypedShape(t *testing.T) {
	validCondition := manifestActionValidationCondition("one")
	tests := []struct {
		name   string
		schema schema
		want   string
	}{
		{
			name:   "conditions without properties",
			schema: schema{"allOf": []any{}},
			want:   "has action conditions but no properties",
		},
		{
			name:   "schema has unknown field",
			schema: schema{"unknown": true},
			want:   "unknown field",
		},
		{
			name:   "conditions without action property",
			schema: schema{"properties": map[string]any{}, "allOf": []any{}},
			want:   "has action conditions but no action property",
		},
		{
			name:   "action property has scalar type",
			schema: manifestActionValidationSchema("invalid", nil),
			want:   "action schema has invalid type",
		},
		{
			name:   "action property has unknown field",
			schema: manifestActionValidationSchema(map[string]any{"enum": []string{"one"}, "unknown": true}, nil),
			want:   "unknown field",
		},
		{
			name:   "action enum is missing",
			schema: manifestActionValidationSchema(map[string]any{}, nil),
			want:   "enum has type <nil>",
		},
		{
			name:   "action enum has wrong type",
			schema: manifestActionValidationSchema(map[string]any{"enum": "one"}, nil),
			want:   "enum has type string",
		},
		{
			name:   "action enum is empty",
			schema: manifestActionValidationSchema(map[string]any{"enum": []string{}}, nil),
			want:   "enum is empty",
		},
		{
			name:   "action enum contains non-string",
			schema: manifestActionValidationSchema(map[string]any{"enum": []any{"one", 2}}, nil),
			want:   "enum value has type float64",
		},
		{
			name:   "allOf has wrong type",
			schema: manifestActionValidationSchema(map[string]any{"enum": []string{"one"}}, "invalid"),
			want:   "action conditions",
		},
		{
			name:   "condition is missing if",
			schema: manifestActionValidationSchema(map[string]any{"enum": []string{"one"}}, []any{map[string]any{}}),
			want:   "missing if block",
		},
		{
			name: "if block has wrong type",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": "invalid"}},
			),
			want: "missing if block",
		},
		{
			name: "if block is missing properties",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": map[string]any{}}},
			),
			want: "invalid properties",
		},
		{
			name: "condition properties have wrong type",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": map[string]any{"properties": "invalid"}}},
			),
			want: "invalid properties",
		},
		{
			name: "condition is missing action",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": map[string]any{"properties": map[string]any{}}}},
			),
			want: "missing action const",
		},
		{
			name: "condition action has wrong type",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": map[string]any{"properties": map[string]any{"action": "invalid"}}}},
			),
			want: "missing action const",
		},
		{
			name: "condition const is missing",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{}}}}},
			),
			want: "invalid const <nil>",
		},
		{
			name: "condition const has wrong type",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": 2}}}}},
			),
			want: "invalid const 2",
		},
		{
			name: "condition action has unknown field",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "one", "unknown": true}}}}},
			),
			want: "unknown field",
		},
		{
			name: "condition if has unknown field",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{"if": map[string]any{
					"properties": map[string]any{"action": map[string]any{"const": "one"}},
					"unknown":    true,
				}}},
			),
			want: "unknown field",
		},
		{
			name: "condition has unknown field",
			schema: manifestActionValidationSchema(
				map[string]any{"enum": []string{"one"}},
				[]any{map[string]any{
					"if":      map[string]any{"properties": map[string]any{"action": map[string]any{"const": "one"}}},
					"unknown": true,
				}},
			),
			want: "unknown field",
		},
		{
			name:   "condition action is not declared",
			schema: manifestActionValidationSchema(map[string]any{"enum": []string{"one"}}, []any{validCondition, manifestActionValidationCondition("two")}),
			want:   "missing from action enum",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateManifestActions(ToolManifest{Name: "probe", Schema: test.schema})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateManifestActions() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func manifestActionValidationSchema(action any, conditions any) schema {
	result := schema{
		"type":       "object",
		"properties": map[string]any{"action": action},
	}
	if conditions != nil {
		result["allOf"] = conditions
	}
	return result
}

func manifestActionValidationCondition(action string) map[string]any {
	return map[string]any{
		"if": map[string]any{
			"properties": map[string]any{
				"action": map[string]any{"const": action},
			},
		},
	}
}
