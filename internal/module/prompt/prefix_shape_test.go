package prompt

import (
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestBuildPrefixShapeUsesAssemblyFactsWithoutPromptContent(t *testing.T) {
	t.Parallel()

	boundary := &contract.PromptAssemblyBoundary{
		CachedPrefix: "cached prefix",
		UncachedTail: "uncached tail",
	}
	sections := []ResolvedPromptSection{
		{Name: "identity", Region: PromptRegionStatic, Content: "static body"},
		{Name: "volatile", Region: PromptRegionStatic, Volatile: true, Content: "volatile body"},
		{Name: "memory", Region: PromptRegionDynamic, Content: "dynamic body"},
		{Name: "  ", Region: PromptRegionStatic, Content: "ignored"},
	}
	got := BuildPrefixShape(
		"base prompt",
		"developer prompt",
		boundary,
		sections,
		[]string{"shell", "grep"},
		" compact ",
	)

	if got.Hash != "b193d14dfd7808ef100d7e1865771850347466e5c7f88593761f066bab04b867" {
		t.Fatalf("Hash = %q", got.Hash)
	}
	assertPrefixShapeNames(t, got)
	assertPrefixShapeSizes(t, got, boundary)
}

func assertPrefixShapeNames(t *testing.T, got contract.PrefixShape) {
	t.Helper()
	if !reflect.DeepEqual(got.StaticSectionNames, []string{"identity"}) {
		t.Fatalf("StaticSectionNames = %#v", got.StaticSectionNames)
	}
	if !reflect.DeepEqual(got.DynamicSectionNames, []string{"memory", "volatile"}) {
		t.Fatalf("DynamicSectionNames = %#v", got.DynamicSectionNames)
	}
	if !reflect.DeepEqual(got.SuppressedToolNames, []string{"grep", "shell"}) {
		t.Fatalf("SuppressedToolNames = %#v", got.SuppressedToolNames)
	}
	if reflect.DeepEqual(got.StaticSectionNames, []string{"static body"}) ||
		reflect.DeepEqual(got.DynamicSectionNames, []string{"dynamic body"}) {
		t.Fatalf("shape leaked prompt content: %#v", got)
	}
}

func assertPrefixShapeSizes(t *testing.T, got contract.PrefixShape, boundary *contract.PromptAssemblyBoundary) {
	t.Helper()
	if got.CachedPrefixBytes != len(boundary.CachedPrefix) || got.UncachedTailBytes != len(boundary.UncachedTail) {
		t.Fatalf("boundary byte counts = %d/%d", got.CachedPrefixBytes, got.UncachedTailBytes)
	}
	if got.DeveloperBytes != len("developer prompt") || got.ChurnReason != "compact" {
		t.Fatalf("developer bytes/churn reason = %d/%q", got.DeveloperBytes, got.ChurnReason)
	}
}

func TestBuildPrefixShapeChangesHashWhenShapeFactsChange(t *testing.T) {
	t.Parallel()

	first := BuildPrefixShape("base", "dev", nil, nil, []string{"grep"}, "")
	second := BuildPrefixShape("base changed", "dev", nil, nil, []string{"grep"}, "")
	if first.Hash == second.Hash {
		t.Fatalf("Hash did not change when base instructions changed: %q", first.Hash)
	}
	if first.CachedPrefixBytes != 0 || first.UncachedTailBytes != 0 {
		t.Fatalf("nil boundary byte counts = %d/%d", first.CachedPrefixBytes, first.UncachedTailBytes)
	}
}
