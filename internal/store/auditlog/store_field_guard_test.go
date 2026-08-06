package auditlog

import (
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

var auditEventFieldRegistry = map[string]string{
	"ID": "ID", "Ts": "Ts", "EventType": "EventType", "Action": "Action",
	"Result": "Result", "Actor": "Actor", "Target": "Target", "Detail": "Detail",
	"Level": "Level", "Extra": "Extra",
}

func TestMapAuditEventFieldGuard(t *testing.T) {
	producerType := reflect.TypeFor[sqlc.AuditEvent]()
	consumerType := reflect.TypeFor[AuditEvent]()
	assertAuditFieldRegistry(t, producerType, consumerType, auditEventFieldRegistry)

	for sourceName, targetName := range auditEventFieldRegistry {
		t.Run(sourceName, func(t *testing.T) {
			producer := reflect.New(producerType).Elem()
			source := producer.FieldByName(sourceName)
			setAuditGuardValue(t, source, sourceName)
			mapped := reflect.ValueOf(mapAuditEvent(producer.Interface().(sqlc.AuditEvent)))
			baseline := reflect.ValueOf(mapAuditEvent(sqlc.AuditEvent{}))
			assertAuditMappedValue(t, sourceName, source, mapped.FieldByName(targetName))
			assertOnlyAuditTargetChanged(t, mapped, baseline, targetName)
		})
	}
}

func assertAuditFieldRegistry(t *testing.T, producerType, consumerType reflect.Type, registry map[string]string) {
	t.Helper()
	for field := range producerType.Fields() {
		name := field.Name
		if _, ok := registry[name]; !ok {
			t.Errorf("producer field %s is missing from registry", name)
		}
	}
	for sourceName, targetName := range registry {
		if _, ok := producerType.FieldByName(sourceName); !ok {
			t.Errorf("registry source %s does not exist", sourceName)
		}
		if _, ok := consumerType.FieldByName(targetName); !ok {
			t.Errorf("registry target %s does not exist", targetName)
		}
	}
	for field := range consumerType.Fields() {
		targetName := field.Name
		count := 0
		for _, registeredTarget := range registry {
			if registeredTarget == targetName {
				count++
			}
		}
		if count != 1 {
			t.Errorf("consumer field %s registry count = %d, want 1", targetName, count)
		}
	}
}

func setAuditGuardValue(t *testing.T, field reflect.Value, name string) {
	t.Helper()
	switch field.Kind() {
	case reflect.Int64:
		field.SetInt(1700000000123)
	case reflect.String:
		field.SetString("guard-" + name)
	default:
		t.Fatalf("unsupported audit producer field kind: %s", field.Kind())
	}
}

func assertAuditMappedValue(t *testing.T, sourceName string, source, target reflect.Value) {
	t.Helper()
	switch sourceName {
	case "Ts":
		if target.Interface().(interface{ UnixMilli() int64 }).UnixMilli() != source.Int() {
			t.Fatalf("mapped Ts = %v, want unix millis %d", target.Interface(), source.Int())
		}
	case "Extra":
		if string(target.Bytes()) != source.String() {
			t.Fatalf("mapped Extra = %q, want %q", string(target.Bytes()), source.String())
		}
	default:
		if !reflect.DeepEqual(target.Interface(), source.Interface()) {
			t.Fatalf("mapped %s = %v, want %v", sourceName, target.Interface(), source.Interface())
		}
	}
}

func assertOnlyAuditTargetChanged(t *testing.T, mapped, baseline reflect.Value, targetName string) {
	t.Helper()
	for i := 0; i < mapped.NumField(); i++ {
		field := mapped.Type().Field(i)
		if field.Name != targetName && !reflect.DeepEqual(mapped.Field(i).Interface(), baseline.Field(i).Interface()) {
			t.Errorf("mapper changed unrelated field %s while guarding %s", field.Name, targetName)
		}
	}
}
