package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestRemoteProgressObserverUsesSideChannelWithoutChangingStdout 验证 stderr 旁路不污染 stdout。
func TestRemoteProgressObserverUsesSideChannelWithoutChangingStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	observer, err := newRemoteProgressObserver(&stderr)
	if err != nil {
		t.Fatalf("newRemoteProgressObserver() error = %v", err)
	}
	observer.ObserveRemoteCIProgress(remoteci.ProgressEvent{
		Phase:       remoteci.ProgressPhaseRun,
		State:       "updated",
		TotalShards: 3,
	})
	if stdout.Len() != 0 {
		t.Fatalf("stdout changed by progress observer: %q", stdout.String())
	}
	var event remoteci.ProgressEvent
	if err := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &event); err != nil {
		t.Fatalf("stderr progress event is not JSON: %v", err)
	}
	if event.Kind != "remote_ci_progress" || event.Phase != remoteci.ProgressPhaseRun || event.TotalShards != 3 {
		t.Fatalf("progress event = %#v", event)
	}
}

// TestRemoteProgressObserverRejectsMultipleSideChannels 验证 CLI 只接受一个旁路 writer。
func TestRemoteProgressObserverRejectsMultipleSideChannels(t *testing.T) {
	_, err := newRemoteProgressObserver(&bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("newRemoteProgressObserver() accepted multiple side channels")
	}
}
