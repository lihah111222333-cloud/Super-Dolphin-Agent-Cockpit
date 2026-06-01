package observability

import "testing"

func TestSamplerKeepsErrorsPanicsAndSlowLifecycleEvents(t *testing.T) {
	s := NewSampler()
	for _, event := range []TraceEvent{
		{Status: StatusError, Kind: "ui_state"},
		{Status: StatusPanic, Kind: "ui_state"},
		{Status: StatusSlow, Kind: "lifecycle"},
	} {
		decision := s.Decide(event)
		if !decision.Keep || decision.Summary != nil {
			t.Fatalf("Decide(%+v) = %+v, want keep without summary", event, decision)
		}
	}
}

func TestSamplerSamplesHighFrequencyUIStateAndEmitsBoundedSummary(t *testing.T) {
	s := NewSampler(SamplerConfig{HighFrequencyKeepEvery: 3})
	var kept, summaries int
	for range 7 {
		decision := s.Decide(TraceEvent{Status: StatusOK, Kind: "ui_state", Method: "ui/state"})
		if decision.Keep {
			kept++
		}
		if decision.Summary != nil {
			summaries++
			if decision.Summary.Status != StatusDroppedSummary {
				t.Fatalf("summary status = %q", decision.Summary.Status)
			}
			if decision.Summary.Metadata["dropped_count"] == nil {
				t.Fatalf("summary missing dropped_count: %#v", decision.Summary.Metadata)
			}
		}
	}
	if kept != 2 || summaries != 2 {
		t.Fatalf("kept=%d summaries=%d, want two sampled keeps with summaries", kept, summaries)
	}
}
