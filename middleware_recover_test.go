package httpserver_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

func TestRecoveryWithSentryCaseNil(t *testing.T) {
	gin.SetMode(gin.TestMode)

	loggerMock := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))

	r := gin.New()
	r.Use(httpserver.RecoveryWithSentry(loggerMock))

	var req *http.Request
	var err error
	httpRecorder := httptest.NewRecorder()

	req, err = http.NewRequest(http.MethodGet, "/some/route", http.NoBody)
	assert.NoError(t, err)

	assert.NotPanics(t, func() {
		r.ServeHTTP(httpRecorder, req)
	})
	loggerMock.AssertNumberOfCalls(t, "Warn", 0)
	loggerMock.AssertNumberOfCalls(t, "Error", 0)
}

func TestRecoveryWithSentryCaseError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	loggerMock := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))

	r := gin.New()
	r.Use(httpserver.RecoveryWithSentry(loggerMock))
	r.Use(func(_ *gin.Context) {
		err := http.ErrServerClosed
		panic(err)
	})

	var req *http.Request
	var err error
	httpRecorder := httptest.NewRecorder()

	req, err = http.NewRequest(http.MethodGet, "/some/route", http.NoBody)
	assert.NoError(t, err)

	assert.NotPanics(t, func() {
		r.ServeHTTP(httpRecorder, req)
	})
	loggerMock.AssertNumberOfCalls(t, "Warn", 0)
	loggerMock.AssertNumberOfCalls(t, "Error", 1)
}

func TestRecoveryWithSentryCaseResponseBodyWriterAndConnectionErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	loggerMock := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))

	r := gin.New()
	r.Use(httpserver.RecoveryWithSentry(loggerMock))
	r.Use(func(_ *gin.Context) {
		err := unix.EPIPE
		panic(err)
	})

	var req *http.Request
	var err error
	httpRecorder := httptest.NewRecorder()

	req, err = http.NewRequest(http.MethodGet, "/some/route", http.NoBody)
	assert.NoError(t, err)

	assert.NotPanics(t, func() {
		r.ServeHTTP(httpRecorder, req)
	})
	loggerMock.AssertNumberOfCalls(t, "Warn", 1)
	loggerMock.AssertNumberOfCalls(t, "Error", 0)
}

func TestRecoveryWithSentryCaseResponseBodyWriterErrorButNotConnectionError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	loggerMock := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))

	r := gin.New()
	r.Use(httpserver.RecoveryWithSentry(loggerMock))
	r.Use(func(_ *gin.Context) {
		err := errors.New("an error")
		panic(err)
	})

	var req *http.Request
	var err error
	httpRecorder := httptest.NewRecorder()

	req, err = http.NewRequest(http.MethodGet, "/some/route", http.NoBody)
	assert.NoError(t, err)

	assert.NotPanics(t, func() {
		r.ServeHTTP(httpRecorder, req)
	})
	loggerMock.AssertNumberOfCalls(t, "Warn", 0)
	loggerMock.AssertNumberOfCalls(t, "Error", 1)
}

func TestRecoveryWithSentryUsesNegotiatedRepresentation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.JSONRepresentation(),
		httpserver.XMLRepresentation(),
	)
	assert.NoError(t, err)

	loggerMock := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))
	r := gin.New()
	r.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	r.Use(httpserver.RecoveryWithSentry(loggerMock))
	r.GET("/panic", func(_ *gin.Context) {
		panic("something went wrong")
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", http.NoBody)
	request.Header.Set(httpserver.HeaderAccept, httpserver.ContentTypeApplicationXml)

	r.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeXml, recorder.Header().Get(httpserver.HeaderContentType))
	assert.Equal(t, `<error><err>something went wrong</err></error>`, recorder.Body.String())
}

func TestRecoveryWithSentryCaseString(t *testing.T) {
	gin.SetMode(gin.TestMode)

	loggerMock := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))

	r := gin.New()
	r.Use(httpserver.RecoveryWithSentry(loggerMock))
	r.Use(func(_ *gin.Context) {
		panic("Panic to test recovery")
	})

	var req *http.Request
	var err error
	httpRecorder := httptest.NewRecorder()

	req, err = http.NewRequest(http.MethodGet, "/some/route", http.NoBody)
	assert.NoError(t, err)

	assert.NotPanics(t, func() {
		r.ServeHTTP(httpRecorder, req)
	})
	loggerMock.AssertNumberOfCalls(t, "Warn", 0)
	loggerMock.AssertNumberOfCalls(t, "Error", 1)
}
