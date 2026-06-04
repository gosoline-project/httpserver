package httpserver_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfilingServer_BindsToLoopbackAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	gin.SetMode(gin.TestMode)
	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))
	profiling := httpserver.NewProfilingWithInterfaces(logger, gin.New(), &httpserver.ProfilingSettings{
		Api: httpserver.ProfilingApiSettings{Port: port},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- profiling.Run(ctx)
	}()

	conn, err := waitForDial(fmt.Sprintf("127.0.0.1:%d", port))
	assert.NoError(t, err)
	if conn != nil {
		require.NoError(t, conn.Close())
	}

	cancel()
	select {
	case runErr := <-errCh:
		assert.NoError(t, runErr)
	case <-time.After(2 * time.Second):
		t.Fatal("profiling server did not shut down in time")
	}
}

func waitForDial(address string) (net.Conn, error) {
	var lastErr error
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			return conn, nil
		}

		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}

	return nil, lastErr
}
