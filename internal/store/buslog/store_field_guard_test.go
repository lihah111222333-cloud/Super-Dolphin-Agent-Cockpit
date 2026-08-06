package buslog

import (
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

var busLogListFieldRegistry = map[string]string{
	"ID": "ID", "Ts": "Ts", "Category": "Category", "Severity": "Severity",
	"Source": "Source", "ToolName": "ToolName", "Message": "Message",
	"Traceback": "Traceback", "Extra": "Extra", "HasTraceback": "HasTraceback",
	"HasExtra": "HasExtra",
}

var busLogDetailFieldRegistry = map[string]string{
	"ID": "ID", "Ts": "Ts", "Category": "Category", "Severity": "Severity",
	"Source": "Source", "ToolName": "ToolName", "Message": "Message",
	"Traceback": "Traceback", "Extra": "Extra", "HasTraceback": "HasTraceback",
	"HasExtra": "HasExtra",
}

func TestMapBusExceptionLogListFieldGuard(t *testing.T) {
	assertBusLogMapperFieldGuard(
		t,
		reflect.TypeFor[sqlc.ListBusExceptionLogsRow](),
		busLogListFieldRegistry,
		func(producer reflect.Value) BusExceptionLog {
			return mapBusExceptionLog(producer.Interface().(sqlc.ListBusExceptionLogsRow))
		},
	)
}

func TestMapBusExceptionLogDetailFieldGuard(t *testing.T) {
	assertBusLogMapperFieldGuard(
		t,
		reflect.TypeFor[sqlc.BusExceptionLog](),
		busLogDetailFieldRegistry,
		func(producer reflect.Value) BusExceptionLog {
			return mapBusExceptionLogDetail(producer.Interface().(sqlc.BusExceptionLog))
		},
	)
}

func assertBusLogMapperFieldGuard(
	t *testing.T,
	producerType reflect.Type,
	registry map[string]string,
	mapper func(reflect.Value) BusExceptionLog,
) {
	t.Helper()
	consumerType := reflect.TypeFor[BusExceptionLog]()
	assertBusLogFieldRegistry(t, producerType, consumerType, registry)
	for sourceName, targetName := range registry {
		t.Run(sourceName, func(t *testing.T) {
			producer := reflect.New(producerType).Elem()
			source := producer.FieldByName(sourceName)
			setBusLogGuardValue(t, source, sourceName)
			mapped := reflect.ValueOf(mapper(producer))
			baseline := reflect.ValueOf(mapper(reflect.New(producerType).Elem()))
			assertBusLogMappedValue(t, sourceName, source, mapped.FieldByName(targetName))
			assertOnlyBusLogTargetChanged(t, mapped, baseline, targetName)
		})
	}
}

func assertBusLogFieldRegistry(t *testing.T, producerType, consumerType reflect.Type, registry map[string]string) {
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

func setBusLogGuardValue(t *testing.T, field reflect.Value, name string) {
	t.Helper()
	switch field.Kind() {
	case reflect.Int64:
		field.SetInt(1700000000123)
	case reflect.String:
		field.SetString("guard-" + name)
	default:
		t.Fatalf("unsupported bus log producer field kind: %s", field.Kind())
	}
}

func assertBusLogMappedValue(t *testing.T, sourceName string, source, target reflect.Value) {
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
	case "HasTraceback", "HasExtra":
		if target.Bool() != (source.Int() != 0) {
			t.Fatalf("mapped %s = %v, want %v", sourceName, target.Bool(), source.Int() != 0)
		}
	default:
		if !reflect.DeepEqual(target.Interface(), source.Interface()) {
			t.Fatalf("mapped %s = %v, want %v", sourceName, target.Interface(), source.Interface())
		}
	}
}

func assertOnlyBusLogTargetChanged(t *testing.T, mapped, baseline reflect.Value, targetName string) {
	t.Helper()
	for i := 0; i < mapped.NumField(); i++ {
		field := mapped.Type().Field(i)
		if field.Name != targetName && !reflect.DeepEqual(mapped.Field(i).Interface(), baseline.Field(i).Interface()) {
			t.Errorf("mapper changed unrelated field %s while guarding %s", field.Name, targetName)
		}
	}
}
