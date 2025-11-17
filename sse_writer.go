package httpserver

import (
	"context"
	"fmt"
	"net/http"
)

type (
	SseHandler[I any] func(ctx context.Context, input *I, writer SseWriter) error
	SseResponseWriter interface {
		http.ResponseWriter
		http.Flusher
	}
	SseWriter func(data string) error
)

func NewSseWriter(writer SseResponseWriter) SseWriter {
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Expose-Headers", "Content-Type")

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")

	return func(data string) error {
		if _, err := fmt.Fprintf(writer, "data: %s\n\n", data); err != nil {
			return err
		}
		writer.Flush()

		return nil
	}
}
