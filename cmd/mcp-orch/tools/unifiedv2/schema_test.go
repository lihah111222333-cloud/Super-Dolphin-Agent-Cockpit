package unifiedv2

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/waitaggregate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	toolspkg "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
)

func TestUnifiedV2SchemasStayClosedAndTrackWireDTOs(t *testing.T) {
	tests := []struct {
		name       string
		definition toolspkg.ToolDefinition
		typ        reflect.Type
		required   []string
	}{
		{name: "send", definition: SendDefinition(), typ: reflect.TypeOf(SendInput{}), required: []string{"target", "message", "delivery", "idempotency_key"}},
		{name: "list", definition: ListDefinition(), typ: reflect.TypeOf(ListAgentsInput{}), required: []string{"view"}},
		{name: "wait", definition: WaitDefinition(), typ: reflect.TypeOf(WaitAgentsInput{}), required: []string{"targets", "condition"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.definition.InputSchema
			if _, err := json.Marshal(schema); err != nil {
				t.Fatalf("schema is not JSON serializable: %v", err)
			}
			assertSchemaObjectFields(t, schema, jsonFields(tc.typ))
			assertSchemaRequired(t, schema, tc.required)
		})
	}
}

func TestUnifiedV2DecodersRejectNestedUnknownFields(t *testing.T) {
	send := `{"target":{"kind":"grant","grant_ref":"grant://opaque","unknown":true},"message":{"kind":"content.single","text":"hello"},"delivery":{"kind":"new_turn","busy_policy":{"action":"fail"}},"idempotency_key":"key-1"}`
	if _, err := decodeSendInput([]byte(send)); err == nil {
		t.Fatal("decodeSendInput accepted an unknown nested field")
	}
	wait := `{"targets":[{"key":"h","grant_id":"g","grant_revision":1,"unknown":true}],"condition":"any","timeout_ms":1}`
	if _, err := decodeInput([]byte(wait)); err == nil {
		t.Fatal("decodeInput accepted an unknown nested field")
	}
}

func TestSendDefinitionNestedSchemaAndBounds(t *testing.T) {
	props := schemaProperties(t, SendDefinition().InputSchema)
	assertSchemaObjectFields(t, schemaAsObject(t, props["target"]), jsonFields(reflect.TypeOf(SendTarget{})))
	assertSchemaObjectFields(t, schemaAsObject(t, props["message"]), jsonFields(reflect.TypeOf(SendMessage{})))
	delivery := schemaAsObject(t, props["delivery"])
	assertSchemaObjectFields(t, delivery, jsonFields(reflect.TypeOf(SendDelivery{})))
	assertSchemaObjectFields(t, schemaAsObject(t, schemaProperties(t, delivery)["busy_policy"]), jsonFields(reflect.TypeOf(BusyPolicy{})))
	assertSchemaEnum(t, schemaProperties(t, schemaAsObject(t, props["target"]))["kind"], "grant")
	assertSchemaEnum(t, schemaProperties(t, schemaAsObject(t, props["message"]))["kind"], "content.single")
	assertSchemaEnum(t, schemaProperties(t, delivery)["kind"], "new_turn")
	assertSchemaEnum(t, schemaProperties(t, schemaAsObject(t, schemaProperties(t, delivery)["busy_policy"]))["action"], "fail")
	assertSchemaBounds(t, schemaProperties(t, schemaAsObject(t, props["message"]))["text"], 1, 65536)
	assertSchemaBounds(t, schemaProperties(t, schemaAsObject(t, props["target"]))["grant_ref"], 1, 512)
	assertSchemaBounds(t, props["idempotency_key"], 1, 512)
}

func TestListDefinitionRequiredAndLimitBounds(t *testing.T) {
	definition := ListDefinition()
	assertSchemaRequired(t, definition.InputSchema, []string{"view"})
	assertSchemaEnum(t, schemaProperties(t, definition.InputSchema)["view"], "route")
	assertSchemaBounds(t, schemaProperties(t, definition.InputSchema)["limit"], 0, maxRouteAgents)
}

