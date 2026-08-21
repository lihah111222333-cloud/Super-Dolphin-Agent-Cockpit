package remoteciruntime

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestConfigJSONFieldRegistry(t *testing.T) {
	t.Parallel()
	for name, item := range map[string]struct {
		typeOf reflect.Type
		fields []string
	}{
		"config":   {typeOf: reflect.TypeFor[Config](), fields: []string{"aliyun_cli", "capacity", "credential_profile", "generation_one_provision", "oss", "region_id", "schema_version", "security_group_id", "vswitches", "worker_role_name"}},
		"oss":      {typeOf: reflect.TypeFor[OSSConfig](), fields: []string{"bucket", "endpoint", "internal_endpoint", "source_prefix"}},
		"capacity": {typeOf: reflect.TypeFor[CapacityConfig](), fields: []string{"resource_policy"}},
	} {
		t.Run(name, func(t *testing.T) {
			actual := make([]string, 0, item.typeOf.NumField())
			for field := range item.typeOf.Fields() {
				jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				actual = append(actual, jsonName)
			}
			sort.Strings(actual)
			if !reflect.DeepEqual(actual, item.fields) {
				t.Fatalf("dynamic producer fields = %v, registered consumers = %v", actual, item.fields)
			}
		})
	}
}
