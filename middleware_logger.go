package httpserver

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/justtrackio/gosoline/pkg/clock"
	"github.com/justtrackio/gosoline/pkg/encoding/base64"
	"github.com/justtrackio/gosoline/pkg/exec"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/reqctx"
)

type logCall struct {
	logger   log.Logger
	settings LoggingSettings
	fields   log.Fields
}

func LoggingMiddleware(logger log.Logger, settings LoggingSettings) fiber.Handler {
	logger = logger.WithChannel("http")

	return NewLoggingMiddlewareWithInterfaces(logger, settings, clock.Provider)
}

func NewLoggingMiddlewareWithInterfaces(logger log.Logger, settings LoggingSettings, clock clock.Clock) fiber.Handler {
	return func(reqCtx fiber.Ctx) error {
		start := clock.Now()
		request := reqCtx.Request()

		ctx := reqCtx.Context()
		ctx = log.InitContext(ctx)
		ctx = reqctx.New(ctx)

		if requestId := request.Header.Peek("X-Request-Id"); len(requestId) != 0 {
			ctx = log.MutateGlobalContextFields(ctx, map[string]any{
				"request_id": string(requestId),
			})
		}

		if sessionId := request.Header.Peek("X-Session-Id"); len(sessionId) != 0 {
			ctx = log.MutateGlobalContextFields(ctx, map[string]any{
				"session_id": string(sessionId),
			})
		}

		reqCtx.SetContext(ctx)

		lp := newLogCall(logger, settings)
		lp.prepare(reqCtx)

		reqErr := reqCtx.Next()

		ctx = reqCtx.Context()
		requestTimeSeconds := clock.Since(start).Seconds()

		lp.finalize(reqCtx, requestTimeSeconds, reqErr)

		return reqErr
	}
}

func newLogCall(logger log.Logger, settings LoggingSettings) *logCall {
	return &logCall{
		logger:   logger,
		settings: settings,
		fields:   log.Fields{},
	}
}

func (lc *logCall) prepare(reqCtx fiber.Ctx) {

	lc.fields["bytes"] = reqCtx.Response().Header.ContentLength()
	lc.fields["client_ip"] = reqCtx.IP()
	lc.fields["host"] = reqCtx.Host()
	lc.fields["protocol"] = reqCtx.Protocol()
	lc.fields["request_method"] = reqCtx.Method()
	lc.fields["request_path"] = reqCtx.OriginalURL()
	lc.fields["request_query"] = string(reqCtx.Request().URI().QueryString())
	lc.fields["request_referer"] = string(reqCtx.RequestCtx().Referer())
	lc.fields["request_user_agent"] = string(reqCtx.RequestCtx().UserAgent())
	lc.fields["scheme"] = reqCtx.Scheme()

	if !lc.settings.RequestBody {
		return
	}

	body := reqCtx.Body()
	if lc.settings.RequestBodyBase64 {
		lc.fields["request_body"] = string(base64.Encode(body))
	} else {
		lc.fields["request_body"] = string(body)
	}
}

func (lc *logCall) finalize(reqCtx fiber.Ctx, requestTimeSecond float64, reqErr error) {
	status := reqCtx.Response().StatusCode()

	// these fields can only be added after all previous handlers have finished
	//lc.fields = funk.MergeMaps(lc.fields, getRequestSizeFields(ginCtx))
	lc.fields["bytes"] = reqCtx.Response().Header.ContentLength()
	lc.fields["request_time"] = requestTimeSecond
	lc.fields["status"] = status

	// only log query parameters in full for successful requests to avoid logging them from bad crawlers
	if status != http.StatusUnauthorized && status != http.StatusForbidden && status != http.StatusNotFound {
		queryParameters := make(map[string]string)

		for k, v := range reqCtx.Queries() {
			queryParameters[k] = v
		}

		lc.fields["request_query_parameters"] = queryParameters
	}

	logger := lc.logger.WithFields(lc.fields)
	method, path, proto := lc.fields["request_method"], lc.fields["request_path"], lc.fields["protocol"]

	if reqErr == nil {
		logger.Info(reqCtx.Context(), "%s %s %s", method, path, proto)

		return
	}

	switch {
	case exec.IsRequestCanceled(reqErr):
		logger.Info(reqCtx.Context(), "%s %s %s - request canceled: %s", method, path, proto, reqErr)
	case exec.IsConnectionError(reqErr):
		logger.Info(reqCtx.Context(), "%s %s %s - connection error: %s", method, path, proto, reqErr)
	default:
		logger.Error(reqCtx.Context(), "%s %s %s: %w", method, path, proto, reqErr)
	}
}
