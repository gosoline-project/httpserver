package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/justtrackio/gosoline/pkg/funk"
	"github.com/justtrackio/gosoline/pkg/refl"
)

// Bind adapts a typed handler into a Gin handler by binding request data into
// the input struct before calling the handler.
func Bind[I any](handler func(ctx context.Context, input *I) (Response, error), binders ...binding.Binding) gin.HandlerFunc {
	return BindR[I](func(ctx context.Context, _ *http.Request, input *I) (Response, error) {
		return handler(ctx, input)
	}, binders...)
}

// BindR adapts a typed handler like Bind, but also passes the raw HTTP request
// to the handler for direct access to headers, method, body metadata, and client data.
func BindR[I any](handler func(ctx context.Context, req *http.Request, input *I) (Response, error), binders ...binding.Binding) gin.HandlerFunc {
	tags := refl.GetTagNames(new(I))

	return func(ginCtx *gin.Context) {
		var err error
		var input *I
		var response Response

		if input, err = BindHandleRequest[I](ginCtx, tags, binders); err != nil {
			reportGinErrorWithType(ginCtx, NewErrorWithStatus(http.StatusBadRequest, err), gin.ErrorTypeBind)

			return
		}

		if response, err = handler(ginCtx, ginCtx.Request, input); err != nil {
			reportGinError(ginCtx, err)

			return
		}

		if err = BindHandleResponse(response, ginCtx); err != nil {
			reportGinError(ginCtx, fmt.Errorf("response error: %w", err))
		}
	}
}

// BindN adapts a typed handler that does not need request input binding.
func BindN(handler func(ctx context.Context) (Response, error)) gin.HandlerFunc {
	return BindNR(func(ctx context.Context, _ *http.Request) (Response, error) {
		return handler(ctx)
	})
}

// BindNR adapts a typed handler that does not need request input binding, but
// still needs access to the raw HTTP request.
func BindNR(handler func(ctx context.Context, req *http.Request) (Response, error)) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		var err error
		var response Response

		if response, err = handler(ginCtx, ginCtx.Request); err != nil {
			reportGinError(ginCtx, err)

			return
		}

		if err = BindHandleResponse(response, ginCtx); err != nil {
			reportGinError(ginCtx, fmt.Errorf("response error: %w", err))
		}
	}
}

// BindSse adapts a typed SSE handler into a Gin handler by binding request data
// into the input struct and providing an SSE writer.
func BindSse[I any](handler func(ctx context.Context, input *I, writer *SseWriter) error, binders ...binding.Binding) gin.HandlerFunc {
	return BindSseR[I](func(ctx context.Context, _ *http.Request, input *I, writer *SseWriter) error {
		return handler(ctx, input, writer)
	}, binders...)
}

// BindSseR adapts a typed SSE handler like BindSse, but also passes the raw
// HTTP request to the handler.
func BindSseR[I any](handler func(ctx context.Context, req *http.Request, input *I, writer *SseWriter) error, binders ...binding.Binding) gin.HandlerFunc {
	tags := refl.GetTagNames(new(I))

	return func(ginCtx *gin.Context) {
		var err error
		var input *I

		if input, err = BindHandleRequest[I](ginCtx, tags, binders); err != nil {
			reportGinErrorWithType(ginCtx, NewErrorWithStatus(http.StatusBadRequest, err), gin.ErrorTypeBind)

			return
		}

		writer := NewSseWriter(ginCtx.Request.Context(), ginCtx.Writer)
		defer writer.Close()

		if err = handler(ginCtx, ginCtx.Request, input, writer); err != nil {
			// If client disconnected, this is a clean exit - no error logging
			if errors.Is(err, ErrClientDisconnected) {
				return
			}

			// Send error as an SSE event instead of letting ErrorMiddleware corrupt the stream
			if sendErr := writer.SendEvent(SseEvent{Event: "error", Data: err.Error()}); sendErr != nil && !errors.Is(sendErr, ErrClientDisconnected) {
				reportGinError(ginCtx, fmt.Errorf("sse error event: %w", sendErr))
			}

			ginCtx.Abort()
		}
	}
}

// BindSseN adapts an SSE handler that does not need request input binding.
func BindSseN(handler func(ctx context.Context, writer *SseWriter) error) gin.HandlerFunc {
	return BindSseNR(func(ctx context.Context, _ *http.Request, writer *SseWriter) error {
		return handler(ctx, writer)
	})
}

