package timeline

import (
	"testing"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestReasoningDeltaHandlers_KeepRegistrationSamplersIsolated(t *testing.T) {
	const stream = "message"

	firstSampler := pkglogger.NewEverySampler(2)
	secondSampler := pkglogger.NewEverySampler(2)
	firstHandler := reasoningDeltaHandler(nil, nil, firstSampler)
	secondHandler := reasoningDeltaHandler(nil, nil, secondSampler)

	// A non-reasoning stream exercises sampling without depending on a Service.
	firstHandler(turndto.TurnOutputDelta{Stream: stream})
	secondHandler(turndto.TurnOutputDelta{Stream: stream})

	if !firstSampler.ShouldLog(stream) {
		t.Fatal("first registration sampler did not retain its independent counter")
	}
	if !secondSampler.ShouldLog(stream) {
		t.Fatal("second registration sampler did not retain its independent counter")
	}
}
