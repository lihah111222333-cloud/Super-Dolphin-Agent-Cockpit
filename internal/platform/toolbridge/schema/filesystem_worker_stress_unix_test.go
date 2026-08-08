//go:build unix

package schema

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestShortLivedFilesystemWorkersAttachReliably(t *testing.T) {
	root := setFilesystemSnapshotRoot(t)
	const (
		iterations  = 64
		concurrency = 16
	)
	var workers errgroup.Group
	workers.SetLimit(concurrency)
	for index := range iterations {
		workers.Go(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), helperFixtureTimeout)
			defer cancel()
			request := filesystemWorkerRequest{Version: filesystemWorkerVersion, Operation: filesystemWorkerSweep}
			setFilesystemWorkerDeadline(ctx, &request)
			_, err := runFilesystemWorker(
				ctx,
				ctx,
				os.Args[0],
				func(path string) *exec.Cmd { return exec.Command(path) },
				nil,
				request,
				nil,
				0,
				nil,
			)
			if err != nil {
				return fmt.Errorf("short-lived filesystem worker %d failed: %w", index, err)
			}
			return nil
		})
	}
	if err := workers.Wait(); err != nil {
		t.Fatal(err)
	}
	if entries := filesystemSnapshotDirectoryNames(t, root); len(entries) != 0 {
		t.Fatalf("short-lived sweep workers left snapshots: %v", entries)
	}
}
