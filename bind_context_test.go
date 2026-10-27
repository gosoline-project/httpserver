package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bindContextKey struct{}

type bindContextModifier struct {
	capture func(context.Context)
}

func (m bindContextModifier) Struct(ctx context.Context, _ any) error {
	m.capture(ctx)

	return nil
}

func TestBindHandlersPreserveRequestContextAfterGinReuse(t *testing.T) {
	cases := []struct {
		name     string
		fallback bool
		bind     func(func(context.Context)) gin.HandlerFunc
	}{
		{
			name:     "BindR",
			fallback: true,
			bind: func(capture func(context.Context)) gin.HandlerFunc {
				return BindR(func(ctx context.Context, _ *http.Request, _ *struct{}) (Response, error) {
					capture(ctx)

					return NewStatusResponse(http.StatusNoContent), nil
				})
			},
		},
		{
			name:     "BindNR",
			fallback: true,
			bind: func(capture func(context.Context)) gin.HandlerFunc {
				return BindNR(func(ctx context.Context, _ *http.Request) (Response, error) {
					capture(ctx)

					return NewStatusResponse(http.StatusNoContent), nil
				})
			},
		},
		{
			name:     "BindSseR",
			fallback: true,
			bind: func(capture func(context.Context)) gin.HandlerFunc {
				return BindSseR(func(ctx context.Context, _ *http.Request, _ *struct{}, _ *SseWriter) error {
					capture(ctx)

					return nil
				})
			},
		},
		{
			name: "BindSseNR",
			bind: func(capture func(context.Context)) gin.HandlerFunc {
				return BindSseNR(func(ctx context.Context, _ *http.Request, _ *SseWriter) error {
					capture(ctx)

					return nil
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertBoundContextSurvivesGinReuse(t, tc.fallback, func(ginCtx *gin.Context, capture func(context.Context)) {
				tc.bind(capture)(ginCtx)
			})
		})
	}
}

func TestBindHandleRequestModifierPreservesRequestContextAfterGinReuse(t *testing.T) {
	originalModifier := defaultModifier
	t.Cleanup(func() {
		defaultModifier = originalModifier
	})

	assertBoundContextSurvivesGinReuse(t, true, func(ginCtx *gin.Context, capture func(context.Context)) {
		WithCustomModifier(bindContextModifier{capture: capture})
		_, err := BindHandleRequest[struct{}](ginCtx, nil, nil)
		require.NoError(t, err)
	})
}

func assertBoundContextSurvivesGinReuse(t *testing.T, fallback bool, invoke func(*gin.Context, func(context.Context))) {
	t.Helper()

	deadlineA := time.Now().Add(time.Hour)
	parentA := context.WithValue(t.Context(), bindContextKey{}, "typed A")
	ctxA, cancelA := context.WithDeadline(parentA, deadlineA)
	defer cancelA()
	parentB := context.WithValue(t.Context(), bindContextKey{}, "typed B")
	ctxB, cancelB := context.WithDeadline(parentB, deadlineA.Add(time.Hour))
	defer cancelB()

	ginCtx, engine := gin.CreateTestContext(httptest.NewRecorder())
	engine.ContextWithFallback = fallback
	ginCtx.Request = httptest.NewRequestWithContext(ctxA, http.MethodGet, "/a", nil)

	var captured context.Context
	invoke(ginCtx, func(ctx context.Context) {
		captured = ctx
	})
	require.NotNil(t, captured)
	require.Empty(t, ginCtx.Errors)

	// Reuse the exact Gin context after the application handler has returned.
	requestB := httptest.NewRequestWithContext(ctxB, http.MethodGet, "/b", nil)
	ginCtx.Request = requestB
	assert.Equal(t, "typed A", captured.Value(bindContextKey{}))
	deadline, ok := captured.Deadline()
	assert.True(t, ok)
	assert.Equal(t, deadlineA, deadline)

	// An asynchronous consumer may still read A while Gin changes its request.
	started := make(chan struct{})
	stableValues := make(chan bool, 1)
	go func() {
		close(started)
		stable := true
		for range 1000 {
			typedValue := captured.Value(bindContextKey{})
			stable = typedValue == "typed A" && stable
		}
		stableValues <- stable
	}()
	<-started
	otherRequestB := requestB.Clone(ctxB)
	for range 1000 {
		ginCtx.Request = otherRequestB
		ginCtx.Request = requestB
	}
	assert.True(t, <-stableValues, "asynchronous context reads must retain request A values")

	cancelB()
	assert.NoError(t, captured.Err(), "request B cancellation must not affect request A")
	select {
	case <-captured.Done():
		t.Error("request B cancellation closed the application context")
	default:
	}

	cancelA()
	assert.ErrorIs(t, captured.Err(), context.Canceled)
	select {
	case <-captured.Done():
	default:
		t.Error("request A cancellation did not close the application context")
	}
}
