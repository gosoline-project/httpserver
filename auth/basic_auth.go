package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

const (
	ByBasicAuth = "basicAuth"

	headerBasicAuth = "Authorization"
	AttributeUser   = "user"
)

type basicAuthAuthenticator struct {
	logger log.Logger
	users  map[string]string
}

func NewBasicAuthHandler(config cfg.Config, logger log.Logger, name string) (gin.HandlerFunc, error) {
	var err error
	var auth Authenticator
	var appId cfg.AppId

	if auth, err = NewBasicAuthAuthenticator(config, logger, name); err != nil {
		return nil, fmt.Errorf("can not create basicAuthAuthenticator: %w", err)
	}

	if appId, err = cfg.GetAppIdFromConfig(config); err != nil {
		return nil, fmt.Errorf("can not get app id: %w", err)
	}

	return func(ginCtx *gin.Context) {
		valid, err := auth.IsValid(ginCtx)

		if valid {
			return
		}

		if err == nil {
			err = fmt.Errorf("the user credentials weren't valid nor was there an error")
		}

		ginCtx.Header("www-authenticate", fmt.Sprintf("Basic realm=%q", appId.Application))
		ginCtx.JSON(http.StatusUnauthorized, gin.H{"err": err.Error()})
		ginCtx.Abort()
	}, nil
}

func NewBasicAuthAuthenticator(config cfg.Config, logger log.Logger, name string) (Authenticator, error) {
	var err error
	var userEntries []string

	if userEntries, err = config.GetStringSlice(fmt.Sprintf("%s.basic.users", configAuthKey(name))); err != nil {
		return nil, fmt.Errorf("can not get basic auth users: %w", err)
	}

	users := make(map[string]string)

	for _, user := range userEntries {
		if user == "" {
			continue
		}

		split := strings.SplitN(user, ":", 2)
		if len(split) != 2 {
			return nil, fmt.Errorf("invalid basic auth credentials: %s", user)
		}

		users[split[0]] = split[1]
	}

	return NewBasicAuthAuthenticatorWithInterfaces(logger, users), nil
}

func NewBasicAuthAuthenticatorWithInterfaces(logger log.Logger, users map[string]string) Authenticator {
	return &basicAuthAuthenticator{
		logger: logger,
		users:  users,
	}
}

func (a *basicAuthAuthenticator) IsValid(ginCtx *gin.Context) (bool, error) {
	var err error
	var auth []byte

	basicAuth := ginCtx.GetHeader(headerBasicAuth)

	if basicAuth == "" {
		return false, fmt.Errorf("no credentials provided")
	}

	if !strings.HasPrefix(basicAuth, "Basic ") {
		return false, fmt.Errorf("invalid credentials provided")
	}

	if auth, err = base64.StdEncoding.DecodeString(basicAuth[6:]); err != nil {
		return false, err
	}

	split := strings.SplitN(string(auth), ":", 2)

	if len(split) != 2 {
		return false, fmt.Errorf("invalid credentials provided")
	}

	if password, ok := a.users[split[0]]; ok {
		if subtle.ConstantTimeCompare([]byte(password), []byte(split[1])) != 1 {
			return false, fmt.Errorf("invalid credentials provided")
		}

		user := &Subject{
			Name:            Anonymous,
			Anonymous:       true,
			AuthenticatedBy: ByBasicAuth,
			Attributes: map[string]any{
				AttributeUser: split[0],
			},
		}

		RequestWithSubject(ginCtx, user)

		return true, nil
	}

	return false, fmt.Errorf("invalid credentials provided")
}
