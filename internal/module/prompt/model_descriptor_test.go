package prompt

import "testing"

func TestLookupModelDescriptorUsesKnownCatalogEntry(t *testing.T) {
	descriptor := LookupModelDescriptor("gpt-5.4")
	if descriptor.MarketingName != "GPT-5.4" {
		t.Fatalf("MarketingName = %q, want GPT-5.4", descriptor.MarketingName)
	}
	if descriptor.MetadataText() != "GPT-5.4 (model ID: gpt-5.4)" {
		t.Fatalf("MetadataText() = %q", descriptor.MetadataText())
	}
}

func TestLookupModelDescriptorFallsBackToModelID(t *testing.T) {
	descriptor := LookupModelDescriptor("custom-model")
	if descriptor.MetadataText() != "model ID: custom-model" {
		t.Fatalf("MetadataText() = %q, want model ID fallback", descriptor.MetadataText())
	}
}
