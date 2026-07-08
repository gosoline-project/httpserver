package httpserver

import (
	"bufio"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/log"
)

// ChaosSettings configures the chaos middleware that randomly introduces failures.
type ChaosSettings struct {
	Enabled      bool                      `cfg:"enabled" default:"false"`
	Reject       ChaosRejectSettings       `cfg:"reject"`
	Delay        ChaosDelaySettings        `cfg:"delay"`
	Drop         ChaosDropSettings         `cfg:"drop"`
	SlowResponse ChaosSlowResponseSettings `cfg:"slow_response"`
	Truncate     ChaosTruncateSettings     `cfg:"truncate"`
}

// ChaosRejectSettings controls random request rejection with an HTTP error.
type ChaosRejectSettings struct {
	// Percent is the probability (0-100) that a request is rejected.
	Percent int `cfg:"percent" default:"3" validate:"min=0,max=100"`
	// StatusCodes is the set of HTTP status codes to respond with on rejection.
	// Defaults to [499, 500, 502, 503, 504] if empty.
	StatusCodes []int `cfg:"status_codes"`
}

// ChaosDelaySettings controls random request delays before processing.
type ChaosDelaySettings struct {
	// Percent is the probability (0-100) that a request is delayed.
	Percent int `cfg:"percent" default:"3" validate:"min=0,max=100"`
	// MinDuration is the minimum random delay applied before processing.
	MinDuration time.Duration `cfg:"min_duration" default:"0" validate:"min=0"`
	// MaxDuration is the maximum random delay applied before processing.
	MaxDuration time.Duration `cfg:"max_duration" default:"60s" validate:"min=1"`
}

// ChaosDropSettings controls random connection drops without sending any response.
// The client sees "connection reset by peer" or "EOF" — simulating an OOM-killed pod
// or network partition.
type ChaosDropSettings struct {
	// Percent is the probability (0-100) that a connection is dropped.
	Percent int `cfg:"percent" default:"3" validate:"min=0,max=100"`
}

// ChaosSlowResponseSettings controls trickle responses where bytes are sent very slowly.
// This tests client streaming timeout logic since bytes are arriving (defeating simple read timeouts)
// but the full response takes extremely long to complete.
type ChaosSlowResponseSettings struct {
	// Percent is the probability (0-100) that a response is throttled.
	Percent int `cfg:"percent" default:"3" validate:"min=0,max=100"`
	// Delay is the pause between each chunk written to the client.
	Delay time.Duration `cfg:"delay" default:"1s" validate:"min=1"`
	// ChunkSize is the number of bytes written per chunk.
	ChunkSize int `cfg:"chunk_size" default:"64" validate:"min=1"`
}

// ChaosTruncateSettings controls responses that send headers and partial body
// then abruptly close the connection. This simulates upstream crashes mid-response.
type ChaosTruncateSettings struct {
	// Percent is the probability (0-100) that a response is truncated.
	Percent int `cfg:"percent" default:"3" validate:"min=0,max=100"`
	// MaxBytes is the maximum number of body bytes sent before dropping the connection.
	// A random amount up to this value is written.
	MaxBytes int `cfg:"max_bytes" default:"512" validate:"min=1"`
}

type chaosMiddleware struct {
	logger   log.Logger
	settings ChaosSettings
}

func ChaosMiddleware(ctx context.Context, logger log.Logger, settings ChaosSettings) gin.HandlerFunc {
	if !settings.Enabled {
		return func(c *gin.Context) { c.Next() }
	}

	if len(settings.Reject.StatusCodes) == 0 {
		settings.Reject.StatusCodes = []int{
			499,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout,
		}
	}

	logger.Info(ctx, "chaos middleware enabled")

	return chaosMiddleware{
		logger:   logger.WithChannel("chaos-middleware"),
		settings: settings,
	}.Handle
}

func (m chaosMiddleware) Handle(c *gin.Context) {
	// Drop: hijack and close the connection immediately.
	if rollPercent(m.settings.Drop.Percent) {
		m.logger.Info(c.Request.Context(), "dropping request")
		m.dropConnection(c)
		c.Next()

		return
	}

	// Delay: sleep before processing.
	if rollPercent(m.settings.Delay.Percent) {
		jitter := m.settings.Delay.MaxDuration - m.settings.Delay.MinDuration
		delay := m.settings.Delay.MinDuration
		if jitter > 0 {
			delay += time.Duration(rand.Int64N(int64(jitter)))
		}
		m.logger.Info(c.Request.Context(), "delaying request by %v", delay)

		sleep(c.Request.Context(), delay)
	}

	// Reject: respond with an error status immediately.
	if rollPercent(m.settings.Reject.Percent) {
		code := m.settings.Reject.StatusCodes[rand.IntN(len(m.settings.Reject.StatusCodes))]
		m.logger.Info(c.Request.Context(), "rejecting request with code %v", code)
		c.AbortWithStatus(code)

		return
	}

	// Slow response: wrap the writer to trickle bytes.
	if rollPercent(m.settings.SlowResponse.Percent) {
		m.logger.Info(c.Request.Context(), "slowing response")
		c.Writer = &slowWriter{
			ResponseWriter: c.Writer,
			ctx:            c.Request.Context(),
			delay:          m.settings.SlowResponse.Delay,
			chunkSize:      m.settings.SlowResponse.ChunkSize,
		}
	}

	// Truncate: wrap the writer to send partial body then drop.
	if rollPercent(m.settings.Truncate.Percent) {
		maxBytes := rand.IntN(m.settings.Truncate.MaxBytes) + 1
		m.logger.Info(c.Request.Context(), "truncating request to %d bytes", maxBytes)
		c.Writer = &truncateWriter{
			ResponseWriter: c.Writer,
			ginContext:     c,
			logger:         m.logger,
			maxBytes:       maxBytes,
		}
	}

	c.Next()
}

