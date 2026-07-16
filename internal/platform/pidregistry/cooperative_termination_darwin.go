//go:build darwin

package pidregistry

import (
	"bufio"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// CooperativeTerminationServer 管理 candidate 的认证退出监听与清理生命周期。
type CooperativeTerminationServer struct {
	listener *net.UnixListener
	endpoint string
	once     sync.Once
	done     chan struct{}
}

// StartCooperativeTerminationServer 启动 0600 Unix socket，并仅接受 token 认证的退出请求。
func StartCooperativeTerminationServer(endpoint, token string, terminate func()) (*CooperativeTerminationServer, error) {
	if endpoint == "" || token == "" || terminate == nil {
		return nil, errors.New("pidregistry: cooperative termination endpoint, token, and callback are required")
	}
	if _, err := os.Lstat(endpoint); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("pidregistry: cooperative termination endpoint already exists")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("pidregistry: listen cooperative termination endpoint: %w", err)
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(endpoint)
		return nil, err
	}
	server := &CooperativeTerminationServer{listener: listener, endpoint: endpoint, done: make(chan struct{})}
	safego.Go(context.Background(), nil, "pidregistry.cooperative-termination", func(context.Context) {
		server.serve(token, terminate)
	})
	return server, nil
}

func (server *CooperativeTerminationServer) serve(token string, terminate func()) {
	defer close(server.done)
	for {
		connection, err := server.listener.AcceptUnix()
		if err != nil {
			return
		}
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
		line, readErr := bufio.NewReader(connection).ReadString('\n')
		got := strings.TrimSuffix(line, "\n")
		if readErr == nil && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
			_, _ = connection.Write([]byte("ACK\n"))
			_ = connection.Close()
			terminate()
			return
		}
		_ = connection.Close()
	}
}

// Close 关闭协作终止监听并删除 endpoint。
func (server *CooperativeTerminationServer) Close() error {
	if server == nil {
		return nil
	}
	var err error
	server.once.Do(func() {
		err = server.listener.Close()
		<-server.done
		err = errors.Join(err, os.Remove(server.endpoint))
	})
	return err
}

// requestCooperativeTermination 发送认证 token，并要求 receiver 返回 ACK。
func requestCooperativeTermination(ctx context.Context, endpoint, token string) error {
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return fmt.Errorf("pidregistry: dial cooperative termination endpoint: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := connection.Write([]byte(token + "\n")); err != nil {
		return err
	}
	response, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		return err
	}
	if response != "ACK\n" {
		return errors.New("pidregistry: cooperative termination ACK mismatch")
	}
	return nil
}
