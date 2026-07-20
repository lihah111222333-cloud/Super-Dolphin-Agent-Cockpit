package localci

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestSchedulerClientTimeoutDiscardsConnection(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	client := &SchedulerClient{
		identity: daemonIdentity{key: "test-daemon-key"}, conn: clientConn, nowFunc: time.Now,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := client.call(ctx, schedulerMethodSnapshot, schedulerEmptyParams{}, &schedulerSnapshotResult{}); err == nil {
		t.Fatal("scheduler call unexpectedly succeeded")
	}
	client.mu.Lock()
	retained := client.conn != nil
	client.mu.Unlock()
	if retained {
		t.Fatal("timed out scheduler connection was retained")
	}
	if client.Available() {
		t.Fatal("timed out scheduler client remained available")
	}
}

func TestSchedulerClientTimeoutFailsClosedAndRejectsLateResponse(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	client := &SchedulerClient{
		identity: daemonIdentity{key: "test-daemon-key"}, conn: clientConn, nowFunc: time.Now,
	}
	received := make(chan schedulerWireRequest, 1)
	releaseResponse := make(chan struct{})
	var server errgroup.Group
	server.Go(func() error {
		payload, err := readSchedulerFrame(serverConn)
		if err != nil {
			return err
		}
		var request schedulerWireRequest
		if err := decodeStrictSchedulerJSON(payload, &request); err != nil {
			return err
		}
		received <- request
		<-releaseResponse
		return writeSchedulerFrame(serverConn, schedulerWireResponse{
			Version: schedulerProtocolVersion, RequestID: request.RequestID, Result: json.RawMessage(`{}`),
		})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := client.call(ctx, schedulerMethodSnapshot, schedulerEmptyParams{}, &schedulerSnapshotResult{}); err == nil {
		t.Fatal("scheduler call unexpectedly succeeded")
	}
	<-received
	if err := client.call(context.Background(), schedulerMethodSnapshot, schedulerEmptyParams{}, &schedulerSnapshotResult{}); !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("second call error=%v, want ErrSchedulerClosed", err)
	}
	close(releaseResponse)
	if err := server.Wait(); err == nil {
		t.Fatal("late scheduler response was accepted after client timeout")
	}
}
