package memory

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type service struct {
	cfg    *Config
	logger *slog.Logger
}

func NewService(cfg *Config, logger *slog.Logger) Service {
	if cfg == nil {
		cfg = &Config{}
	}
	return &service{cfg: cfg, logger: logger}
}

func (s *service) Config() Config {
	if s == nil || s.cfg == nil {
		return Config{}
	}
	return *s.cfg
}

func (s *service) RootDir() string {
	return strings.TrimSpace(s.Config().RootDir)
}

func (s *service) EnsureRoot(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	root := s.RootDir()
	if root == "" {
		return errors.New("memory root dir is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.Debug("memory root ready", "root_dir", filepath.Clean(root))
	}
	return nil
}
