package gate

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// JSONFieldNames 从测试生产类型的 JSON tag 动态枚举字段真值。
func JSONFieldNames(producer reflect.Type) ([]string, error) {
	if producer == nil {
		return nil, errors.New("producer type is required")
	}
	for producer.Kind() == reflect.Pointer {
		producer = producer.Elem()
	}
	if producer.Kind() != reflect.Struct {
		return nil, fmt.Errorf("producer must be a struct, got %s", producer.Kind())
	}
	fields := make([]string, 0, producer.NumField())
	seen := make(map[string]struct{}, producer.NumField())
	for field := range producer.Fields() {
		tag, ok := field.Tag.Lookup("json")
		if !ok {
			return nil, fmt.Errorf("producer field %s has no json tag", field.Name)
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			return nil, fmt.Errorf("producer field %s has invalid json tag %q", field.Name, tag)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate producer json field %q", name)
		}
		seen[name] = struct{}{}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields, nil
}

// FieldCoverageDiff 计算测试消费登记缺失字段和已过期字段，并稳定排序输出。
func FieldCoverageDiff(producer, coverage []string) (missing, stale []string) {
	producerSet := stringSet(producer)
	coverageSet := stringSet(coverage)
	for field := range producerSet {
		if _, ok := coverageSet[field]; !ok {
			missing = append(missing, field)
		}
	}
	for field := range coverageSet {
		if _, ok := producerSet[field]; !ok {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
