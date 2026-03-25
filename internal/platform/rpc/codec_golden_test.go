package rpc

import (
	"testing"

	goldentest "github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestPayloadEncoderGoldenSamples(t *testing.T) {
	t.Parallel()

	encoder := PayloadEncoder{}
	goldentest.AssertJSON(t, goldentest.Case{
		BaseDir: "testdata/golden",
		Domain:  goldentest.DomainTransport,
		Name:    "payload_encoder_samples",
	}, map[string]any{
		"success": map[string]any{
			"payload": encoder.WrapSuccess(map[string]any{
				"agent_id": "agent-1",
				"state":    "running",
			}),
		},
		"error": map[string]any{
			"payload": encoder.WrapError(CodeInvalidState, "thread session is not available"),
		},
	})
}
