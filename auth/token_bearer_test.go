package auth_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver/auth"
	"github.com/justtrackio/gosoline/pkg/cfg"
	kvStoreMocks "github.com/justtrackio/gosoline/pkg/kvstore/mocks"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/justtrackio/gosoline/pkg/test/matcher"
	"github.com/stretchr/testify/assert"
)

type bearer struct {
	token string
}

func (b *bearer) GetToken() string {
	return b.token
}

type tokenBearerTestCase struct {
	bearerId  string
	token     string
	success   bool
	bearer    *bearer
	bearerErr error
}

func makeKvStoreProvider(t *testing.T, test *tokenBearerTestCase) auth.TokenBearerProvider {
	repo := kvStoreMocks.NewKvStore[bearer](t)

	if test.bearerId != "" && test.token != "" {
		repo.EXPECT().Get(matcher.Context, test.bearerId, &bearer{}).Run(func(ctx context.Context, key any, m *bearer) {
			if test.bearer != nil {
				*m = *test.bearer
			}
		}).Return(test.bearer != nil, test.bearerErr).Once()
	}

	return auth.ProvideTokenBearerFromGetter(func(ctx context.Context, key string, value auth.TokenBearer) (bool, error) {
		return repo.Get(ctx, key, value.(*bearer))
	}, func() auth.TokenBearer {
		return &bearer{}
	})
}

func (test *tokenBearerTestCase) run(t *testing.T) {
	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))
	provider := makeKvStoreProvider(t, test)

	headers := http.Header{}
	headers.Set("X-BEARER-ID", test.bearerId)
	headers.Set("X-BEARER-TOKEN", test.token)

	ginCtx := &gin.Context{
		Request: &http.Request{
			Header: headers,
		},
	}

	a := auth.NewTokenBearerAuthenticatorWithInterfaces(logger, "X-BEARER-ID", "X-BEARER-TOKEN", provider)
	valid, err := a.IsValid(ginCtx)

	assert.Equal(t, test.success, valid)

	if test.success {
		assert.NoError(t, err)
		subject := auth.GetSubject(ginCtx.Request.Context())
		assert.Equal(t, test.bearer, subject.Attributes[auth.AttributeTokenBearer])
	} else {
		assert.Error(t, err)
		assert.Equal(t, auth.InvalidTokenErr{}, err)
	}
}

func TestTokenBearerAuthenticator_IsValid(t *testing.T) {
	for name, test := range makeTokenBearerTestCases() {
		t.Run(fmt.Sprintf("%s-kvStore", name), func(t *testing.T) {
			test.run(t)
		})
	}
}

func TestTokenBearer_NewAuthenticator_ReadsBearerHeadersFromNamedHttpserverAuthConfig(t *testing.T) {
	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))
	config := cfg.New(map[string]any{
		"httpserver": map[string]any{
			"default": map[string]any{
				"auth": map[string]any{
					"bearer": map[string]any{
						"id_header":    "X-BEARER-ID",
						"token_header": "X-BEARER-TOKEN",
					},
				},
			},
		},
	})

	a, err := auth.NewTokenBearerAuthenticator(config, logger, "default", func(ctx context.Context, key string, token string) (auth.TokenBearer, error) {
		assert.Equal(t, "my bearer", key)
		assert.Equal(t, "my token", token)

		return &bearer{token: "my token"}, nil
	})
	assert.NoError(t, err)

	ginCtx := &gin.Context{
		Request: &http.Request{
			Header: http.Header{
				"X-Bearer-Id":    []string{"my bearer"},
				"X-Bearer-Token": []string{"my token"},
			},
		},
	}

	ok, err := a.IsValid(ginCtx)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func makeTokenBearerTestCases() map[string]tokenBearerTestCase {
	return map[string]tokenBearerTestCase{
		"noHeaders": {
			bearerId: "",
			token:    "",
			success:  false,
		},
		"no bearer": {
			bearerId:  "my bearer",
			token:     "my token",
			success:   false,
			bearer:    nil,
			bearerErr: nil,
		},
		"bearer error": {
			bearerId:  "my bearer",
			token:     "my token",
			success:   false,
			bearer:    nil,
			bearerErr: errors.New("this is an error"),
		},
		"invalid token": {
			bearerId: "my bearer",
			token:    "my token",
			success:  false,
			bearer: &bearer{
				token: "not my token",
			},
			bearerErr: nil,
		},
		"valid token": {
			bearerId: "my bearer",
			token:    "my token",
			success:  true,
			bearer: &bearer{
				token: "my token",
			},
			bearerErr: nil,
		},
	}
}
