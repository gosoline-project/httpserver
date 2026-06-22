package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
)

func RunRouteHandleTest(ctx context.Context, setup func(router *gin.Engine) *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	request := setup(router)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)

	return w
}
