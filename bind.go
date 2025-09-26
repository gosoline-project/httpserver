package httpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

func Bind[I any](handler func(ctx context.Context, input *I) (Response, error)) fiber.Handler {
	return BindR[I](func(ctx context.Context, req *fasthttp.Request, input *I) (Response, error) {
		return handler(ctx, input)
	})
}

func BindR[I any](handler func(ctx context.Context, req *fasthttp.Request, input *I) (Response, error)) fiber.Handler {
	return func(reqCtx fiber.Ctx) error {
		var err error
		var response Response

		in := new(I)
		if err = reqCtx.Bind().All(in); err != nil {
			return fmt.Errorf("bind error: %w", err)
		}

		if response, err = handler(reqCtx, reqCtx.Request(), in); err != nil {
			return fmt.Errorf("handler error: %w", err)
		}

		return bindHandleResponse(response, reqCtx)
	}
}

func BindN(handler func(ctx context.Context) (Response, error)) fiber.Handler {
	return BindNR(func(ctx context.Context, req *fasthttp.Request) (Response, error) {
		return handler(ctx)
	})
}

func BindNR(handler func(ctx context.Context, req *fasthttp.Request) (Response, error)) fiber.Handler {
	return func(reqCtx fiber.Ctx) error {
		var err error
		var response Response

		if response, err = handler(reqCtx, reqCtx.Request()); err != nil {
			return fmt.Errorf("handler error: %w", err)
		}

		return bindHandleResponse(response, reqCtx)
	}
}

func bindHandleResponse(response Response, reqCtx fiber.Ctx) error {
	var err error
	var size, statusCode int
	var header http.Header
	var body []byte

	statusCode = response.StatusCode()
	header = response.Header()

	if body, err = response.Body(); err != nil {
		return fmt.Errorf("body read error: %w", err)
	}

	for key, values := range header {
		for _, value := range values {
			reqCtx.Set(key, value)
		}
	}

	if size, err = reqCtx.Write(body); err != nil {
		return fmt.Errorf("body write error: %w", err)
	}

	reqCtx.Status(statusCode)
	reqCtx.Response().Header.SetContentLength(size)

	return nil
}
