package acpnode

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestWireEnvelopeFieldRegistryMatchesProducer 将 JSON-RPC producer 字段绑定到所有校验消费端。
func TestWireEnvelopeFieldRegistryMatchesProducer(t *testing.T) {
	t.Parallel()

	assertWireFieldRegistry(t, reflect.TypeFor[Message](), map[string]string{
		"jsonrpc": "validateMessageEnvelope",
		"id":      "validateMessageEnvelope/decodeID",
		"method":  "validateMessageEnvelope",
		"params":  "validateMessageEnvelope/validateJSONContainer",
		"result":  "validateMessageEnvelope/validateJSONContainer",
		"error":   "validateMessageEnvelope/validateRPCError",
	})
	assertWireFieldRegistry(t, reflect.TypeFor[RPCError](), map[string]string{
		"code":    "validateRPCError",
		"message": "validateRPCError",
		"data":    "validateRPCError/validateJSONValue",
	})
}

func assertWireFieldRegistry(t *testing.T, producer reflect.Type, consumers map[string]string) {
	t.Helper()
	fields := producerJSONFields(t, producer)
	missing, stale := wireRegistryDrift(fields, consumers)
	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("%s field registry drifted: missing consumers=%v stale entries=%v", producer, missing, stale)
	}
}

func producerJSONFields(t *testing.T, producer reflect.Type) map[string]bool {
	t.Helper()
	fields := make(map[string]bool, producer.NumField())
	for field := range producer.Fields() {
		name := strings.TrimSpace(strings.SplitN(field.Tag.Get("json"), ",", 2)[0])
		if name == "" || name == "-" {
			t.Fatalf("%s.%s must declare a JSON field", producer, field.Name)
		}
		if fields[name] {
			t.Fatalf("%s duplicates JSON field %q", producer, name)
		}
		fields[name] = true
	}
	return fields
}

func wireRegistryDrift(fields map[string]bool, consumers map[string]string) ([]string, []string) {
	var missing, stale []string
	for field := range fields {
		if strings.TrimSpace(consumers[field]) == "" {
			missing = append(missing, field)
		}
	}
	for field := range consumers {
		if !fields[field] {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}