// dropConnection hijacks the TCP connection and closes it immediately.
func (m chaosMiddleware) dropConnection(c *gin.Context) {
	var conn net.Conn
	var err error

	// abort the request in any case, even if we can't hijack
	defer c.Abort()

	hijacker, ok := c.Writer.(http.Hijacker)
	if !ok {
		return
	}

	if conn, _, err = hijacker.Hijack(); err != nil {
		return
	}

	// Close immediately — client sees connection reset / EOF.
	if err := conn.Close(); err != nil {
		m.logger.Warn(c.Request.Context(), "chaos: failed to close connection on drop: %s", err)
	}
}

// slowWriter wraps a gin.ResponseWriter and injects delays between byte chunks.
type slowWriter struct {
	gin.ResponseWriter
	ctx       context.Context
	delay     time.Duration
	chunkSize int
}

var (
	_ gin.ResponseWriter = &slowWriter{}
	_ http.Hijacker      = &slowWriter{}
)

func (w *slowWriter) Write(data []byte) (int, error) {
	written := 0
	for written < len(data) {
		end := min(len(data), written+w.chunkSize)
		n, err := w.ResponseWriter.Write(data[written:end])
		written += n

		if err != nil {
			return written, err
		}

		// Flush after each chunk so bytes actually reach the client.
		w.Flush()

		if written < len(data) {
			sleep(w.ctx, w.delay)
		}
	}

	return written, nil
}

func (w *slowWriter) WriteString(s string) (int, error) {
	//nolint:gocritic // The linter wants us to use WriteString, but that would loop forever
	return w.Write([]byte(s))
}

func (w *slowWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}

	return nil, nil, fmt.Errorf("upstream writer does not support hijacking")
}

// truncateWriter sends headers and partial body then drops the connection.
type truncateWriter struct {
	gin.ResponseWriter
	logger       log.Logger
	ginContext   *gin.Context
	maxBytes     int
	bytesWritten int
	truncated    bool
}

var (
	_ gin.ResponseWriter = &truncateWriter{}
	_ http.Hijacker      = &truncateWriter{}
)

func (w *truncateWriter) Write(data []byte) (int, error) {
	if w.truncated {
		return 0, fmt.Errorf("connection closed")
	}

	remaining := w.maxBytes - w.bytesWritten
	if remaining <= 0 {
		w.truncateConnection()

		return 0, fmt.Errorf("connection closed")
	}

	toWrite := data
	if len(toWrite) > remaining {
		toWrite = toWrite[:remaining]
	}

	n, err := w.ResponseWriter.Write(toWrite)
	w.bytesWritten += n

	if err != nil {
		return n, err
	}

	// Flush so the partial bytes reach the client.
	w.Flush()

	// If we've hit the limit, drop the connection.
	if w.bytesWritten >= w.maxBytes {
		w.truncateConnection()
	}

	// Report all bytes as "written" to the caller to prevent gin from retrying.
	return len(data), nil
}

func (w *truncateWriter) WriteString(s string) (int, error) {
	//nolint:gocritic // The linter wants us to use WriteString, but that would loop forever
	return w.Write([]byte(s))
}

func (w *truncateWriter) truncateConnection() {
	var conn net.Conn
	var err error

	if w.truncated {
		return
	}

	w.truncated = true

	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return
	}

	if conn, _, err = hijacker.Hijack(); err != nil {
		return
	}

	if err := conn.Close(); err != nil {
		w.logger.Warn(w.ginContext.Request.Context(), "chaos: failed to close connection on truncate: %s", err)
	}
}

func (w *truncateWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}

	return nil, nil, fmt.Errorf("upstream writer does not support hijacking")
}

func sleep(ctx context.Context, delay time.Duration) {
	select {
	case <-time.After(delay):
	case <-ctx.Done():
	}
}

func rollPercent(percent int) bool {
	return rand.IntN(100) < percent
}