func TestWaitDefinitionRequiredOneHotAndLimits(t *testing.T) {
	definition := WaitDefinition()
	input := definition.InputSchema
	if got, ok := input["oneOf"].([]any); !ok || len(got) != 2 {
		t.Fatalf("timeout/deadline oneOf = %#v, want two branches", input["oneOf"])
	}
	if got, ok := input["allOf"].([]any); !ok || len(got) != 3 {
		t.Fatalf("conditional allOf = %#v, want quorum/first-success/predicate guards", input["allOf"])
	}
	props := schemaProperties(t, input)
	targets := schemaAsArray(t, props["targets"])
	if got := targets["minItems"]; got != 1 {
		t.Fatalf("targets minItems = %#v, want 1", got)
	}
	if got := targets["maxItems"]; got != 32 {
		t.Fatalf("targets maxItems = %#v, want 32", got)
	}
	target := schemaAsObject(t, targets["items"])
	assertSchemaObjectFields(t, target, jsonFields(reflect.TypeOf(WaitTarget{})))
	assertSchemaRequired(t, target, []string{"key", "grant_id", "grant_revision", "selector"})
	assertSchemaBounds(t, schemaProperties(t, target)["grant_revision"], 1, -1)
	selector := schemaAsObject(t, schemaProperties(t, target)["selector"])
	assertSchemaObjectFields(t, selector, jsonFields(reflect.TypeOf(accesswait.EventSelector{})))
	eventTypes := schemaAsArray(t, schemaProperties(t, selector)["event_types"])
	if eventTypes["minItems"] != 1 || eventTypes["maxItems"] != 32 || eventTypes["uniqueItems"] != true {
		t.Fatalf("event_types bounds = %#v, want 1..32 unique", eventTypes)
	}
	assertSchemaBounds(t, props["timeout_ms"], waitaggregate.MinTimeoutMS, waitaggregate.MaxTimeoutMS)
	assertSchemaBounds(t, props["quorum_count"], 1, 32)
	assertSchemaObjectFields(t, schemaAsObject(t, props["predicate"]), jsonFields(reflect.TypeOf(waitaggregate.Predicate{})))
}

func jsonFields(typ reflect.Type) []string {
	fields := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

func schemaProperties(t *testing.T, schema toolspkg.Schema) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties type = %T, want map[string]any", schema["properties"])
	}
	return properties
}

func schemaAsObject(t *testing.T, raw any) toolspkg.Schema {
	t.Helper()
	object, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("nested schema type = %T, want map[string]any", raw)
	}
	return toolspkg.Schema(object)
}

func schemaAsArray(t *testing.T, raw any) map[string]any {
	t.Helper()
	array, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("array schema type = %T, want map[string]any", raw)
	}
	return array
}

func assertSchemaObjectFields(t *testing.T, schema toolspkg.Schema, want []string) {
	t.Helper()
	got := make([]string, 0, len(schemaProperties(t, schema)))
	for name := range schemaProperties(t, schema) {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema fields = %v, want %v", got, want)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
}

func assertSchemaRequired(t *testing.T, schema toolspkg.Schema, want []string) {
	t.Helper()
	got, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required type = %T, want []string", schema["required"])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required = %v, want %v", got, want)
	}
}

func assertSchemaBounds(t *testing.T, raw any, min, max int) {
	t.Helper()
	schema, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("scalar schema type = %T, want map[string]any", raw)
	}
	if min >= 0 && schema["minimum"] != min && schema["minLength"] != min {
		t.Fatalf("schema minimum/minLength = %#v/%#v, want %d", schema["minimum"], schema["minLength"], min)
	}
	if max >= 0 && schema["maximum"] != max && schema["maxLength"] != max {
		t.Fatalf("schema maximum/maxLength = %#v/%#v, want %d", schema["maximum"], schema["maxLength"], max)
	}
}

func assertSchemaEnum(t *testing.T, raw any, want string) {
	t.Helper()
	schema, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("enum schema type = %T, want map[string]any", raw)
	}
	values, ok := schema["enum"].([]string)
	if !ok || len(values) != 1 || values[0] != want {
		t.Fatalf("enum = %#v, want [%q]", schema["enum"], want)
	}
}
