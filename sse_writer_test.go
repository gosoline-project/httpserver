package httpserver_test

import (
	"bufio"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/suite"
)

func TestSseWriterTestSuite(t *testing.T) {
	suite.Run(t, new(SseWriterTestSuite))
}

type SseWriterTestSuite struct {
	suite.Suite
	rec    *httptest.ResponseRecorder
	ctx    context.Context
	writer *httpserver.SseWriter
}

func (s *SseWriterTestSuite) SetupTest() {
	gin.SetMode(gin.TestMode)
	s.rec = httptest.NewRecorder()
	s.ctx = context.Background()
	s.writer = httpserver.NewSseWriter(s.ctx, s.rec)
}

func (s *SseWriterTestSuite) TearDownTest() {
	s.writer.Close()
}

func (s *SseWriterTestSuite) TestSend() {
	err := s.writer.Send("hello world")
	s.NoError(err)

	// Check headers
	s.Equal(httpserver.ContentTypeEventStream, s.rec.Header().Get(httpserver.HeaderContentType))
	s.Equal(httpserver.HeaderValueNoCache, s.rec.Header().Get(httpserver.HeaderCacheControl))
	s.Equal(httpserver.HeaderValueKeepAlive, s.rec.Header().Get(httpserver.HeaderConnection))

	// Check body
	expected := "data: hello world\n\n"
	s.Equal(expected, s.rec.Body.String())
}

func (s *SseWriterTestSuite) TestSendEvent_FullEvent() {
	err := s.writer.SendEvent(httpserver.SseEvent{
		Event: "update",
		Data:  "some data",
		Id:    "123",
		Retry: 5000,
	})
	s.NoError(err)

	expected := "event: update\nid: 123\nretry: 5000\ndata: some data\n\n"
	s.Equal(expected, s.rec.Body.String())
}

func (s *SseWriterTestSuite) TestSendEvent_DataOnly() {
	err := s.writer.SendEvent(httpserver.SseEvent{
		Data: "test",
	})
	s.NoError(err)

	expected := "data: test\n\n"
	s.Equal(expected, s.rec.Body.String())
}

func (s *SseWriterTestSuite) TestSendEvent_MultilineData() {
	err := s.writer.SendEvent(httpserver.SseEvent{
		Data: "line 1\nline 2\nline 3",
	})
	s.NoError(err)

	expected := "data: line 1\ndata: line 2\ndata: line 3\n\n"
	s.Equal(expected, s.rec.Body.String())
}

func (s *SseWriterTestSuite) TestSendEvent_EmptyData() {
	err := s.writer.SendEvent(httpserver.SseEvent{
		Event: "ping",
		Data:  "",
	})
	s.NoError(err)

	expected := "event: ping\ndata: \n\n"
	s.Equal(expected, s.rec.Body.String())
}

func (s *SseWriterTestSuite) TestSendEvent_OnlyEventType() {
	err := s.writer.SendEvent(httpserver.SseEvent{
		Event: "heartbeat",
	})
	s.NoError(err)

	// Empty data still needs a data: line
	expected := "event: heartbeat\ndata: \n\n"
	s.Equal(expected, s.rec.Body.String())
}

func (s *SseWriterTestSuite) TestSendEvent_ClientDisconnected() {
	// Create a new writer with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	writer := httpserver.NewSseWriter(ctx, rec)
	defer writer.Close()

	// Cancel the context to simulate client disconnect
	cancel()

	// Give a moment for the cancellation to propagate
	time.Sleep(10 * time.Millisecond)

	err := writer.Send("this should fail")
	s.Error(err)
	s.ErrorIs(err, httpserver.ErrClientDisconnected)
}

func (s *SseWriterTestSuite) TestMultipleEvents() {
	err := s.writer.Send("first")
	s.NoError(err)

	err = s.writer.SendEvent(httpserver.SseEvent{
		Event: "update",
		Data:  "second",
		Id:    "2",
	})
	s.NoError(err)

	err = s.writer.Send("third")
	s.NoError(err)

	expected := "data: first\n\nevent: update\nid: 2\ndata: second\n\ndata: third\n\n"
	s.Equal(expected, s.rec.Body.String())
}

func (s *SseWriterTestSuite) TestNoCorsHeaders() {
	// Verify CORS headers are NOT set
	s.Empty(s.rec.Header().Get(httpserver.HeaderAccessControlAllowOrigin))
	s.Empty(s.rec.Header().Get(httpserver.HeaderAccessControlExposeHeaders))
}

func (s *SseWriterTestSuite) TestGzipDetection() {
	// Create a new recorder with gzip headers already set
	rec := httptest.NewRecorder()
	rec.Header().Set(httpserver.HeaderContentEncoding, httpserver.HeaderValueGzip)
	rec.Header().Set(httpserver.HeaderVary, httpserver.HeaderAcceptEncoding)

	writer := httpserver.NewSseWriter(s.ctx, rec)
	defer writer.Close()

	// Verify gzip headers are removed
	s.Empty(rec.Header().Get(httpserver.HeaderContentEncoding))
	s.Empty(rec.Header().Get(httpserver.HeaderVary))
}

func (s *SseWriterTestSuite) TestBrowserEventSourceCompatibility() {
	// This test verifies the SSE format is compatible with browser EventSource API

	// Send a named event with ID for reconnection
	err := s.writer.SendEvent(httpserver.SseEvent{
		Event: "userJoined",
		Data:  `{"user":"alice"}`,
		Id:    "msg-001",
	})
	s.NoError(err)

	// Send a default message event
	err = s.writer.Send("Hello")
	s.NoError(err)

	// Send a retry directive
	err = s.writer.SendEvent(httpserver.SseEvent{
		Retry: 3000,
		Data:  "reconnect",
	})
	s.NoError(err)

	body := s.rec.Body.String()

	// Parse the SSE stream as a browser would
	scanner := bufio.NewScanner(strings.NewReader(body))
	events := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			events++
		}
	}

	// Should have 3 events
	s.Equal(3, events)

	// Verify format matches SSE spec
	s.Contains(body, "event: userJoined\n")
	s.Contains(body, "id: msg-001\n")
	s.Contains(body, "retry: 3000\n")
	s.Contains(body, "data: ")
}

func (s *SseWriterTestSuite) TestHeartbeatsEnabledByDefault() {
	rec := httptest.NewRecorder()
	writer := httpserver.NewSseWriter(s.ctx, rec)
	defer writer.Close()

	time.Sleep(6 * time.Second)

	s.Contains(rec.Body.String(), ": heartbeat\n\n")
}
