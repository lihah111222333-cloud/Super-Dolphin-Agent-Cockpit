package prompt

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

type invalidationProbe struct {
	calls  int
	reason InvalidateReason
}

func (p *invalidationProbe) SectionName() string {
	return DynamicSectionMemoryContext
}

func (p *invalidationProbe) Resolve(context.Context, SectionContext) (*string, error) {
	return nil, nil
}

func (p *invalidationProbe) OnPromptInvalidate(reason InvalidateReason) {
	p.calls++
	p.reason = reason
}

func TestInvalidateSectionsNotifiesTargetedProviders(t *testing.T) {
	svc := NewService(&Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	probe := &invalidationProbe{}
	registerDynamicProviderForTest(t, svc, probe)

	invalidator, ok := svc.(SectionInvalidator)
	if !ok {
		t.Fatal("service does not implement SectionInvalidator")
	}

	generation := invalidator.InvalidateSections(InvalidateMemoryWrite, DynamicSectionMemoryContext, DynamicSectionMemoryContext)
	if generation == 0 {
		t.Fatalf("InvalidateSections() generation = %d, want > 0", generation)
	}
	if probe.calls != 1 {
		t.Fatalf("OnPromptInvalidate() calls = %d, want 1", probe.calls)
	}
	if probe.reason != InvalidateMemoryWrite {
		t.Fatalf("OnPromptInvalidate() reason = %q, want %q", probe.reason, InvalidateMemoryWrite)
	}
}
