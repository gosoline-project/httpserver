package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MiddlewareConcurrencyTestSuite struct {
	suite.Suite
}

func TestMiddlewareConcurrencyTestSuite(t *testing.T) {
	suite.Run(t, new(MiddlewareConcurrencyTestSuite))
}

func (s *MiddlewareConcurrencyTestSuite) SetupTest() {
	gin.SetMode(gin.TestMode)
}

func (s *MiddlewareConcurrencyTestSuite) newConcurrentRequestLimitRouter(settings httpserver.ConcurrencySettings, entered chan struct{}, release <-chan struct{}) *gin.Engine {
	router := gin.New()
	var enteredOnce sync.Once

	router.Use(httpserver.ConcurrentRequestLimitMiddleware(settings))
	router.GET("/", func(c *gin.Context) {
		if entered != nil {
			enteredOnce.Do(func() {
				close(entered)
			})
		}

		if release != nil {
			<-release
		}

		c.Status(http.StatusNoContent)
	})

	return router
}

func (s *MiddlewareConcurrencyTestSuite) newRequest() *http.Request {
	req, err := http.NewRequest(http.MethodGet, "/", http.NoBody)
	s.Require().NoError(err)

	return req
}

func (s *MiddlewareConcurrencyTestSuite) TestZeroLimitDisablesEnforcement() {
	release := make(chan struct{})
	router := s.newConcurrentRequestLimitRouter(httpserver.ConcurrencySettings{}, nil, release)

	result := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			recorder := httptest.NewRecorder()
			req, err := http.NewRequest(http.MethodGet, "/", http.NoBody)
			require.NoError(s.T(), err)

			router.ServeHTTP(recorder, req)
			result <- recorder.Code
		}()
	}

	close(release)

	s.Equal(http.StatusNoContent, <-result)
	s.Equal(http.StatusNoContent, <-result)
}

func (s *MiddlewareConcurrencyTestSuite) TestRejectsWhenLimitReached() {
	entered := make(chan struct{})
	release := make(chan struct{})
	router := s.newConcurrentRequestLimitRouter(httpserver.ConcurrencySettings{
		MaxRequests:        1,
		OverloadStatusCode: http.StatusTooManyRequests,
		RetryAfter:         1500 * time.Millisecond,
	}, entered, release)

	firstResult := make(chan int)
	go func() {
		recorder := httptest.NewRecorder()
		req, err := http.NewRequest(http.MethodGet, "/", http.NoBody)
		require.NoError(s.T(), err)

		router.ServeHTTP(recorder, req)
		firstResult <- recorder.Code
	}()
	<-entered

	recorder := httptest.NewRecorder()
	req := s.newRequest()

	router.ServeHTTP(recorder, req)

	s.Equal(http.StatusTooManyRequests, recorder.Code)
	s.Equal("2", recorder.Header().Get(httpserver.HeaderRetryAfter))
	s.JSONEq(`{"error":"server overloaded"}`, recorder.Body.String())

	close(release)
	s.Equal(http.StatusNoContent, <-firstResult)
}

func (s *MiddlewareConcurrencyTestSuite) TestRejectsWithXMLAcceptFallsBackToJSON() {
	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.JSONRepresentation(),
		httpserver.XMLRepresentation(),
	)
	s.Require().NoError(err)

	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	router.Use(httpserver.ConcurrentRequestLimitMiddleware(httpserver.ConcurrencySettings{
		MaxRequests:        1,
		OverloadStatusCode: http.StatusTooManyRequests,
	}))
	router.GET("/", func(c *gin.Context) {
		enteredOnce.Do(func() {
			close(entered)
		})
		<-release
		c.Status(http.StatusNoContent)
	})

	firstResult := make(chan int)
	go func() {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, s.newRequest())
		firstResult <- recorder.Code
	}()
	<-entered

	recorder := httptest.NewRecorder()
	request := s.newRequest()
	request.Header.Set(httpserver.HeaderAccept, httpserver.ContentTypeApplicationXml)
	router.ServeHTTP(recorder, request)

	s.Equal(http.StatusTooManyRequests, recorder.Code)
	s.Equal(httpserver.ContentTypeJson, recorder.Header().Get(httpserver.HeaderContentType))
	s.Equal(httpserver.HeaderAccept, recorder.Header().Get(httpserver.HeaderVary))
	s.JSONEq(`{"error":"server overloaded"}`, recorder.Body.String())

	close(release)
	s.Equal(http.StatusNoContent, <-firstResult)
}

func (s *MiddlewareConcurrencyTestSuite) TestReleasesSlotAfterRequest() {
	router := s.newConcurrentRequestLimitRouter(httpserver.ConcurrencySettings{MaxRequests: 1}, nil, nil)

	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		req := s.newRequest()

		router.ServeHTTP(recorder, req)

		s.Equal(http.StatusNoContent, recorder.Code)
	}
}
