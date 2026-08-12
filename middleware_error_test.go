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

func (s *errorMiddlewareTestSuite) TestErrorResponseFallsBackToJSONWhenXMLCannotEncodeMap() {
	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.JSONRepresentation(),
		httpserver.XMLRepresentation(),
	)
	s.Require().NoError(err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/error", func(c *gin.Context) {
		require.NotNil(s.T(), c.Error(httpserver.NewErrorWithStatus(http.StatusBadRequest, errors.New("bad request detail"))))
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/error", http.NoBody)
	request.Header.Set(httpserver.HeaderAccept, httpserver.ContentTypeApplicationXml)
	router.ServeHTTP(recorder, request)

	s.Equal(http.StatusBadRequest, recorder.Code)
	s.Equal(httpserver.ContentTypeJson, recorder.Header().Get(httpserver.HeaderContentType))
	s.Equal(httpserver.HeaderAccept, recorder.Header().Get(httpserver.HeaderVary))
	s.JSONEq(`{"err":"bad request detail"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestCustomErrorHandlerResponsePreservesExplicitResponse() {
	defer httpserver.WithErrorHandler(nil)

	httpserver.WithErrorHandler(func(statusCode int, err error) httpserver.Response {
		s.Equal(http.StatusBadRequest, statusCode)
		s.Equal("bad request detail", err.Error())

		return httpserver.NewResponse(
			httpserver.WithBody([]byte("custom error")),
			httpserver.WithHeader(httpserver.HeaderContentType, httpserver.ContentTypeTextPlain),
			httpserver.WithHeader("X-Error-Source", "custom"),
			httpserver.WithStatusCode(http.StatusTeapot),
		)
	})

	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.JSONRepresentation(),
		httpserver.XMLRepresentation(),
	)
	s.Require().NoError(err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/error", func(c *gin.Context) {
		require.NotNil(s.T(), c.Error(httpserver.NewErrorWithStatus(http.StatusBadRequest, errors.New("bad request detail"))))
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/error", http.NoBody)
	request.Header.Set(httpserver.HeaderAccept, httpserver.ContentTypeApplicationXml)
	router.ServeHTTP(recorder, request)

	s.Equal(http.StatusTeapot, recorder.Code)
	s.Equal(httpserver.ContentTypeTextPlain, recorder.Header().Get(httpserver.HeaderContentType))
	s.Equal("custom", recorder.Header().Get("X-Error-Source"))
	s.Equal("custom error", recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestErrorResponseFallsBackToJSONWhenEncoderFails() {
	expectedError := errors.New("cannot encode error")
	negotiator, err := httpserver.NewContentNegotiator(
		"application/problem+json",
		httpserver.ResponseRepresentation{
			MediaType: "application/problem+json",
			Encode: func(any) ([]byte, error) {
				return nil, expectedError
			},
		},
	)
	s.Require().NoError(err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/error", func(c *gin.Context) {
		require.NotNil(s.T(), c.Error(httpserver.NewErrorWithStatus(http.StatusTeapot, errors.New("bad request detail"))))
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/error", http.NoBody)
	request.Header.Set(httpserver.HeaderAccept, "application/problem+json")
	router.ServeHTTP(recorder, request)

	s.Equal(http.StatusTeapot, recorder.Code)
	s.Equal(httpserver.ContentTypeJson, recorder.Header().Get(httpserver.HeaderContentType))
	s.Equal(httpserver.HeaderAccept, recorder.Header().Get(httpserver.HeaderVary))
	s.JSONEq(`{"err":"bad request detail"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestRegisteredErrorMapperReturnsForbidden() {
	deniedError := errors.New("permission denied")
	httpserver.RegisterErrorMapper(func(err error) (int, bool) {
		if errors.Is(err, deniedError) {
			return http.StatusForbidden, true
		}

		return 0, false
	})

	err := fmt.Errorf("handler failed: %w", deniedError)
	recorder := s.serveErrorMiddlewareRequest(err, httpserver.ErrorMiddleware())

	s.Equal(http.StatusForbidden, recorder.Code)
	s.JSONEq(`{"err":"handler failed: permission denied"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestValidationErrorReturnsBadRequest() {
	err := validation.NewError(errors.New("invalid input"))
	recorder := s.serveErrorMiddlewareRequest(err, httpserver.ErrorMiddleware())

	s.Equal(http.StatusBadRequest, recorder.Code)
	s.JSONEq(`{"err":"validation: invalid input"}`, recorder.Body.String())
}

func (s *errorMiddlewareTestSuite) TestWrappedValidationErrorReturnsBadRequestWithoutWrapperMessage() {
	err := fmt.Errorf("service context: %w", validation.NewError(errors.New("invalid input")))
	recorder := s.serveErrorMiddlewareRequest(err, httpserver.ErrorMiddleware())

	s.Equal(http.StatusBadRequest, recorder.Code)
	s.JSONEq(`{"err":"service context: validation: invalid input"}`, recorder.Body.String())
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
