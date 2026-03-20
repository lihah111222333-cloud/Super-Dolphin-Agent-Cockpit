package shared

import (
	"fmt"

	sharedDTO "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

func RequireNonEmpty(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s: %w", field, sharedDTO.ErrRequired)
	}
	return nil
}
