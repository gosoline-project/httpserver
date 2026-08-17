package httpserver

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/funk"
)

// BindHandleResponse writes a Response to the Gin context, including status,
// headers, and body handling for methods or status codes that must not include a body.
func BindHandleResponse(response Response, ginCtx *gin.Context) error {
	var err error
	var statusCode int
	var header http.Header
	var body []byte

	statusCode = response.StatusCode()
	header = response.Header()
	bodyless := hasBodylessResponse(ginCtx.Request, statusCode)

	if !bodyless {
		if body, err = response.Body(); err != nil {
			return fmt.Errorf("body read error: %w", err)
		}
	}

	for key, values := range header {
		for _, value := range values {
			if strings.EqualFold(key, HeaderVary) {
				addVaryHeader(ginCtx.Writer.Header(), value)

				continue
			}

			ginCtx.Header(key, value)
		}
	}

	ginCtx.Status(statusCode)
	ginCtx.Writer.WriteHeaderNow()

	if bodyless {
		return nil
	}

	if _, err = ginCtx.Writer.Write(body); err != nil {
		return fmt.Errorf("body write error: %w", err)
	}

	return nil
}

func hasBodylessResponse(request *http.Request, statusCode int) bool {
	if request != nil && request.Method == http.MethodHead {
		return true
	}

	return statusCode >= 100 && statusCode < 200 || statusCode == http.StatusNoContent || statusCode == http.StatusNotModified
}

func addVaryHeader(header http.Header, value string) {
	var values []string
	seen := funk.NewSet[string]()

	for _, existing := range header.Values(HeaderVary) {
		for token := range strings.SplitSeq(existing, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}

			if token == "*" {
				header.Set(HeaderVary, "*")

				return
			}

			key := strings.ToLower(token)
			if seen.Contains(key) {
				continue
			}

			seen.Add(key)
			values = append(values, token)
		}
	}

	for token := range strings.SplitSeq(value, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "*" {
			header.Set(HeaderVary, "*")

			return
		}

		key := strings.ToLower(token)
		if seen.Contains(key) {
			continue
		}

		seen.Add(key)
		values = append(values, token)
	}

	if len(values) > 0 {
		header.Set(HeaderVary, strings.Join(values, ", "))
	}
}

type responseWithStatus struct {
	Response
	statusCode int
}

func (r responseWithStatus) StatusCode() int {
	return r.statusCode
}

func responseWithStatusCode(response Response, statusCode int) Response {
	return responseWithStatus{
		Response:   response,
		statusCode: statusCode,
	}
}

// responseFromOutput preserves explicit Response values as an escape hatch and
// delegates typed application results to the request's response negotiator.
func responseFromOutput[O any](ginCtx *gin.Context, output O) (Response, error) {
	var err error
	var response Response

	if response, ok := any(output).(Response); ok {
		return response, nil
	}

	if response, err = responseNegotiatorFromContext(ginCtx).Render(ginCtx.Request, output); err != nil {
		return nil, err
	}

	if statusCode, ok := any(output).(StatusCode); ok {
		return responseWithStatusCode(response, statusCode.StatusCode()), nil
	}

	return response, nil
}

func responseFromOutputWithStatus[O any](ginCtx *gin.Context, output O, statusCode int) (Response, error) {
	var err error
	var response Response

	if response, err = responseFromOutput(ginCtx, output); err != nil {
		return nil, err
	}

	if _, ok := any(output).(Response); ok {
		return response, nil
	}

	return responseWithStatusCode(response, statusCode), nil
}
