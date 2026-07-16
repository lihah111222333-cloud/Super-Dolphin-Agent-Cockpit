//go:build windows

package localci

import (
	"context"
	"net"
	"os"
)

func openSchedulerTransportListener(string, int) (net.Listener, os.FileInfo, error) {
	return nil, nil, errSchedulerPlatformUnsupported
}

func dialSchedulerTransport(context.Context, string, int) (net.Conn, error) {
	return nil, errSchedulerPlatformUnsupported
}

func schedulerTransportPeerUID(net.Conn) (int, error) {
	return 0, errSchedulerPlatformUnsupported
}

func removeSchedulerTransportSocket(string, os.FileInfo, int) error {
	return errSchedulerPlatformUnsupported
}
