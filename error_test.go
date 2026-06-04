package httpserver_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func marshalResponseBody(t *testing.T, resp httpserver.Response) string {
	t.Helper()

	body, err := resp.Body()
	require.NoError(t, err)

	return string(body)
}

func TestErrorHandlerJson_5xxReturnsGenericMessage(t *testing.T) {
	handler := httpserver.GetErrorHandler()
	resp := handler(http.StatusInternalServerError, fmt.Errorf("super secret internal detail"))

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode())
	assert.JSONEq(t, `{"err":"internal server error"}`, marshalResponseBody(t, resp))
}

func TestErrorHandlerJson_4xxExposesActualError(t *testing.T) {
	handler := httpserver.GetErrorHandler()
	resp := handler(http.StatusBadRequest, fmt.Errorf("validation failed"))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.JSONEq(t, `{"err":"validation failed"}`, marshalResponseBody(t, resp))
}
