package archtest

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestRegistryCoversAllIntFields 用反射扫描 FileMetrics 的全部 int 字段，
// 验证每个字段要么在 metricRules() 注册表中，要么在显式豁免清单中。
// 作用：代码守卫自己守卫自己 — 在 baseline.go 新增字段但忘了 metric_registry.go 时立即失败。
func TestRegistryCoversAllIntFields(t *testing.T) {
	// 显式豁免：这些字段因特殊语义不在 int 注册表中，由各消费点单独处理。
	exempt := map[string]string{
		// bool 类型字段不在 int 注册表中
	}

	// 构建注册表覆盖的字段地址集合
	var probe FileMetrics
	covered := make(map[uintptr]bool)
	for _, r := range metricRules() {
		addr := reflect.ValueOf(r.Access(&probe)).Pointer()
		covered[addr] = true
	}

	// 反射遍历所有嵌入子结构的 int 字段
	var missing []string
	probeVal := reflect.ValueOf(&probe).Elem()
	for i := 0; i < probeVal.NumField(); i++ {
		sub := probeVal.Field(i)
		if sub.Kind() != reflect.Struct {
			continue
		}
		subType := sub.Type()
		for j := 0; j < subType.NumField(); j++ {
			f := subType.Field(j)
			if f.Type.Kind() != reflect.Int {
				continue // 跳过 bool、map 等非 int 字段
			}
			if _, ok := exempt[f.Name]; ok {
				continue
			}
			addr := sub.Field(j).Addr().Pointer()
			if !covered[addr] {
				missing = append(missing, f.Name)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("baseline.go 中有 int 字段未在 metric_registry.go 注册且不在豁免清单中: %v\n"+
			"修复方法: 在 metric_registry.go 对应子注册表追加规则，或将字段加入本测试的 exempt map", missing)
	}
}

// TestRegistryFieldNamesMatchJSON 验证注册表中的 Field 名称与 JSON tag 一致。
// 防止 Field 名称拼写错误导致 ratchet 报错信息与 baseline JSON 不对应。
func TestRegistryFieldNamesMatchJSON(t *testing.T) {
	jsonTags := collectFileMetricsJSONTags()
	for _, r := range metricRules() {
		if !jsonTags[r.Field] {
			t.Errorf("registry 字段 %q 在 FileMetrics JSON tag 中不存在", r.Field)
		}
	}
}

// collectFileMetricsJSONTags 用反射收集 FileMetrics 所有嵌入 struct 字段的 JSON tag 名称。
func collectFileMetricsJSONTags() map[string]bool {
	tags := make(map[string]bool)
	probeVal := reflect.ValueOf(FileMetrics{})
	for i := 0; i < probeVal.NumField(); i++ {
		sub := probeVal.Field(i)
		if sub.Kind() != reflect.Struct {
			continue
		}
		collectStructJSONTags(sub.Type(), tags)
	}
	return tags
}

func collectStructJSONTags(st reflect.Type, out map[string]bool) {
	for j := 0; j < st.NumField(); j++ {
		tag := st.Field(j).Tag.Get("json")
		if tag == "" {
			continue
		}
		// 去除 ,omitempty 等选项
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		out[tag] = true
	}
}

// TestRegistryRuleFlags 验证注册表规则的 Flags 设置合理性。
func TestRegistryRuleFlags(t *testing.T) {
	for _, r := range metricRules() {
		if r.Flags == 0 {
			t.Errorf("registry 字段 %q 的 Flags 为 0，至少应参与一个消费循环", r.Field)
		}
		if r.Kind == limitHard && r.HardLimit == nil {
			t.Errorf("registry 字段 %q 的 Kind 为 limitHard 但 HardLimit 未设置", r.Field)
		}
		if r.Kind != limitHard && r.HardLimit != nil {
			t.Errorf("registry 字段 %q 的 Kind 非 limitHard 但设置了 HardLimit", r.Field)
		}
	}
}

// TestRegistryNoDuplicateFields 验证注册表中没有重复注册的字段。
func TestRegistryNoDuplicateFields(t *testing.T) {
	seen := make(map[string]bool)
	for _, r := range metricRules() {
		if seen[r.Field] {
			t.Errorf("registry 重复注册字段: %q", r.Field)
		}
		seen[r.Field] = true
	}
}