// BindSseNR adapts an SSE handler that does not need request input binding, but
// still needs access to the raw HTTP request.
func BindSseNR(handler func(ctx context.Context, req *http.Request, writer *SseWriter) error) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		var err error

		writer := NewSseWriter(ginCtx.Request.Context(), ginCtx.Writer)
		defer writer.Close()

		if err = handler(ginCtx, ginCtx.Request, writer); err != nil {
			// If client disconnected, this is a clean exit - no error logging
			if errors.Is(err, ErrClientDisconnected) {
				return
			}

			// Send error as an SSE event instead of letting ErrorMiddleware corrupt the stream
			if sendErr := writer.SendEvent(SseEvent{Event: "error", Data: err.Error()}); sendErr != nil && !errors.Is(sendErr, ErrClientDisconnected) {
				reportGinError(ginCtx, fmt.Errorf("sse error event: %w", sendErr))
			}

			ginCtx.Abort()
		}
	}
}

// BindHandleRequest binds request data into a new input value using explicit
// binders or binders inferred from the request content type and input tags.
func BindHandleRequest[I any](ginCtx *gin.Context, tags []string, binders []binding.Binding) (*I, error) {
	in := new(I)

	if len(binders) == 0 {
		binders = getBinders(ginCtx, tags)
	}

	for _, binder := range binders {
		if err := ginCtx.ShouldBindWith(in, binder); err != nil {
			return nil, fmt.Errorf("%s: %w", binder.Name(), err)
		}
	}

	if funk.Contains(tags, "uri") {
		if err := ginCtx.ShouldBindUri(in); err != nil {
			return nil, fmt.Errorf("uri: %w", err)
		}
	}

	if err := modifyInput(ginCtx, in); err != nil {
		return nil, err
	}

	return in, nil
}

func getBinders(ginCtx *gin.Context, tags []string) []binding.Binding {
	binders := make([]binding.Binding, 0)

	if binder := getContentTypeBinder(ginCtx); binder != nil {
		binders = append(binders, binder)
	}

	binders = append(binders, getTagBinders(tags)...)

	return funk.Uniq(binders)
}

func getContentTypeBinder(ginCtx *gin.Context) binding.Binding {
	switch ginCtx.ContentType() {
	case binding.MIMEJSON:
		return binding.JSON
	case binding.MIMEXML, binding.MIMEXML2:
		return binding.XML
	case binding.MIMEPROTOBUF:
		return protobufBinding
	case binding.MIMEMSGPACK, binding.MIMEMSGPACK2:
		return binding.MsgPack
	case binding.MIMEYAML, binding.MIMEYAML2:
		return binding.YAML
	case binding.MIMETOML:
		return binding.TOML
	case binding.MIMEMultipartPOSTForm:
		return binding.FormMultipart
	case binding.MIMEPOSTForm:
		return binding.Form
	}

	return nil
}

func getTagBinders(tags []string) (binders []binding.Binding) {
	for _, tag := range tags {
		switch tag {
		case "form":
			binders = append(binders, binding.Form, binding.Query)
		case "header":
			binders = append(binders, binding.Header)
		case "json":
			binders = append(binders, binding.JSON)
		case "yaml":
			binders = append(binders, binding.YAML)
		case "xml":
			binders = append(binders, binding.XML)
		case "protobuf":
			binders = append(binders, protobufBinding)
		case "msgpack":
			binders = append(binders, binding.MsgPack)
		case "toml":
			binders = append(binders, binding.TOML)
		case "plain":
			binders = append(binders, binding.JSON)
		}
	}

	return
}

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

func reportGinError(ginCtx *gin.Context, err error) {
	reportGinErrorWithType(ginCtx, err, gin.ErrorTypePrivate)
}

func reportGinErrorWithType(ginCtx *gin.Context, err error, errType gin.ErrorType) {
	ginErr := ginCtx.Error(err)
	ginErr.Type = errType
}

func hasBodylessResponse(request *http.Request, statusCode int) bool {
	if request != nil && request.Method == http.MethodHead {
		return true
	}

	return statusCode >= 100 && statusCode < 200 || statusCode == http.StatusNoContent || statusCode == http.StatusNotModified
}

// NoBodyBinding is a no-op binder for handlers that must not bind the request body.
// Pass it explicitly to prevent automatic body binding based on the request
// content type or input tags.
type NoBodyBinding struct{}

func (NoBodyBinding) Name() string {
	return "noBody"
}

func (NoBodyBinding) Bind(_ *http.Request, _ any) error {
	return nil
}
