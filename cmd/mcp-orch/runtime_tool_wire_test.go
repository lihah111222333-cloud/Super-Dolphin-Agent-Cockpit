package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	orchtools "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestRegistryToolProviderListToolsPreservesRealOutputSchemas(t *testing.T) {
	registry := newRegistry(newRegistryParamsForTest())
	defs, err := registry.List()
	if err != nil {
		t.Fatalf("Registry.List() error = %v", err)
	}
	got, err := (registryToolProvider{registry: registry}).ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(got) != len(defs) {
		t.Fatalf("ListTools() returned %d tools, want %d", len(got), len(defs))
	}
	for index, def := range defs {
		if got[index].Name != def.Name {
			t.Fatalf("ListTools()[%d].Name = %q, want %q", index, got[index].Name, def.Name)
		}
		want := expectedToolOutputSchema(t, def)
		assertWireJSONEqual(t, got[index].OutputSchema, want, "outputSchema for "+def.Name)
	}
}

func TestMapRegistryToolDefinitionPreservesOutputSchema(t *testing.T) {
	got, err := mapRegistryToolDefinition(toolDefinitionWireFixture())
	if err != nil {
		t.Fatalf("mapRegistryToolDefinition() error = %v", err)
	}
	assertWireJSONEqual(t, got.OutputSchema, json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`), "outputSchema")
}

func TestMapRegistryToolDefinitionOmitsEmptyOutputSchema(t *testing.T) {
	def := toolDefinitionWireFixture()
	def.Metadata.OutputSchema = nil
	got, err := mapRegistryToolDefinition(def)
	if err != nil {
		t.Fatalf("mapRegistryToolDefinition() error = %v", err)
	}
	if len(got.OutputSchema) != 0 {
		t.Fatalf("empty output schema = %s, want omitted", got.OutputSchema)
	}
}

func TestMapRegistryToolDefinitionOneHotWireFieldCoverage(t *testing.T) {
	projections, err := toolDefinitionWireProjections()
	if err != nil {
		t.Fatalf("toolDefinitionWireProjections() error = %v", err)
	}
	if len(projections) == 0 {
		t.Fatal("toolDefinitionWireProjections() returned no fields")
	}

	baseline := toolDefinitionWireFixture()
	baselineTool, err := mapRegistryToolDefinition(baseline)
	if err != nil {
		t.Fatalf("mapRegistryToolDefinition(baseline) error = %v", err)
	}
	baselineFields := marshalWireToolFields(t, baselineTool)
	for _, projection := range projections {
		projection := projection
		t.Run(projection.targetJSONName, func(t *testing.T) {
			def := toolDefinitionWireFixture()
			value := reflect.New(reflect.TypeFor[orchtools.ToolDefinition]()).Elem()
			value.Set(reflect.ValueOf(def))
			field := value.FieldByIndex(projection.sourceIndex)
			if err := setToolWireSentinel(field, projection.targetJSONName); err != nil {
				t.Fatalf("set %s sentinel: %v", projection.targetJSONName, err)
			}

			got, err := mapRegistryToolDefinition(value.Interface().(orchtools.ToolDefinition))
			if err != nil {
				t.Fatalf("mapRegistryToolDefinition(%s) error = %v", projection.targetJSONName, err)
			}
			gotFields := marshalWireToolFields(t, got)
			assertOneHotWireDelta(t, projection.targetJSONName, baselineFields, gotFields)
		})
	}
}

type toolDefinitionWireProjection struct {
	targetJSONName string
	sourceIndex    []int
}

func toolDefinitionWireProjections() ([]toolDefinitionWireProjection, error) {
	sourceType := reflect.TypeFor[orchtools.ToolDefinition]()
	targetType := reflect.TypeFor[mcpdto.MCPTool]()
	projections := make([]toolDefinitionWireProjection, 0, targetType.NumField())
	for index := 0; index < targetType.NumField(); index++ {
		field := targetType.Field(index)
		if field.PkgPath != "" {
			continue
		}
		jsonName, ok := toolWireJSONName(field)
		if !ok {
			continue
		}
		sourceIndex, ok := findToolWireSourceField(sourceType, field.Name, nil)
		if !ok {
			return nil, fmt.Errorf("target field %s has no producer path", field.Name)
		}
		projections = append(projections, toolDefinitionWireProjection{
			targetJSONName: jsonName,
			sourceIndex:    sourceIndex,
		})
	}
	return projections, nil
}

func toolWireJSONName(field reflect.StructField) (string, bool) {
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if name == "-" {
		return "", false
	}
	if name == "" {
		return field.Name, true
	}
	return name, true
}

func findToolWireSourceField(typ reflect.Type, targetName string, prefix []int) ([]int, bool) {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil, false
	}
	if sourceIndex, ok := findDirectToolWireSourceField(typ, targetName, prefix); ok {
		return sourceIndex, true
	}
	return findNestedToolWireSourceField(typ, targetName, prefix)
}

func findDirectToolWireSourceField(typ reflect.Type, targetName string, prefix []int) ([]int, bool) {
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !isToolWireSourceCandidate(field) {
			continue
		}
		if field.Name == targetName {
			return append(append([]int(nil), prefix...), index), true
		}
	}
	return nil, false
}

func findNestedToolWireSourceField(typ reflect.Type, targetName string, prefix []int) ([]int, bool) {
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !isToolWireSourceCandidate(field) {
			continue
		}
		nested, ok := findToolWireSourceField(field.Type, targetName, append(append([]int(nil), prefix...), index))
		if ok {
			return nested, true
		}
	}
	return nil, false
}

func isToolWireSourceCandidate(field reflect.StructField) bool {
	if field.PkgPath != "" {
		return false
	}
	_, ok := toolWireJSONName(field)
	return ok
}

func toolDefinitionWireFixture() orchtools.ToolDefinition {
	return orchtools.ToolDefinition{
		Name:        "wire-fixture",
		Description: "wire fixture description",
		InputSchema: orchtools.Schema{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
		Metadata: orchtools.ToolMetadata{
			OutputSchema: orchtools.Schema{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}},
		},
	}
}

func expectedToolOutputSchema(t *testing.T, def orchtools.ToolDefinition) json.RawMessage {
	t.Helper()
	if len(def.Metadata.OutputSchema) == 0 {
		return nil
	}
	raw, err := json.Marshal(map[string]any(def.Metadata.OutputSchema))
	if err != nil {
		t.Fatalf("marshal %s output schema: %v", def.Name, err)
	}
	return raw
}

func marshalWireToolFields(t *testing.T, tool mcpdto.MCPTool) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal MCP tool: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal MCP tool: %v", err)
	}
	return fields
}

func assertOneHotWireDelta(t *testing.T, target string, baseline, got map[string]json.RawMessage) {
	t.Helper()
	keys := make(map[string]struct{}, len(baseline)+len(got))
	for key := range baseline {
		keys[key] = struct{}{}
	}
	for key := range got {
		keys[key] = struct{}{}
	}
	for key := range keys {
		if key == target {
			continue
		}
		if !wireJSONRawEqual(baseline[key], got[key]) {
			t.Fatalf("producer sentinel for %q changed unrelated output field %q", target, key)
		}
	}
	if wireJSONRawEqual(baseline[target], got[target]) {
		t.Fatalf("producer sentinel for %q did not change output field", target)
	}
}

func wireJSONRawEqual(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return bytes.Equal(left, right)
	}
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return bytes.Equal(left, right)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func assertWireJSONEqual(t *testing.T, got, want json.RawMessage, field string) {
	t.Helper()
	if !wireJSONRawEqual(got, want) {
		t.Fatalf("%s = %s, want %s", field, got, want)
	}
}

func setToolWireSentinel(field reflect.Value, label string) error {
	if !field.CanSet() {
		return fmt.Errorf("field is not settable")
	}
	sentinel := "wire-mapper-sentinel-" + label
	switch field.Kind() {
	case reflect.String:
		field.SetString(sentinel)
	case reflect.Map:
		if field.Type() != reflect.TypeFor[orchtools.Schema]() {
			return fmt.Errorf("unsupported map type %s", field.Type())
		}
		field.Set(reflect.ValueOf(orchtools.Schema{"wire_sentinel": sentinel}))
	default:
		return fmt.Errorf("unsupported field kind %s", field.Kind())
	}
	return nil
}
