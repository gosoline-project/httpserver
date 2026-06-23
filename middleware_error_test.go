package httpserver_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/validation"
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
	recorder := s.serveErrorMiddlewareRequest(errors.New("super secret internal detail"), httpserver.ErrorMiddleware())

	s.Equal(http.StatusInternalServerError, recorder.Code)
	s.JSONEq(`{"err":"internal server error"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestPrivateErrorsPrivacyReturnsGenericInternalServerError() {
	recorder := s.serveErrorMiddlewareRequest(errors.New("super secret internal detail"), httpserver.ErrorMiddlewareWithSettings(httpserver.ErrorsSettings{
		Privacy: httpserver.ErrorPrivacyPrivate,
	}))

	s.Equal(http.StatusInternalServerError, recorder.Code)
	s.JSONEq(`{"err":"internal server error"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestPublicErrorsPrivacyReturnsDetailedInternalServerError() {
	recorder := s.serveErrorMiddlewareRequest(errors.New("super secret internal detail"), httpserver.ErrorMiddlewareWithSettings(httpserver.ErrorsSettings{
		Privacy: httpserver.ErrorPrivacyPublic,
	}))

	s.Equal(http.StatusInternalServerError, recorder.Code)
	s.JSONEq(`{"err":"super secret internal detail"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestStatusErrorReturnsStatusAndExposesError() {
	err := httpserver.NewErrorWithStatus(http.StatusBadRequest, errors.New("bad request detail"))
	recorder := s.serveErrorMiddlewareRequest(err, httpserver.ErrorMiddleware())

	s.Equal(http.StatusBadRequest, recorder.Code)
	s.JSONEq(`{"err":"bad request detail"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestValidationErrorReturnsBadRequest() {
	err := validation.NewError(errors.New("invalid input"))
	recorder := s.serveErrorMiddlewareRequest(err, httpserver.ErrorMiddleware())

	s.Equal(http.StatusBadRequest, recorder.Code)
	s.JSONEq(`{"err":"validation: invalid input"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestWrappedValidationErrorReturnsBadRequest() {
	err := fmt.Errorf("handler error: %w", validation.NewError(errors.New("invalid input")))
	recorder := s.serveErrorMiddlewareRequest(err, httpserver.ErrorMiddleware())

	s.Equal(http.StatusBadRequest, recorder.Code)
	s.JSONEq(`{"err":"handler error: validation: invalid input"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestStatusErrorTakesPrecedenceOverValidationError() {
	err := httpserver.NewErrorWithStatus(http.StatusTeapot, validation.NewError(errors.New("invalid input")))
	recorder := s.serveErrorMiddlewareRequest(err, httpserver.ErrorMiddleware())

	s.Equal(http.StatusTeapot, recorder.Code)
	s.JSONEq(`{"err":"validation: invalid input"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) serveErrorMiddlewareRequest(err error, middleware gin.HandlerFunc) *httptest.ResponseRecorder {
	s.T().Helper()

	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(middleware)
	router.GET("/error", func(c *gin.Context) {
		require.NotNil(s.T(), c.Error(err))
	})

	recorder := httptest.NewRecorder()
	req, reqErr := http.NewRequest(http.MethodGet, "/error", http.NoBody)
	require.NoError(s.T(), reqErr)

	router.ServeHTTP(recorder, req)

	return recorder
}
