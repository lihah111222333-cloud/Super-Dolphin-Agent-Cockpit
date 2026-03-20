package claudecli

import (
	"context"
	"fmt"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const capThreadConfigure = "thread_configure"

func (s *session) Configure(ctx context.Context, patch dto.ThreadConfigPatch) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if configurePatchEmpty(patch) {
		return nil
	}
	return fmt.Errorf(
		"claudecli: runtime Configure is not supported for active sessions: %w",
		dto.NewCapabilityError(configureCapability(patch), "claude"),
	)
}

func configurePatchEmpty(patch dto.ThreadConfigPatch) bool {
	return strings.TrimSpace(configureValue(patch.Model)) == "" &&
		strings.TrimSpace(configureValue(patch.Personality)) == "" &&
		strings.TrimSpace(configureValue(patch.Approvals)) == ""
}

func configureCapability(patch dto.ThreadConfigPatch) string {
	if strings.TrimSpace(configureValue(patch.Model)) != "" {
		return dto.CapModelSwitch
	}
	return capThreadConfigure
}

func configureValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
