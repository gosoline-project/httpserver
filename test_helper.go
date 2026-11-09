package httpserver

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-contrib/location"
	"github.com/gin-gonic/gin"
)

//go:generate env MOCKERY_INCLUDE_INTERFACE_REGEX=^NetListener${DOLLAR} mockery --config .mockery-select.yml
type NetListener interface {
	net.Listener
}

//go:generate env MOCKERY_INCLUDE_INTERFACE_REGEX=^NetConn${DOLLAR} mockery --config .mockery-select.yml
type NetConn interface {
	net.Conn
}

var (
	_ NetListener = nil
	_ NetConn     = nil
)

type HttpBody interface {
	string | []byte
}

func HttpTest[Body HttpBody](method, path, requestPath string, body Body, handler gin.HandlerFunc, requestOptions ...func(r *http.Request)) *httptest.ResponseRecorder {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(location.Default())
	r.Use(ErrorMiddleware())
	r.Handle(method, path, handler)

	var bodyReader io.Reader
	switch value := any(body).(type) {
	case string:
		bodyReader = strings.NewReader(value)
	case []byte:
		bodyReader = bytes.NewReader(value)
	}

	request := httptest.NewRequest(method, requestPath, bodyReader)
	for _, opt := range requestOptions {
		opt(request)
	}

	response := httptest.NewRecorder()

	r.ServeHTTP(response, request)

	return response
}
