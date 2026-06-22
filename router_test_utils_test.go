package httpserver_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/assert"
)

func TestRouterWithBindCases(t *testing.T) {
	h := &Handler{}

	cases := []struct {
		name         string
		setup        func(router *gin.Engine) *http.Request
		expectedBody string
	}{
		{
			name: "uri",
			setup: func(router *gin.Engine) *http.Request {
				router.POST("/json/:id", httpserver.Bind(h.HandleWithUri))

				return httptest.NewRequest(http.MethodPost, "/json/1", nil)
			},
			expectedBody: "hello 1",
		},
		{
			name: "json",
			setup: func(router *gin.Engine) *http.Request {
				router.POST("/json", httpserver.Bind(h.HandleWithJson))

				req := httptest.NewRequest(http.MethodPost, "/json", strings.NewReader(`{"name":"alice"}`))
				req.Header.Set(httpserver.HeaderContentType, httpserver.ContentTypeApplicationJson)

				return req
			},
			expectedBody: "hello alice",
		},
		{
			name: "mixed uri and json",
			setup: func(router *gin.Engine) *http.Request {
				router.POST("/mixed/:id", httpserver.Bind(h.HandleWithMixed))

				req := httptest.NewRequest(http.MethodPost, "/mixed/2", strings.NewReader(`{"name":"bob"}`))
				req.Header.Set(httpserver.HeaderContentType, httpserver.ContentTypeApplicationJson)

				return req
			},
			expectedBody: "hello 2 bob",
		},
		{
			name: "query",
			setup: func(router *gin.Engine) *http.Request {
				router.GET("/search", httpserver.Bind(h.HandleWithQuery))

				return httptest.NewRequest(http.MethodGet, "/search?search=golang&page=3", nil)
			},
			expectedBody: "search golang page 3",
		},
		{
			name: "header",
			setup: func(router *gin.Engine) *http.Request {
				router.GET("/header", httpserver.Bind(h.HandleWithHeader))

				req := httptest.NewRequest(http.MethodGet, "/header", nil)
				req.Header.Set(httpserver.HeaderAuthorization, "Bearer token")

				return req
			},
			expectedBody: "auth Bearer token",
		},
		{
			name: "form",
			setup: func(router *gin.Engine) *http.Request {
				router.POST("/form", httpserver.Bind(h.HandleWithForm))

				req := httptest.NewRequest(http.MethodPost, "/form", strings.NewReader("name=charlie&email=charlie%40example.com"))
				req.Header.Set(httpserver.HeaderContentType, httpserver.ContentTypeFormURLEncoded)

				return req
			},
			expectedBody: "form charlie charlie@example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := httpserver.RunRouteHandleTest(t.Context(), tc.setup)

			assert.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, tc.expectedBody, response.Body.String())
		})
	}
}

type HandlerInputUri struct {
	Id int `uri:"id"`
}

type HandlerInputJson struct {
	Name string `json:"name"`
}

type HandlerInputMixed struct {
	Id   int    `uri:"id"`
	Name string `json:"name"`
}

type HandlerInputQuery struct {
	Search string `form:"search"`
	Page   int    `form:"page"`
}

type HandlerInputHeader struct {
	Auth string `header:"Authorization"`
}

type HandlerInputForm struct {
	Name  string `form:"name"`
	Email string `form:"email"`
}

type Handler struct {
}

func (h *Handler) HandleWithUri(ctx context.Context, input *HandlerInputUri) (httpserver.Response, error) {
	return httpserver.NewTextResponse(fmt.Sprintf("hello %d", input.Id)), nil
}

func (h *Handler) HandleWithJson(ctx context.Context, input *HandlerInputJson) (httpserver.Response, error) {
	return httpserver.NewTextResponse(fmt.Sprintf("hello %s", input.Name)), nil
}

func (h *Handler) HandleWithMixed(ctx context.Context, input *HandlerInputMixed) (httpserver.Response, error) {
	return httpserver.NewTextResponse(fmt.Sprintf("hello %d %s", input.Id, input.Name)), nil
}

func (h *Handler) HandleWithQuery(ctx context.Context, input *HandlerInputQuery) (httpserver.Response, error) {
	return httpserver.NewTextResponse(fmt.Sprintf("search %s page %d", input.Search, input.Page)), nil
}

func (h *Handler) HandleWithHeader(ctx context.Context, input *HandlerInputHeader) (httpserver.Response, error) {
	return httpserver.NewTextResponse(fmt.Sprintf("auth %s", input.Auth)), nil
}

func (h *Handler) HandleWithForm(ctx context.Context, input *HandlerInputForm) (httpserver.Response, error) {
	return httpserver.NewTextResponse(fmt.Sprintf("form %s %s", input.Name, input.Email)), nil
}
