package auth_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver/auth"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/stretchr/testify/assert"
)

func getBasicAuthMocks(user string, password string) (log.Logger, *gin.Context) {
	logger := logMocks.NewLoggerMock(logMocks.WithMockAll)

	header := http.Header{}
	header.Set("Authorization", fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", user, password)))))

	request := &http.Request{
		Header: header,
	}
	ginCtx := &gin.Context{
		Request: request,
	}

	ginCtx.Request = request

	return logger, ginCtx
}

func TestBasicAuth_Authenticate_InvalidUser(t *testing.T) {
	logger, ginCtx := getBasicAuthMocks("user", "password")

	a := auth.NewBasicAuthAuthenticatorWithInterfaces(logger, map[string]string{
		"other user": "other password",
	})
	_, err := a.IsValid(ginCtx)

	if assert.Error(t, err) {
		assert.Equal(t, "invalid credentials provided", err.Error())
	}
}

func TestBasicAuth_Authenticate_InvalidPassword(t *testing.T) {
	logger, ginCtx := getBasicAuthMocks("user", "password")

	a := auth.NewBasicAuthAuthenticatorWithInterfaces(logger, map[string]string{
		"user": "other password",
	})
	_, err := a.IsValid(ginCtx)

	if assert.Error(t, err) {
		assert.Equal(t, "invalid credentials provided", err.Error())
	}
}

func TestBasicAuth_Authenticate_ValidUser(t *testing.T) {
	logger, ginCtx := getBasicAuthMocks("user", "password")

	a := auth.NewBasicAuthAuthenticatorWithInterfaces(logger, map[string]string{
		"user": "password",
	})
	_, err := a.IsValid(ginCtx)

	assert.Equal(t, nil, err)
}

func TestBasicAuth_Authenticate_ValidUser_SubjectHasCorrectFields(t *testing.T) {
	logger, ginCtx := getBasicAuthMocks("alice", "correctpass")

	a := auth.NewBasicAuthAuthenticatorWithInterfaces(logger, map[string]string{
		"alice": "correctpass",
	})
	ok, err := a.IsValid(ginCtx)

	assert.True(t, ok)
	assert.NoError(t, err)

	subj := auth.GetSubject(ginCtx.Request.Context())
	assert.Equal(t, "anon", subj.Name)
	assert.True(t, subj.Anonymous)
	assert.Equal(t, auth.ByBasicAuth, subj.AuthenticatedBy)
}

func TestBasicAuth_NewAuthenticator_ReadsUsersFromNamedHttpserverAuthConfig(t *testing.T) {
	logger, ginCtx := getBasicAuthMocks("user", "password")
	config := cfg.New(map[string]any{
		"httpserver": map[string]any{
			"default": map[string]any{
				"auth": map[string]any{
					"basic": map[string]any{
						"users": []string{"user:password"},
					},
				},
			},
		},
	})

	a, err := auth.NewBasicAuthAuthenticator(config, logger, "default")
	assert.NoError(t, err)

	ok, err := a.IsValid(ginCtx)
	assert.True(t, ok)
	assert.NoError(t, err)
}
