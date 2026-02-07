package httpserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/suite"
)

// BindSseTestSuite tests the SSE binding functions
func TestBindSseTestSuite(t *testing.T) {
	suite.Run(t, new(BindSseTestSuite))
}

type BindSseTestSuite struct {
	suite.Suite
}

func (s *BindSseTestSuite) SetupTest() {
	gin.SetMode(gin.TestMode)
}

// serveRequest is a helper to execute a request and return the recorder
func (s *BindSseTestSuite) serveRequest(router *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reqBody)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// serveRequestWithContext is a helper to execute a request with a custom context
func (s *BindSseTestSuite) serveRequestWithContext(router *gin.Engine, method, path string, ctx context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

type sseTestInput struct {
	Message string `json:"message"`
}

func (s *BindSseTestSuite) TestBindSse_Success() {
	router := gin.New()
	router.POST("/sse", httpserver.BindSse(func(ctx context.Context, input *sseTestInput, writer *httpserver.SseWriter) error {
		return writer.Send(input.Message)
	}))

	rec := s.serveRequest(router, http.MethodPost, "/sse", `{"message":"hello"}`, map[string]string{
		"Content-Type": "application/json",
	})

	s.Equal(http.StatusOK, rec.Code)
	s.Equal("text/event-stream", rec.Header().Get("Content-Type"))
	s.Equal("data: hello\n\n", rec.Body.String())
}

func (s *BindSseTestSuite) TestBindSse_HandlerError() {
	router := gin.New()
	router.POST("/sse", httpserver.BindSse(func(ctx context.Context, input *sseTestInput, writer *httpserver.SseWriter) error {
		// Send one successful event
		_ = writer.Send("before error")
		// Then return an error
		return errors.New("something went wrong")
	}))

	rec := s.serveRequest(router, http.MethodPost, "/sse", `{"message":"test"}`, map[string]string{
		"Content-Type": "application/json",
	})

	// SSE headers should be set
	s.Equal("text/event-stream", rec.Header().Get("Content-Type"))

	// Body should contain the successful event AND the error event
	body := rec.Body.String()
	s.Contains(body, "data: before error\n\n")
	s.Contains(body, "event: error\n")
	s.Contains(body, "data: something went wrong\n")
}

func (s *BindSseTestSuite) TestBindSse_BindingError() {
	router := gin.New()
	router.Use(httpserver.ErrorMiddleware())
	router.POST("/sse", httpserver.BindSse(func(ctx context.Context, input *sseTestInput, writer *httpserver.SseWriter) error {
		return writer.Send(input.Message)
	}))

	// Send invalid JSON
	rec := s.serveRequest(router, http.MethodPost, "/sse", `{"message":`, map[string]string{
		"Content-Type": "application/json",
	})

	// Should return normal JSON error response (headers not committed yet)
	s.Equal(http.StatusInternalServerError, rec.Code)
	s.Contains(rec.Body.String(), "bind error")
}

func (s *BindSseTestSuite) TestBindSse_ClientDisconnect() {
	router := gin.New()
	router.GET("/sse", httpserver.BindSseN(func(ctx context.Context, writer *httpserver.SseWriter) error {
		// Send one successful event
		err := writer.Send("event 1")
		if err != nil {
			return err
		}

		// Check if client is still connected
		if ctx.Err() != nil {
			// Return the disconnect error
			return httpserver.ErrClientDisconnected
		}

		// Send another event
		return writer.Send("event 2")
	}))

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before serving

	rec := s.serveRequestWithContext(router, http.MethodGet, "/sse", ctx)

	// Should have SSE headers
	s.Equal("text/event-stream", rec.Header().Get("Content-Type"))

	// With context already cancelled, first Send will fail with ErrClientDisconnected
	// and bind handler will exit cleanly without sending error event
	s.NotContains(rec.Body.String(), "event: error")
}

func (s *BindSseTestSuite) TestBindSseR_WithRequest() {
	router := gin.New()
	router.POST("/sse", httpserver.BindSseR(func(ctx context.Context, req *http.Request, input *sseTestInput, writer *httpserver.SseWriter) error {
		// Access request headers
		userAgent := req.Header.Get("User-Agent")
		return writer.SendEvent(httpserver.SseEvent{
			Event: "info",
			Data:  userAgent,
		})
	}))

	rec := s.serveRequest(router, http.MethodPost, "/sse", `{"message":"test"}`, map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   "TestClient/1.0",
	})

	s.Equal("text/event-stream", rec.Header().Get("Content-Type"))
	s.Contains(rec.Body.String(), "event: info\n")
	s.Contains(rec.Body.String(), "data: TestClient/1.0\n")
}

func (s *BindSseTestSuite) TestBindSseN_NoInput() {
	router := gin.New()
	router.GET("/events", httpserver.BindSseN(func(ctx context.Context, writer *httpserver.SseWriter) error {
		_ = writer.Send("event1")
		_ = writer.Send("event2")
		return nil
	}))

	rec := s.serveRequest(router, http.MethodGet, "/events", "", nil)

	s.Equal("text/event-stream", rec.Header().Get("Content-Type"))
	s.Equal("data: event1\n\ndata: event2\n\n", rec.Body.String())
}

func (s *BindSseTestSuite) TestBindSseNR_NoInputWithRequest() {
	router := gin.New()
	router.GET("/stream", httpserver.BindSseNR(func(ctx context.Context, req *http.Request, writer *httpserver.SseWriter) error {
		query := req.URL.Query().Get("filter")
		return writer.SendEvent(httpserver.SseEvent{
			Event: "filtered",
			Data:  query,
		})
	}))

	rec := s.serveRequest(router, http.MethodGet, "/stream?filter=active", "", nil)

	s.Equal("text/event-stream", rec.Header().Get("Content-Type"))
	s.Contains(rec.Body.String(), "event: filtered\n")
	s.Contains(rec.Body.String(), "data: active\n")
}

func (s *BindSseTestSuite) TestBindSse_StructuredEvents() {
	router := gin.New()
	router.GET("/events", httpserver.BindSseN(func(ctx context.Context, writer *httpserver.SseWriter) error {
		// Send various event types
		_ = writer.SendEvent(httpserver.SseEvent{
			Event: "start",
			Data:  "session started",
			Id:    "1",
		})

		_ = writer.SendEvent(httpserver.SseEvent{
			Event: "update",
			Data:  "processing",
			Id:    "2",
		})

		_ = writer.SendEvent(httpserver.SseEvent{
			Event: "complete",
			Data:  "finished",
			Id:    "3",
			Retry: 5000,
		})

		return nil
	}))

	rec := s.serveRequest(router, http.MethodGet, "/events", "", nil)

	body := rec.Body.String()
	s.Contains(body, "event: start\n")
	s.Contains(body, "id: 1\n")
	s.Contains(body, "event: update\n")
	s.Contains(body, "id: 2\n")
	s.Contains(body, "event: complete\n")
	s.Contains(body, "id: 3\n")
	s.Contains(body, "retry: 5000\n")
}

