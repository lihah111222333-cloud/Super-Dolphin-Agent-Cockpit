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

	if got.Hash == "" {
		t.Fatal("Hash is empty")
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

func TestBuildPrefixShapeHashIgnoresPromptBodies(t *testing.T) {
	t.Parallel()

	first := BuildPrefixShape(
		"alpha",
		"dev-1",
		&contract.PromptAssemblyBoundary{CachedPrefix: "cached prefix A", UncachedTail: "uncached tail A"},
		[]ResolvedPromptSection{{Name: "identity", Region: PromptRegionStatic, Content: "secret body A"}},
		[]string{"grep"},
		"",
	)
	second := BuildPrefixShape(
		"bravo",
		"dev-2",
		&contract.PromptAssemblyBoundary{CachedPrefix: "cached prefix B", UncachedTail: "uncached tail B"},
		[]ResolvedPromptSection{{Name: "identity", Region: PromptRegionStatic, Content: "secret body B"}},
		[]string{"grep"},
		"",
	)
	if first.Hash != second.Hash {
		t.Fatalf("Hash changed after prompt body-only edits: first=%q second=%q", first.Hash, second.Hash)
	}
}

func TestBuildPrefixShapeChangesHashWhenShapeFactsChange(t *testing.T) {
	t.Parallel()

	first := BuildPrefixShape("base", "dev", nil, nil, []string{"grep"}, "")
	second := BuildPrefixShape("base", "dev", nil, nil, []string{"shell"}, "")
	if first.Hash == second.Hash {
		t.Fatalf("Hash did not change when shape facts changed: %q", first.Hash)
	}
	if first.CachedPrefixBytes != 0 || first.UncachedTailBytes != 0 {
		t.Fatalf("nil boundary byte counts = %d/%d", first.CachedPrefixBytes, first.UncachedTailBytes)
	}
}
