package httpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/justtrackio/gosoline/pkg/funk"
	"github.com/justtrackio/gosoline/pkg/refl"
)

func Bind[I any](handler func(ctx context.Context, input *I) (Response, error), binders ...binding.Binding) gin.HandlerFunc {
	return BindR[I](func(ctx context.Context, _ *http.Request, input *I) (Response, error) {
		return handler(ctx, input)
	}, binders...)
}

func BindR[I any](handler func(ctx context.Context, req *http.Request, input *I) (Response, error), binders ...binding.Binding) gin.HandlerFunc {
	tags := refl.GetTagNames(new(I))

	return func(ginCtx *gin.Context) {
		var err error
		var input *I
		var response Response

		if input, err = BindHandleRequest[I](ginCtx, tags, binders); err != nil {
			ginCtx.Error(fmt.Errorf("bind error: %w", err))

			return
		}

		if response, err = handler(ginCtx, ginCtx.Request, input); err != nil {
			ginCtx.Error(fmt.Errorf("handler error: %w", err))

			return
		}

		if err = BindHandleResponse(response, ginCtx); err != nil {
			ginCtx.Error(fmt.Errorf("response error: %w", err))
		}
	}
}

func BindN(handler func(ctx context.Context) (Response, error)) gin.HandlerFunc {
	return BindNR(func(ctx context.Context, _ *http.Request) (Response, error) {
		return handler(ctx)
	})
}

func BindNR(handler func(ctx context.Context, req *http.Request) (Response, error)) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		var err error
		var response Response

		if response, err = handler(ginCtx, ginCtx.Request); err != nil {
			ginCtx.Error(fmt.Errorf("handler error: %w", err))

			return
		}

		if err = BindHandleResponse(response, ginCtx); err != nil {
			ginCtx.Error(fmt.Errorf("response error: %w", err))
		}
	}
}

func BindSse[I any](handler func(ctx context.Context, input *I, writer SseWriter) error, binders ...binding.Binding) gin.HandlerFunc {
	return BindSseR[I](func(ctx context.Context, _ *http.Request, input *I, writer SseWriter) error {
		return handler(ctx, input, writer)
	}, binders...)
}

func BindSseR[I any](handler func(ctx context.Context, req *http.Request, input *I, writer SseWriter) error, binders ...binding.Binding) gin.HandlerFunc {
	tags := refl.GetTagNames(new(I))

	return func(ginCtx *gin.Context) {
		var err error
		var input *I

		if input, err = BindHandleRequest[I](ginCtx, tags, binders); err != nil {
			ginCtx.Error(fmt.Errorf("bind error: %w", err))

			return
		}

		writer := NewSseWriter(ginCtx.Writer)
		if err = handler(ginCtx, ginCtx.Request, input, writer); err != nil {
			ginCtx.Error(fmt.Errorf("handler error: %w", err))
		}
	}
}

func BindSseN(handler func(ctx context.Context, writer SseWriter) error) gin.HandlerFunc {
	return BindSseNR(func(ctx context.Context, _ *http.Request, writer SseWriter) error {
		return handler(ctx, writer)
	})
}

func BindSseNR(handler func(ctx context.Context, req *http.Request, writer SseWriter) error, binders ...binding.Binding) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		var err error

		writer := NewSseWriter(ginCtx.Writer)
		if err = handler(ginCtx, ginCtx.Request, writer); err != nil {
			ginCtx.Error(fmt.Errorf("handler error: %w", err))
		}
	}
}

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
		return binding.ProtoBuf
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
			binders = append(binders, binding.ProtoBuf)
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

func BindHandleResponse(response Response, ginCtx *gin.Context) error {
	var err error
	var statusCode int
	var header http.Header
	var body []byte

	statusCode = response.StatusCode()
	header = response.Header()

	if body, err = response.Body(); err != nil {
		return fmt.Errorf("body read error: %w", err)
	}

	for key, values := range header {
		for _, value := range values {
			ginCtx.Header(key, value)
		}
	}

	ginCtx.Status(statusCode)
	ginCtx.Writer.WriteHeaderNow()

	if _, err = ginCtx.Writer.Write(body); err != nil {
		return fmt.Errorf("body write error: %w", err)
	}

	return nil
}
