package httpserver_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type errorMiddlewareTestSuite struct {
	suite.Suite
}

func TestErrorMiddlewareTestSuite(t *testing.T) {
	suite.Run(t, new(errorMiddlewareTestSuite))
}

func (s *errorMiddlewareTestSuite) TestDefaultErrorReturnsGenericInternalServerError() {
	recorder := s.serveErrorMiddlewareRequest(errors.New("super secret internal detail"))

	s.Equal(http.StatusInternalServerError, recorder.Code)
	s.JSONEq(`{"err":"internal server error"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestStatusErrorReturnsStatusAndExposesError() {
	err := httpserver.NewErrorWithStatus(http.StatusBadRequest, errors.New("bad request detail"))
	recorder := s.serveErrorMiddlewareRequest(err)

	s.Equal(http.StatusBadRequest, recorder.Code)
	s.JSONEq(`{"err":"bad request detail"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) serveErrorMiddlewareRequest(err error) *httptest.ResponseRecorder {
	s.T().Helper()

	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/error", func(c *gin.Context) {
		require.NotNil(s.T(), c.Error(err))
	})

	recorder := httptest.NewRecorder()
	req, reqErr := http.NewRequest(http.MethodGet, "/error", http.NoBody)
	require.NoError(s.T(), reqErr)

	router.ServeHTTP(recorder, req)

	return recorder
}
