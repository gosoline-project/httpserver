package httpserver_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/quic-go/quic-go/http3"
	"github.com/stretchr/testify/require"
)

func TestBindSseProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, protocol := range []string{"HTTP/1.1", "HTTP/2.0", "HTTP/3.0"} {
		for _, binding := range []string{"without input", "with input"} {
			t.Run(protocol+"/"+binding, func(t *testing.T) {
				testBindSseProtocol(t, protocol, binding)
			})
		}
	}
}

func testBindSseProtocol(t *testing.T, protocol, binding string) {
	t.Helper()

	release := make(chan struct{})
	canceled := make(chan struct{})
	handler := func(ctx context.Context, writer *httpserver.SseWriter) error {
		if err := writer.Send("first"); err != nil {
			return err
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := writer.Send("second"); err != nil {
			return err
		}
		<-ctx.Done()
		close(canceled)

		return nil
	}

	router := gin.New()
	if binding == "with input" {
		router.POST("/events", httpserver.BindSse(func(ctx context.Context, _ *struct{}, writer *httpserver.SseWriter) error {
			return handler(ctx, writer)
		}))
	} else {
		router.POST("/events", httpserver.BindSseN(handler))
	}

	client, url := newSseProtocolServer(t, protocol, router)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/events", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	require.Equal(t, protocol, resp.Proto)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	require.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	require.Empty(t, resp.Header.Get("Connection"))

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: first\n", line)
	line, err = reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "\n", line)
	close(release)
	line, err = reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: second\n", line)
	line, err = reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "\n", line)
	cancel()
	select {
	case <-canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not observe request cancellation")
	}
}

func newSseProtocolServer(t *testing.T, protocol string, handler http.Handler) (client *http.Client, url string) {
	t.Helper()

	server := httptest.NewUnstartedServer(handler)
	if protocol == "HTTP/1.1" {
		server.Start()
	} else {
		server.EnableHTTP2 = true
		server.StartTLS()
	}
	t.Cleanup(server.Close)
	client, url = server.Client(), server.URL
	if protocol == "HTTP/3.0" {
		packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
		require.NoError(t, err)
		quicServer := &http3.Server{Handler: handler, TLSConfig: &tls.Config{
			Certificates: server.TLS.Certificates,
			MinVersion:   tls.VersionTLS13,
		}}
		served := make(chan error, 1)
		go func() { served <- quicServer.Serve(packet) }()
		transport := &http3.Transport{TLSClientConfig: client.Transport.(*http.Transport).TLSClientConfig.Clone()}
		client = &http.Client{Transport: transport}
		url = "https://" + packet.LocalAddr().String()
		t.Cleanup(func() {
			require.NoError(t, transport.Close())
			require.NoError(t, quicServer.Close())
			require.NoError(t, packet.Close())
			err := <-served
			if !errors.Is(err, http.ErrServerClosed) {
				require.NoError(t, err)
			}
		})
	}

	return client, url
}

func TestBindSseHTTP1KeepAlive(t *testing.T) {
	router := gin.New()
	router.GET("/events", httpserver.BindSseN(func(_ context.Context, writer *httpserver.SseWriter) error {
		return writer.Send("event")
	}))
	server := httptest.NewServer(router)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := range 2 {
		var reused bool
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, server.URL+"/events", http.NoBody)
		require.NoError(t, err)
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, "HTTP/1.1", resp.Proto)
		require.Equal(t, "data: event\n\n", string(body))
		require.Empty(t, resp.Header.Get("Connection"))
		require.Equal(t, i == 1, reused)
	}
}
