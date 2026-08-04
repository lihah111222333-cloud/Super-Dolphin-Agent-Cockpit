//go:build darwin

package multilsp

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

func closeResourceCohortClientForTest(t *testing.T, current *client) error {
	t.Helper()
	if current == nil || current.transport == nil {
		return nil
	}
	err := current.Close()
	if !errors.Is(err, hiddenexec.ErrProcessTreeCleanupPending) {
		return err
	}
	if cleanupErr := killResourceCohortFixture(current.transport); cleanupErr != nil {
		return errors.Join(err, cleanupErr)
	}
	if cleanupErr := waitResourceCohortFixture(current.transport); cleanupErr != nil {
		return errors.Join(err, cleanupErr)
	}
	if retryErr := retryResourceCohortClose(current.transport); retryErr != nil {
		return errors.Join(err, retryErr)
	}
	if !processTreeReleaseComplete(current.transport) {
		return errors.Join(err, errors.New("Darwin resource cohort retry Close() did not complete owner release"))
	}
	return nil
}

func killResourceCohortFixture(tr *transport) error {
	if tr.cmd == nil || tr.cmd.Process == nil {
		return errors.New("Darwin resource cohort fixture process is unavailable")
	}
	if err := tr.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func waitResourceCohortFixture(tr *transport) error {
	select {
	case <-tr.done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("Darwin resource cohort fixture process did not exit after explicit test cleanup")
	}
}

func retryResourceCohortClose(tr *transport) error {
	firstErr := tr.Close()
	if firstErr == nil {
		return nil
	}
	secondErr := tr.Close()
	if secondErr == nil {
		return nil
	}
	return fmt.Errorf("Darwin resource cohort retry Close() errors = (%v, %v) after explicit fixture kill", firstErr, secondErr)
}
