package httpserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/stretchr/testify/require"
)

func TestWebsocketRegistryClosesLateRawAttachment(t *testing.T) {
	registry := newWebsocketRegistry(log.NewLogger(), defaultWebsocketOptions())
	reservation, admitted := registry.reserve()
	require.True(t, admitted)
	registry.stopAdmission()
	server, peer := net.Pipe()
	t.Cleanup(func() { require.NoError(t, peer.Close()) })
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
	require.False(t, reservation.attachRaw(server))
	_, err := peer.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)
	reservation.release()
	reservation.release()
	_, admitted = registry.reserve()
	require.False(t, admitted)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, registry.shutdown(ctx))
}

func TestWebsocketShutdownSharesCloseDeadlineAcrossBlockedPeers(t *testing.T) {
	const peers = 4
	deadlines := make(chan time.Time, peers)
	started := make(chan struct{}, peers)
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, _ *WebsocketConn) error {
		started <- struct{}{}
		<-ctx.Done()

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router, func(server *HttpServer) {
		server.websockets.options.CloseTimeout = 100 * time.Millisecond
		server.listener = &websocketBlockedCloseListener{Listener: server.listener, deadlines: deadlines}
	})
	for range peers {
		client, _, err := websocket.DefaultDialer.DialContext(t.Context(), fmt.Sprintf("ws://127.0.0.1:%d/socket", server.port), nil)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("websocket callback did not start")
		}
	}

	require.NoError(t, server.stopWithin(t, time.Second))
	var shared time.Time
	for range peers {
		select {
		case deadline := <-deadlines:
			require.False(t, deadline.IsZero())
			if shared.IsZero() {
				shared = deadline
			}
			require.Equal(t, shared, deadline, "every blocked close must use one absolute batch deadline")
		default:
			t.Fatal("shutdown did not attempt every peer's close control")
		}
	}
}

type websocketBlockedCloseListener struct {
	net.Listener
	deadlines chan<- time.Time
}

func (l *websocketBlockedCloseListener) Accept() (net.Conn, error) {
	var conn net.Conn
	var err error
	if conn, err = l.Listener.Accept(); err != nil {
		return nil, fmt.Errorf("accept websocket deadline test connection: %w", err)
	}

	return &websocketBlockedCloseConn{Conn: conn, deadlines: l.deadlines, done: make(chan struct{})}, nil
}

type websocketBlockedCloseConn struct {
	net.Conn
	mu        sync.Mutex
	deadline  time.Time
	deadlines chan<- time.Time
	done      chan struct{}
	once      sync.Once
}

func (c *websocketBlockedCloseConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.deadline = deadline
	c.mu.Unlock()
	if err := c.Conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("set websocket deadline test write bound: %w", err)
	}

	return nil
}

func (c *websocketBlockedCloseConn) Write(data []byte) (int, error) {
	if len(data) == 0 || data[0] != 0x88 {
		var n int
		var err error
		if n, err = c.Conn.Write(data); err != nil {
			return n, fmt.Errorf("write websocket deadline test handshake: %w", err)
		}

		return n, nil
	}
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	c.deadlines <- deadline
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-timer.C:
		return 0, fmt.Errorf("blocked websocket close control: %w", os.ErrDeadlineExceeded)
	case <-c.done:
		return 0, fmt.Errorf("closed websocket close control: %w", net.ErrClosed)
	}
}

func (c *websocketBlockedCloseConn) Close() error {
	c.once.Do(func() { close(c.done) })
	if err := c.Conn.Close(); err != nil {
		return fmt.Errorf("close websocket deadline test socket: %w", err)
	}

	return nil
}
